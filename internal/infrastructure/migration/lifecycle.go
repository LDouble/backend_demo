package migration

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/generator"
	"gopkg.in/yaml.v3"
)

const manifestVersion = 1

var (
	formalMigrationPattern = regexp.MustCompile(`^(\d{6}|\d{14})_([a-z][a-z0-9_]*)\.(up|down)\.sql$`)
	draftNamePattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)
)

// ChangePlan is a deterministic schema-to-snapshot migration proposal.
type ChangePlan struct {
	Module      string   `json:"module"`
	UpSQL       string   `json:"up_sql"`
	DownSQL     string   `json:"down_sql"`
	Destructive []string `json:"destructive,omitempty"`
}

// Migration describes one paired formal migration.
type Migration struct {
	Version string `json:"version"`
	Name    string `json:"name"`
	Up      string `json:"up"`
	Down    string `json:"down"`
}

type manifest struct {
	Version int             `json:"version"`
	Files   []manifestEntry `json:"files"`
}

type manifestEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Snapshot stores the current normalized module schema as the promote baseline.
func Snapshot(ctx context.Context, root, module string) error {
	if module == "core" {
		manifestPath, pathErr := repositoryPath(root, "migrations/manifest.json")
		if pathErr != nil {
			return pathErr
		}
		if !lifecycleFileExists(root, manifestPath) {
			return refreshManifest(root)
		}
		return checkManifest(root)
	}
	schema, err := loadModuleSchema(ctx, root, module)
	if err != nil {
		return err
	}
	manifestPath, pathErr := repositoryPath(root, "migrations/manifest.json")
	if pathErr != nil {
		return pathErr
	}
	if !lifecycleFileExists(root, manifestPath) {
		if err = refreshManifest(root); err != nil {
			return err
		}
	} else if err = checkManifest(root); err != nil {
		return err
	}
	return writeSnapshot(root, schema)
}

// Plan compares the promoted snapshot with the current module schema.
func Plan(ctx context.Context, root, module string) (ChangePlan, error) {
	current, err := loadModuleSchema(ctx, root, module)
	if err != nil {
		return ChangePlan{}, err
	}
	path, err := repositoryPath(root, ".agent/schema-snapshots/"+module+".yaml")
	if err != nil {
		return ChangePlan{}, err
	}
	// #nosec G304 -- repositoryPath confines snapshots to the repository root.
	data, err := readLifecycleFile(root, path)
	if errors.Is(err, fs.ErrNotExist) {
		return planSchemas(generator.Schema{Version: current.Version, Module: module}, current), nil
	}
	if err != nil {
		return ChangePlan{}, fmt.Errorf("read schema snapshot: %w", err)
	}
	var previous generator.Schema
	if err = yaml.Unmarshal(data, &previous); err != nil {
		return ChangePlan{}, fmt.Errorf("decode schema snapshot: %w", err)
	}
	if err = previous.Normalize(); err != nil {
		return ChangePlan{}, fmt.Errorf("normalize schema snapshot: %w", err)
	}
	return planSchemas(previous, current), nil
}

// NewDraft writes a reviewed migration proposal under migrations/drafts.
func NewDraft(ctx context.Context, root, name, module string) (Migration, error) {
	if !draftNamePattern.MatchString(name) {
		return Migration{}, fmt.Errorf("invalid migration name %q", name)
	}
	plan, err := Plan(ctx, root, module)
	if err != nil {
		return Migration{}, err
	}
	if len(plan.Destructive) > 0 {
		return Migration{}, fmt.Errorf("destructive schema changes require explicit SQL review: %s", strings.Join(plan.Destructive, "; "))
	}
	upPath, err := repositoryPath(root, "migrations/drafts/"+name+".up.sql")
	if err != nil {
		return Migration{}, err
	}
	downPath, err := repositoryPath(root, "migrations/drafts/"+name+".down.sql")
	if err != nil {
		return Migration{}, err
	}
	if lifecycleFileExists(root, upPath) || lifecycleFileExists(root, downPath) {
		return Migration{}, fmt.Errorf("migration draft %q already exists", name)
	}
	header := "-- module: " + module + "\n"
	if err = atomicWriteLifecycle(root, upPath, []byte(header+sqlOrNoop(plan.UpSQL))); err != nil {
		return Migration{}, err
	}
	if err = atomicWriteLifecycle(root, downPath, []byte(header+sqlOrNoop(plan.DownSQL))); err != nil {
		return Migration{}, err
	}
	return Migration{Name: name, Up: upPath, Down: downPath}, nil
}

// Promote validates and atomically assigns a UTC timestamp version to a draft pair.
func Promote(ctx context.Context, root, draft string, now time.Time) (Migration, error) {
	if err := ctx.Err(); err != nil {
		return Migration{}, err
	}
	name := strings.TrimSuffix(filepath.Base(draft), ".up.sql")
	name = strings.TrimSuffix(name, ".down.sql")
	if !draftNamePattern.MatchString(name) {
		return Migration{}, fmt.Errorf("invalid migration draft %q", draft)
	}
	upDraft, err := repositoryPath(root, "migrations/drafts/"+name+".up.sql")
	if err != nil {
		return Migration{}, err
	}
	downDraft, err := repositoryPath(root, "migrations/drafts/"+name+".down.sql")
	if err != nil {
		return Migration{}, err
	}
	// #nosec G304 -- repositoryPath and draftNamePattern confine this draft to migrations/drafts.
	up, err := readLifecycleFile(root, upDraft)
	if err != nil {
		return Migration{}, fmt.Errorf("read up draft: %w", err)
	}
	// #nosec G304 -- repositoryPath and draftNamePattern confine this draft to migrations/drafts.
	down, err := readLifecycleFile(root, downDraft)
	if err != nil {
		return Migration{}, fmt.Errorf("read down draft: %w", err)
	}
	if strings.TrimSpace(string(up)) == "" || strings.TrimSpace(string(down)) == "" {
		return Migration{}, fmt.Errorf("migration drafts must contain both up and down SQL")
	}
	module := draftModule(up)
	if module == "" || module != draftModule(down) {
		return Migration{}, fmt.Errorf("migration draft module headers do not match")
	}
	manifestPath, err := repositoryPath(root, "migrations/manifest.json")
	if err != nil {
		return Migration{}, err
	}
	if lifecycleFileExists(root, manifestPath) {
		if err = checkManifest(root); err != nil {
			return Migration{}, err
		}
	}
	version, err := nextVersion(root, now.UTC())
	if err != nil {
		return Migration{}, err
	}
	base := version + "_" + name
	upFinal, _ := repositoryPath(root, "migrations/"+base+".up.sql")
	downFinal, _ := repositoryPath(root, "migrations/"+base+".down.sql")
	if err = atomicWriteLifecycle(root, upFinal, up); err != nil {
		return Migration{}, err
	}
	if err = atomicWriteLifecycle(root, downFinal, down); err != nil {
		_ = removeLifecycleFile(root, upFinal)
		return Migration{}, err
	}
	// Promote is the only lifecycle operation allowed to extend the immutable
	// manifest. Snapshot must reject checksum or membership mismatches.
	if err = refreshManifest(root); err != nil {
		_ = removeLifecycleFile(root, upFinal)
		_ = removeLifecycleFile(root, downFinal)
		return Migration{}, err
	}
	if err = Snapshot(ctx, root, module); err != nil {
		_ = removeLifecycleFile(root, upFinal)
		_ = removeLifecycleFile(root, downFinal)
		_ = refreshManifest(root)
		return Migration{}, err
	}
	if err = removeLifecycleFile(root, upDraft); err != nil {
		return Migration{}, fmt.Errorf("remove promoted up draft: %w", err)
	}
	if err = removeLifecycleFile(root, downDraft); err != nil {
		return Migration{}, fmt.Errorf("remove promoted down draft: %w", err)
	}
	return Migration{Version: version, Name: name, Up: upFinal, Down: downFinal}, nil
}

// Check verifies migration pairing, versions, checksums, drafts and schema drift.
func Check(ctx context.Context, root string) error {
	migrations, err := List(root)
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		return fmt.Errorf("no formal migrations found")
	}
	if err = checkManifest(root); err != nil {
		return err
	}
	draftDirectory, err := repositoryPath(root, "migrations/drafts")
	if err != nil {
		return err
	}
	draftEntries, err := readLifecycleDir(root, draftDirectory)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("list migration drafts: %w", err)
	}
	drafts := []string{}
	for _, entry := range draftEntries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			drafts = append(drafts, filepath.Join(draftDirectory, entry.Name()))
		}
	}
	if len(drafts) > 0 {
		return fmt.Errorf("unpromoted migration drafts: %s", strings.Join(drafts, ", "))
	}
	modules, err := generator.ListModules(ctx, root)
	if err != nil {
		return err
	}
	for _, module := range modules {
		plan, planErr := Plan(ctx, root, module.Name)
		if planErr != nil {
			return planErr
		}
		if strings.TrimSpace(plan.UpSQL) != "" || len(plan.Destructive) > 0 {
			return fmt.Errorf("schema drift for module %q", module.Name)
		}
	}
	return nil
}

// List returns all paired formal migrations in version order.
func List(root string) ([]Migration, error) {
	directory, err := repositoryPath(root, "migrations")
	if err != nil {
		return nil, err
	}
	entries, err := readLifecycleDir(root, directory)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}
	paired := map[string]*Migration{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := formalMigrationPattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			if strings.HasSuffix(entry.Name(), ".sql") {
				return nil, fmt.Errorf("invalid formal migration filename %q", entry.Name())
			}
			continue
		}
		key := matches[1] + "_" + matches[2]
		value := paired[key]
		if value == nil {
			value = &Migration{Version: matches[1], Name: matches[2]}
			paired[key] = value
		}
		path := filepath.Join(directory, entry.Name())
		if matches[3] == "up" {
			if value.Up != "" {
				return nil, fmt.Errorf("duplicate up migration %q", key)
			}
			value.Up = path
		} else {
			if value.Down != "" {
				return nil, fmt.Errorf("duplicate down migration %q", key)
			}
			value.Down = path
		}
	}
	result := make([]Migration, 0, len(paired))
	seenVersion := map[string]string{}
	for key, value := range paired {
		if value.Up == "" || value.Down == "" {
			return nil, fmt.Errorf("migration %q is missing up/down pair", key)
		}
		if previous, exists := seenVersion[value.Version]; exists {
			return nil, fmt.Errorf("migration version %s reused by %s and %s", value.Version, previous, value.Name)
		}
		seenVersion[value.Version] = value.Name
		result = append(result, *value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	return result, nil
}

func planSchemas(previous, current generator.Schema) ChangePlan {
	plan := ChangePlan{Module: current.Module, Destructive: []string{}}
	oldEntities := entitiesByTable(previous)
	newEntities := entitiesByTable(current)
	up := []string{}
	down := []string{}
	for table, oldEntity := range oldEntities {
		if _, exists := newEntities[table]; !exists {
			plan.Destructive = append(plan.Destructive, "drop table "+table)
			_ = oldEntity
		}
	}
	for _, entity := range schemaEntities(current) {
		oldEntity, exists := oldEntities[entity.Table]
		if !exists {
			up = append(up, createTableSQL(entity))
			down = append([]string{"DROP TABLE " + entity.Table + ";"}, down...)
			continue
		}
		entityUp := []string{}
		fieldDown := []string{}
		oldFields := fieldsByName(oldEntity.Fields)
		newFields := fieldsByName(entity.Fields)
		for _, field := range entity.Fields {
			old, found := oldFields[field.Name]
			if !found && field.RenameFrom != "" {
				old, found = oldFields[field.RenameFrom]
				if found {
					entityUp = append(entityUp, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s;", entity.Table, field.RenameFrom, field.Name))
					fieldDown = append([]string{fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s;", entity.Table, field.Name, field.RenameFrom)}, fieldDown...)
				}
			}
			if !found {
				entityUp = append(entityUp, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s%s;", entity.Table, field.Name, field.SQLType, nullability(field.Required)))
				fieldDown = append([]string{fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", entity.Table, field.Name)}, fieldDown...)
				continue
			}
			if field.Type != old.Type || (field.Type == "string" && field.Size < old.Size) || (!old.Required && field.Required) {
				plan.Destructive = append(plan.Destructive, fmt.Sprintf("change column %s.%s", entity.Table, field.Name))
			} else if field.SQLType != old.SQLType || field.Required != old.Required {
				entityUp = append(entityUp, fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s %s%s;", entity.Table, field.Name, field.SQLType, nullability(field.Required)))
			}
		}
		for name := range oldFields {
			if _, exists := newFields[name]; exists || renamedFrom(entity.Fields, name) {
				continue
			}
			plan.Destructive = append(plan.Destructive, "drop column "+entity.Table+"."+name)
		}

		oldForeignKeys := foreignKeysByField(oldEntity.ForeignKeys)
		newForeignKeys := foreignKeysByField(entity.ForeignKeys)
		foreignKeyDropsUp := []string{}
		foreignKeyAddsUp := []string{}
		foreignKeyDropsDown := []string{}
		foreignKeyAddsDown := []string{}
		for _, foreignKey := range entity.ForeignKeys {
			oldForeignKey, found := oldForeignKeys[foreignKey.Field]
			if !found {
				foreignKeyAddsUp = append(foreignKeyAddsUp, addForeignKeySQL(entity.Table, foreignKey))
				foreignKeyDropsDown = append(foreignKeyDropsDown, dropForeignKeySQL(entity.Table, foreignKey))
				continue
			}
			if foreignKeysEqual(oldForeignKey, foreignKey) {
				continue
			}
			plan.Destructive = append(plan.Destructive, "change foreign key "+entity.Table+"."+foreignKey.Field)
			foreignKeyDropsUp = append(foreignKeyDropsUp, dropForeignKeySQL(entity.Table, oldForeignKey))
			foreignKeyAddsUp = append(foreignKeyAddsUp, addForeignKeySQL(entity.Table, foreignKey))
			foreignKeyDropsDown = append(foreignKeyDropsDown, dropForeignKeySQL(entity.Table, foreignKey))
			foreignKeyAddsDown = append(foreignKeyAddsDown, addForeignKeySQL(entity.Table, oldForeignKey))
		}
		for field, oldForeignKey := range oldForeignKeys {
			if _, found := newForeignKeys[field]; found {
				continue
			}
			plan.Destructive = append(plan.Destructive, "drop foreign key "+entity.Table+"."+field)
			foreignKeyDropsUp = append(foreignKeyDropsUp, dropForeignKeySQL(entity.Table, oldForeignKey))
			foreignKeyAddsDown = append(foreignKeyAddsDown, addForeignKeySQL(entity.Table, oldForeignKey))
		}

		indexUp := []string{}
		indexDown := []string{}
		oldIndexes := indexesByName(oldEntity.Indexes)
		newIndexes := indexesByName(entity.Indexes)
		for _, index := range entity.Indexes {
			oldIndex, found := oldIndexes[index.Name]
			if !found {
				indexUp = append(indexUp, addIndexSQL(entity.Table, index))
				indexDown = append(indexDown, dropIndexSQL(entity.Table, index.Name))
				continue
			}
			if !indexesEqual(oldIndex, index) {
				plan.Destructive = append(plan.Destructive, "change index "+entity.Table+"."+index.Name)
				indexUp = append(indexUp, dropIndexSQL(entity.Table, index.Name), addIndexSQL(entity.Table, index))
				indexDown = append(indexDown, dropIndexSQL(entity.Table, index.Name), addIndexSQL(entity.Table, oldIndex))
			}
		}
		for name, oldIndex := range oldIndexes {
			if _, exists := newIndexes[name]; !exists {
				plan.Destructive = append(plan.Destructive, "drop index "+entity.Table+"."+name)
				indexUp = append(indexUp, dropIndexSQL(entity.Table, name))
				indexDown = append(indexDown, addIndexSQL(entity.Table, oldIndex))
			}
		}

		entityUp = append(entityUp, foreignKeyDropsUp...)
		entityUp = append(entityUp, indexUp...)
		entityUp = append(entityUp, foreignKeyAddsUp...)
		entityDown := make(
			[]string,
			0,
			len(foreignKeyDropsDown)+len(indexDown)+len(foreignKeyAddsDown)+len(fieldDown),
		)
		entityDown = append(entityDown, foreignKeyDropsDown...)
		entityDown = append(entityDown, indexDown...)
		entityDown = append(entityDown, foreignKeyAddsDown...)
		entityDown = append(entityDown, fieldDown...)
		up = append(up, entityUp...)
		down = append(entityDown, down...)
	}
	sort.Strings(plan.Destructive)
	plan.UpSQL = strings.Join(up, "\n")
	plan.DownSQL = strings.Join(down, "\n")
	return plan
}

func createTableSQL(entity generator.Entity) string {
	columns := []string{"id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT"}
	for _, field := range entity.Fields {
		columns = append(columns, field.Name+" "+field.SQLType+nullability(field.Required))
	}
	columns = append(columns, "created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)")
	columns = append(columns, "updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)")
	columns = append(columns, "PRIMARY KEY (id)")
	for _, index := range entity.Indexes {
		prefix := "KEY "
		if index.Unique {
			prefix = "UNIQUE KEY "
		}
		columns = append(columns, prefix+index.Name+" ("+strings.Join(index.Fields, ", ")+")")
	}
	for _, foreignKey := range entity.ForeignKeys {
		columns = append(columns, fmt.Sprintf(
			"CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s(%s) ON DELETE %s",
			foreignKeyConstraintName(entity.Table, foreignKey.Field),
			foreignKey.Field,
			foreignKey.RefTable,
			foreignKey.RefField,
			foreignKey.OnDelete,
		))
	}
	return "CREATE TABLE " + entity.Table + " (\n  " + strings.Join(columns, ",\n  ") + "\n);"
}

func loadModuleSchema(ctx context.Context, root, module string) (generator.Schema, error) {
	if !draftNamePattern.MatchString(module) {
		return generator.Schema{}, fmt.Errorf("invalid module %q", module)
	}
	return generator.LoadRepository(ctx, root, "schemas/"+module+".yaml")
}

func writeSnapshot(root string, schema generator.Schema) error {
	data, err := yaml.Marshal(schema)
	if err != nil {
		return fmt.Errorf("encode schema snapshot: %w", err)
	}
	path, err := repositoryPath(root, ".agent/schema-snapshots/"+schema.Module+".yaml")
	if err != nil {
		return err
	}
	return atomicReplaceLifecycle(root, path, data)
}

func nextVersion(root string, now time.Time) (string, error) {
	directory, err := repositoryPath(root, "migrations")
	if err != nil {
		return "", err
	}
	entries, err := readLifecycleDir(root, directory)
	if err != nil {
		return "", fmt.Errorf("check migration version: %w", err)
	}
	for attempts := 0; attempts < 60; attempts++ {
		version := now.Add(time.Duration(attempts) * time.Second).Format("20060102150405")
		available := true
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), version+"_") && strings.HasSuffix(entry.Name(), ".sql") {
				available = false
				break
			}
		}
		if available {
			return version, nil
		}
	}
	return "", fmt.Errorf("could not allocate a unique migration timestamp")
}

func refreshManifest(root string) error {
	migrations, err := List(root)
	if err != nil {
		return err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	value := manifest{Version: manifestVersion, Files: []manifestEntry{}}
	for _, migration := range migrations {
		for _, path := range []string{migration.Up, migration.Down} {
			// #nosec G304 -- List returns only paired migration files discovered under the repository migration root.
			data, readErr := readLifecycleFile(root, path)
			if readErr != nil {
				return readErr
			}
			relative, relErr := filepath.Rel(absoluteRoot, path)
			if relErr != nil {
				return fmt.Errorf("format migration manifest path: %w", relErr)
			}
			value.Files = append(value.Files, manifestEntry{Path: filepath.ToSlash(relative), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))})
		}
	}
	sort.Slice(value.Files, func(i, j int) bool { return value.Files[i].Path < value.Files[j].Path })
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	path, err := repositoryPath(root, "migrations/manifest.json")
	if err != nil {
		return err
	}
	return atomicReplaceLifecycle(root, path, append(data, '\n'))
}

func checkManifest(root string) error {
	path, err := repositoryPath(root, "migrations/manifest.json")
	if err != nil {
		return err
	}
	// #nosec G304 -- repositoryPath confines the manifest to the repository root.
	data, err := readLifecycleFile(root, path)
	if err != nil {
		return fmt.Errorf("read migration manifest: %w", err)
	}
	var value manifest
	if err = json.Unmarshal(data, &value); err != nil || value.Version != manifestVersion {
		return fmt.Errorf("invalid migration manifest")
	}
	migrations, err := List(root)
	if err != nil {
		return err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	expected := make(map[string]struct{}, len(migrations)*2)
	for _, migration := range migrations {
		for _, migrationPath := range []string{migration.Up, migration.Down} {
			relative, relErr := filepath.Rel(absoluteRoot, migrationPath)
			if relErr != nil {
				return fmt.Errorf("format immutable migration path: %w", relErr)
			}
			expected[filepath.ToSlash(relative)] = struct{}{}
		}
	}
	actual := make(map[string]struct{}, len(value.Files))
	for _, entry := range value.Files {
		if _, exists := expected[entry.Path]; !exists {
			return fmt.Errorf("migration manifest contains unknown file: %s", entry.Path)
		}
		if _, exists := actual[entry.Path]; exists {
			return fmt.Errorf("migration manifest contains duplicate file: %s", entry.Path)
		}
		actual[entry.Path] = struct{}{}
		migrationPath, pathErr := repositoryPath(root, entry.Path)
		if pathErr != nil {
			return pathErr
		}
		// #nosec G304 -- repositoryPath rejects absolute and non-local manifest entries.
		content, readErr := readLifecycleFile(root, migrationPath)
		if readErr != nil {
			return fmt.Errorf("read immutable migration %s: %w", entry.Path, readErr)
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(content))
		if checksum != entry.SHA256 {
			return fmt.Errorf("immutable migration checksum changed: %s", entry.Path)
		}
	}
	for path := range expected {
		if _, exists := actual[path]; !exists {
			return fmt.Errorf("migration manifest is missing file: %s", path)
		}
	}
	return nil
}

func repositoryPath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) || !filepath.IsLocal(relative) {
		return "", fmt.Errorf("path must be local to repository: %q", relative)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	path := filepath.Join(absoluteRoot, filepath.FromSlash(relative))
	rel, err := filepath.Rel(absoluteRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository root: %q", relative)
	}
	return path, nil
}

func schemaEntities(schema generator.Schema) []generator.Entity {
	if schema.Version == 1 {
		if schema.Entity.Name == "" {
			return []generator.Entity{}
		}
		return []generator.Entity{schema.Entity}
	}
	return schema.Entities
}

func entitiesByTable(schema generator.Schema) map[string]generator.Entity {
	result := map[string]generator.Entity{}
	for _, entity := range schemaEntities(schema) {
		result[entity.Table] = entity
	}
	return result
}

func fieldsByName(fields []generator.Field) map[string]generator.Field {
	result := make(map[string]generator.Field, len(fields))
	for _, field := range fields {
		result[field.Name] = field
	}
	return result
}

func indexesByName(indexes []generator.Index) map[string]generator.Index {
	result := make(map[string]generator.Index, len(indexes))
	for _, index := range indexes {
		result[index.Name] = index
	}
	return result
}

func foreignKeysByField(foreignKeys []generator.ForeignKey) map[string]generator.ForeignKey {
	result := make(map[string]generator.ForeignKey, len(foreignKeys))
	for _, foreignKey := range foreignKeys {
		result[foreignKey.Field] = foreignKey
	}
	return result
}

func indexesEqual(left, right generator.Index) bool {
	return left.Unique == right.Unique && slices.Equal(left.Fields, right.Fields)
}

func foreignKeysEqual(left, right generator.ForeignKey) bool {
	return left.Field == right.Field &&
		left.RefTable == right.RefTable &&
		left.RefField == right.RefField &&
		left.OnDelete == right.OnDelete
}

func addIndexSQL(table string, index generator.Index) string {
	unique := ""
	if index.Unique {
		unique = "UNIQUE "
	}
	return fmt.Sprintf(
		"ALTER TABLE %s ADD %sINDEX %s (%s);",
		table,
		unique,
		index.Name,
		strings.Join(index.Fields, ", "),
	)
}

func dropIndexSQL(table, name string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP INDEX %s;", table, name)
}

func addForeignKeySQL(table string, foreignKey generator.ForeignKey) string {
	return fmt.Sprintf(
		"ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s(%s) ON DELETE %s;",
		table,
		foreignKeyConstraintName(table, foreignKey.Field),
		foreignKey.Field,
		foreignKey.RefTable,
		foreignKey.RefField,
		foreignKey.OnDelete,
	)
}

func dropForeignKeySQL(table string, foreignKey generator.ForeignKey) string {
	return fmt.Sprintf(
		"ALTER TABLE %s DROP FOREIGN KEY %s;",
		table,
		foreignKeyConstraintName(table, foreignKey.Field),
	)
}

func foreignKeyConstraintName(table, field string) string {
	const maxIdentifierLength = 64
	name := "fk_" + table + "_" + field
	if len(name) <= maxIdentifierLength {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	suffix := fmt.Sprintf("_%x", digest[:4])
	return name[:maxIdentifierLength-len(suffix)] + suffix
}

func renamedFrom(fields []generator.Field, name string) bool {
	for _, field := range fields {
		if field.RenameFrom == name {
			return true
		}
	}
	return false
}

func nullability(required bool) string {
	if required {
		return " NOT NULL"
	}
	return " NULL"
}

func draftModule(data []byte) string {
	first, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimSpace(strings.TrimPrefix(first, "-- module:"))
}

func sqlOrNoop(sql string) string {
	if strings.TrimSpace(sql) == "" {
		return "-- no schema changes\n"
	}
	return sql + "\n"
}
