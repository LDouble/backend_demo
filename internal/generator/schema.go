// Package generator implements deterministic module scaffolding from YAML schemas.
package generator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the current module schema version.
const SchemaVersion = 2

// RegistryVersion is independent from module schema evolution.
const RegistryVersion = 1

var (
	packagePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)
	tablePattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	typePattern    = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{1,63}$`)
	permissionID   = regexp.MustCompile(`^[a-z][a-z0-9_]*:[a-z][a-z0-9_]*$`)
)

// Schema is the versioned declaration used to generate one business module.
type Schema struct {
	Version     int          `yaml:"version"`
	Module      string       `yaml:"module"`
	Entity      Entity       `yaml:"entity"`
	Entities    []Entity     `yaml:"entities"`
	CRUD        []string     `yaml:"crud"`
	Permissions []Permission `yaml:"permissions"`
}

// Entity describes the module's primary persistent entity.
type Entity struct {
	Name        string       `yaml:"name"`
	Table       string       `yaml:"table"`
	Primary     bool         `yaml:"primary,omitempty"`
	Fields      []Field      `yaml:"fields"`
	Indexes     []Index      `yaml:"indexes,omitempty"`
	ForeignKeys []ForeignKey `yaml:"foreign_keys,omitempty"`
}

// Index describes a deterministic multi-column database index.
type Index struct {
	Name   string   `yaml:"name"`
	Fields []string `yaml:"fields"`
	Unique bool     `yaml:"unique,omitempty"`
}

// ForeignKey describes an entity relationship used by generated migrations.
type ForeignKey struct {
	Field      string `yaml:"field"`
	References string `yaml:"references"`
	OnDelete   string `yaml:"on_delete,omitempty"`
	RefTable   string `yaml:"-"`
	RefField   string `yaml:"-"`
}

// Field describes a generated entity and database column.
type Field struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Required bool   `yaml:"required"`
	Size     int    `yaml:"size,omitempty"`
	Unique   bool   `yaml:"unique,omitempty"`
	Index    bool   `yaml:"index,omitempty"`

	GoName   string `yaml:"-"`
	GoType   string `yaml:"-"`
	SQLType  string `yaml:"-"`
	JSONName string `yaml:"-"`
	GORMTag  string `yaml:"-"`
}

// Permission describes a stable Casbin permission emitted for a module API.
type Permission struct {
	Name    string   `yaml:"name" json:"name"`
	Path    string   `yaml:"path" json:"path_pattern"`
	Methods []string `yaml:"methods" json:"methods"`
}

// Load reads, strictly decodes, normalizes and validates one schema.
func Load(ctx context.Context, path string) (Schema, error) {
	if err := ctx.Err(); err != nil {
		return Schema{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Schema{}, fmt.Errorf("read module schema: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var schema Schema
	if err = decoder.Decode(&schema); err != nil {
		return Schema{}, fmt.Errorf("decode module schema: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return Schema{}, err
	}
	if err = schema.Normalize(); err != nil {
		return Schema{}, err
	}
	return schema, nil
}

// Normalize validates the schema and fills deterministic derived values.
func (s *Schema) Normalize() error {
	if s.Version != 1 && s.Version != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", s.Version)
	}
	s.Module = strings.TrimSpace(s.Module)
	if !packagePattern.MatchString(s.Module) || strings.Contains(s.Module, "..") {
		return fmt.Errorf("invalid module name %q", s.Module)
	}
	if s.Version == 2 {
		if len(s.Entities) == 0 {
			return fmt.Errorf("entities are required for schema v2")
		}
		primary := -1
		seenTables := map[string]struct{}{}
		for i := range s.Entities {
			if err := normalizeEntity(&s.Entities[i]); err != nil {
				return fmt.Errorf("entity %d: %w", i+1, err)
			}
			if _, ok := seenTables[s.Entities[i].Table]; ok {
				return fmt.Errorf("duplicate table %q", s.Entities[i].Table)
			}
			seenTables[s.Entities[i].Table] = struct{}{}
			if s.Entities[i].Primary {
				if primary >= 0 {
					return fmt.Errorf("multiple primary entities")
				}
				primary = i
			}
		}
		if primary < 0 {
			primary = 0
			s.Entities[0].Primary = true
		}
		primaryTable := s.Entities[primary].Table
		ordered, err := orderEntities(s.Entities)
		if err != nil {
			return err
		}
		s.Entities = ordered
		for i := range s.Entities {
			if s.Entities[i].Table == primaryTable {
				s.Entity = s.Entities[i]
				break
			}
		}
	} else if err := normalizeEntity(&s.Entity); err != nil {
		return err
	}
	return s.normalizeOperations()
}

func orderEntities(entities []Entity) ([]Entity, error) {
	byTable := make(map[string]Entity, len(entities))
	position := make(map[string]int, len(entities))
	for i, entity := range entities {
		byTable[entity.Table] = entity
		position[entity.Table] = i
	}
	dependencies := make(map[string]map[string]struct{}, len(entities))
	for _, entity := range entities {
		dependencies[entity.Table] = map[string]struct{}{}
		for _, fk := range entity.ForeignKeys {
			parent, ok := byTable[fk.RefTable]
			if !ok {
				return nil, fmt.Errorf("entity %q references undeclared table %q", entity.Name, fk.RefTable)
			}
			if fk.RefField != "id" {
				found := false
				for _, field := range parent.Fields {
					if field.Name == fk.RefField {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("entity %q references unknown column %q", entity.Name, fk.References)
				}
			}
			if fk.RefTable != entity.Table {
				dependencies[entity.Table][fk.RefTable] = struct{}{}
			}
		}
	}
	ordered := make([]Entity, 0, len(entities))
	done := map[string]struct{}{}
	for len(ordered) < len(entities) {
		candidate := ""
		for _, entity := range entities {
			if _, ok := done[entity.Table]; ok {
				continue
			}
			ready := true
			for dependency := range dependencies[entity.Table] {
				if _, ok := done[dependency]; !ok {
					ready = false
					break
				}
			}
			if ready && (candidate == "" || position[entity.Table] < position[candidate]) {
				candidate = entity.Table
			}
		}
		if candidate == "" {
			return nil, fmt.Errorf("cyclic foreign key dependencies")
		}
		ordered = append(ordered, byTable[candidate])
		done[candidate] = struct{}{}
	}
	return ordered, nil
}

func normalizeEntity(entity *Entity) error {
	entity.Name = strings.TrimSpace(entity.Name)
	entity.Table = strings.TrimSpace(entity.Table)
	if !typePattern.MatchString(entity.Name) {
		return fmt.Errorf("invalid entity name %q", entity.Name)
	}
	if !tablePattern.MatchString(entity.Table) {
		return fmt.Errorf("invalid table name %q", entity.Table)
	}
	if len(entity.Fields) == 0 {
		return fmt.Errorf("entity fields are required")
	}
	seenFields := map[string]struct{}{}
	for i := range entity.Fields {
		if err := normalizeField(&entity.Fields[i], seenFields); err != nil {
			return fmt.Errorf("field %d: %w", i+1, err)
		}
	}
	for i := range entity.Indexes {
		idx := &entity.Indexes[i]
		if !tablePattern.MatchString(idx.Name) || len(idx.Fields) == 0 {
			return fmt.Errorf("invalid index %q", idx.Name)
		}
		for _, field := range idx.Fields {
			if _, ok := seenFields[field]; !ok {
				return fmt.Errorf("index %q references unknown field %q", idx.Name, field)
			}
		}
	}
	for i := range entity.ForeignKeys {
		fk := &entity.ForeignKeys[i]
		if _, ok := seenFields[fk.Field]; !ok {
			return fmt.Errorf("foreign key references unknown field %q", fk.Field)
		}
		parts := strings.Split(fk.References, ".")
		if len(parts) != 2 || !tablePattern.MatchString(parts[0]) || !tablePattern.MatchString(parts[1]) {
			return fmt.Errorf("invalid foreign key reference %q", fk.References)
		}
		fk.RefTable, fk.RefField = parts[0], parts[1]
		fk.OnDelete = strings.ToUpper(strings.TrimSpace(fk.OnDelete))
		if fk.OnDelete == "" {
			fk.OnDelete = "CASCADE"
		}
		if fk.OnDelete != "CASCADE" && fk.OnDelete != "RESTRICT" && fk.OnDelete != "SET NULL" {
			return fmt.Errorf("invalid on_delete %q", fk.OnDelete)
		}
	}
	return nil
}

func (s *Schema) normalizeOperations() error {
	allowedCRUD := map[string]bool{"create": true, "get": true, "list": true, "update": true, "delete": true}
	seenCRUD := map[string]struct{}{}
	for i := range s.CRUD {
		s.CRUD[i] = strings.ToLower(strings.TrimSpace(s.CRUD[i]))
		if !allowedCRUD[s.CRUD[i]] {
			return fmt.Errorf("unsupported CRUD operation %q", s.CRUD[i])
		}
		if _, exists := seenCRUD[s.CRUD[i]]; exists {
			return fmt.Errorf("duplicate CRUD operation %q", s.CRUD[i])
		}
		seenCRUD[s.CRUD[i]] = struct{}{}
	}
	sort.Strings(s.CRUD)
	seenPermissions := map[string]struct{}{}
	for i := range s.Permissions {
		permission := &s.Permissions[i]
		permission.Name = strings.TrimSpace(permission.Name)
		permission.Path = strings.TrimSpace(permission.Path)
		if !permissionID.MatchString(permission.Name) {
			return fmt.Errorf("invalid permission name %q", permission.Name)
		}
		if _, exists := seenPermissions[permission.Name]; exists {
			return fmt.Errorf("duplicate permission %q", permission.Name)
		}
		seenPermissions[permission.Name] = struct{}{}
		if !strings.HasPrefix(permission.Path, "/api/v1/") || strings.Contains(permission.Path, "..") {
			return fmt.Errorf("invalid permission path %q", permission.Path)
		}
		if len(permission.Methods) == 0 {
			return fmt.Errorf("permission %q has no methods", permission.Name)
		}
		methodSeen := map[string]struct{}{}
		for j := range permission.Methods {
			permission.Methods[j] = strings.ToUpper(strings.TrimSpace(permission.Methods[j]))
			if !validMethod(permission.Methods[j]) {
				return fmt.Errorf("permission %q has invalid method %q", permission.Name, permission.Methods[j])
			}
			if _, exists := methodSeen[permission.Methods[j]]; exists {
				return fmt.Errorf("permission %q repeats method %q", permission.Name, permission.Methods[j])
			}
			methodSeen[permission.Methods[j]] = struct{}{}
		}
		sort.Strings(permission.Methods)
	}
	sort.Slice(s.Permissions, func(i, j int) bool { return s.Permissions[i].Name < s.Permissions[j].Name })
	return nil
}

func normalizeField(field *Field, seen map[string]struct{}) error {
	field.Name = strings.TrimSpace(field.Name)
	if !tablePattern.MatchString(field.Name) || field.Name == "id" || field.Name == "created_at" || field.Name == "updated_at" {
		return fmt.Errorf("invalid or reserved name %q", field.Name)
	}
	if _, exists := seen[field.Name]; exists {
		return fmt.Errorf("duplicate name %q", field.Name)
	}
	seen[field.Name] = struct{}{}
	field.Type = strings.ToLower(strings.TrimSpace(field.Type))
	field.GoType, field.SQLType = fieldTypes(field.Type, field.Size)
	if field.GoType == "" {
		return fmt.Errorf("unsupported type %q", field.Type)
	}
	if field.Size < 0 || field.Size > 65535 {
		return fmt.Errorf("invalid size %d", field.Size)
	}
	field.GoName = exportedName(field.Name)
	if !field.Required {
		field.GoType = "*" + field.GoType
	}
	field.JSONName = field.Name
	tags := []string{"column:" + field.Name}
	if field.Required {
		tags = append(tags, "not null")
	}
	if field.Unique {
		tags = append(tags, "uniqueIndex")
	} else if field.Index {
		tags = append(tags, "index")
	}
	field.GORMTag = strings.Join(tags, ";")
	return nil
}

func fieldTypes(kind string, size int) (string, string) {
	switch kind {
	case "string":
		if size == 0 {
			size = 255
		}
		return "string", fmt.Sprintf("VARCHAR(%d)", size)
	case "text":
		return "string", "LONGTEXT"
	case "int64":
		return "int64", "BIGINT"
	case "uint64":
		return "uint64", "BIGINT UNSIGNED"
	case "bool":
		return "bool", "BOOLEAN"
	case "datetime":
		return "time.Time", "DATETIME(3)"
	case "json":
		return "[]byte", "JSON"
	default:
		return "", ""
	}
}

func exportedName(value string) string {
	var out strings.Builder
	upper := true
	for _, r := range value {
		if r == '_' {
			upper = true
			continue
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		out.WriteRune(r)
	}
	return out.String()
}

func validMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}
