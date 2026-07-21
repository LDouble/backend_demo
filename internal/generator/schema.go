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
	Version     int            `yaml:"version"`
	Module      string         `yaml:"module"`
	Entity      Entity         `yaml:"entity"`
	Entities    []Entity       `yaml:"entities"`
	CRUD        []string       `yaml:"crud"`
	Permissions []Permission   `yaml:"permissions"`
	Operations  []APIOperation `yaml:"operations,omitempty"`
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

// APIOperation declares one HTTP capability and its entry permission.
type APIOperation struct {
	OperationID string         `yaml:"operation_id"`
	Method      string         `yaml:"method"`
	Path        string         `yaml:"path"`
	Permission  string         `yaml:"permission"`
	Summary     string         `yaml:"summary,omitempty"`
	Headers     []APIParameter `yaml:"headers,omitempty"`
	Query       []APIParameter `yaml:"query,omitempty"`
	Body        *APIObject     `yaml:"body,omitempty"`
	Responses   []APIResponse  `yaml:"responses,omitempty"`
}

// APIParameter declares a generated header or query parameter.
type APIParameter struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Required bool   `yaml:"required,omitempty"`
	Format   string `yaml:"format,omitempty"`
	Minimum  *int64 `yaml:"minimum,omitempty"`
	Maximum  *int64 `yaml:"maximum,omitempty"`
}

// APIObject declares an inline JSON request object.
type APIObject struct {
	Fields []APIField `yaml:"fields"`
}

// APIField declares one JSON request field.
type APIField struct {
	Name     string `yaml:"name"`
	Type     string `yaml:"type"`
	Required bool   `yaml:"required,omitempty"`
	Format   string `yaml:"format,omitempty"`
	Items    string `yaml:"items,omitempty"`
	Minimum  *int64 `yaml:"minimum,omitempty"`
	Maximum  *int64 `yaml:"maximum,omitempty"`
}

// APIResponse declares one operation response status and shared envelope kind.
type APIResponse struct {
	Status int    `yaml:"status"`
	Kind   string `yaml:"kind"`
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
	if err := s.normalizeAPIOperations(); err != nil {
		return err
	}
	return nil
}

func (s *Schema) normalizeAPIOperations() error {
	seenIDs := map[string]struct{}{}
	seenRoutes := map[string]struct{}{}
	for i := range s.Operations {
		op := &s.Operations[i]
		op.OperationID = strings.TrimSpace(op.OperationID)
		op.Method = strings.ToUpper(strings.TrimSpace(op.Method))
		op.Path = strings.TrimSpace(op.Path)
		op.Permission = strings.TrimSpace(op.Permission)
		if !typePattern.MatchString(op.OperationID) {
			return fmt.Errorf("invalid operation_id %q", op.OperationID)
		}
		if !validMethod(op.Method) {
			return fmt.Errorf("operation %q has invalid method %q", op.OperationID, op.Method)
		}
		if !strings.HasPrefix(op.Path, "/api/v1/") || strings.Contains(op.Path, "..") {
			return fmt.Errorf("operation %q has invalid path %q", op.OperationID, op.Path)
		}
		if !permissionID.MatchString(op.Permission) {
			return fmt.Errorf("operation %q has invalid permission %q", op.OperationID, op.Permission)
		}
		if _, exists := seenIDs[op.OperationID]; exists {
			return fmt.Errorf("duplicate operation_id %q", op.OperationID)
		}
		seenIDs[op.OperationID] = struct{}{}
		routeKey := op.Method + " " + op.Path
		if _, exists := seenRoutes[routeKey]; exists {
			return fmt.Errorf("duplicate operation route %q", routeKey)
		}
		seenRoutes[routeKey] = struct{}{}
		// Constraint: every state-changing operation (POST / PATCH / DELETE)
		// MUST declare an `Idempotency-Key` header with `required: true`. This
		// makes retry-safe semantics a first-class contract and stops the
		// review-finding "handler advertises idempotency, store ignores it"
		// pattern from sneaking back in. read-side endpoints (GET) are exempt.
		if op.Method == "POST" || op.Method == "PATCH" || op.Method == "DELETE" || op.Method == "PUT" {
			if !hasIdempotencyHeader(op.Headers) {
				return fmt.Errorf("operation %q (%s %s) must declare Idempotency-Key header (required: true); see platformwide idempotency contract", op.OperationID, op.Method, op.Path)
			}
		}
		for j := range op.Headers {
			if err := normalizeAPIParameter(&op.Headers[j]); err != nil {
				return fmt.Errorf("operation %q header: %w", op.OperationID, err)
			}
		}
		for j := range op.Query {
			if err := normalizeAPIParameter(&op.Query[j]); err != nil {
				return fmt.Errorf("operation %q query: %w", op.OperationID, err)
			}
		}
		if op.Body != nil {
			for j := range op.Body.Fields {
				if err := normalizeAPIField(&op.Body.Fields[j]); err != nil {
					return fmt.Errorf("operation %q body: %w", op.OperationID, err)
				}
			}
		}
		if len(op.Responses) == 0 {
			op.Responses = []APIResponse{{Status: 200, Kind: "success"}}
		}
		for _, response := range op.Responses {
			if response.Status < 100 || response.Status > 599 || (response.Kind != "success" && response.Kind != "error") {
				return fmt.Errorf("operation %q has invalid response", op.OperationID)
			}
		}
	}
	sort.Slice(s.Operations, func(i, j int) bool {
		if s.Operations[i].Path == s.Operations[j].Path {
			return s.Operations[i].Method < s.Operations[j].Method
		}
		return s.Operations[i].Path < s.Operations[j].Path
	})
	if len(s.Operations) > 0 {
		s.Permissions = permissionsFromOperations(s.Operations)
	}
	return nil
}

func normalizeAPIParameter(parameter *APIParameter) error {
	parameter.Name = strings.TrimSpace(parameter.Name)
	parameter.Type = strings.ToLower(strings.TrimSpace(parameter.Type))
	if parameter.Name == "" || !validAPIType(parameter.Type) {
		return fmt.Errorf("invalid parameter %q", parameter.Name)
	}
	return nil
}

func normalizeAPIField(field *APIField) error {
	field.Name = strings.TrimSpace(field.Name)
	field.Type = strings.ToLower(strings.TrimSpace(field.Type))
	if !tablePattern.MatchString(field.Name) || !validAPIType(field.Type) {
		return fmt.Errorf("invalid field %q", field.Name)
	}
	if field.Type == "array" && !validAPIType(field.Items) {
		return fmt.Errorf("array field %q has invalid items", field.Name)
	}
	return nil
}

func validAPIType(value string) bool {
	switch value {
	case "string", "integer", "boolean", "array":
		return true
	default:
		return false
	}
}

func permissionsFromOperations(operations []APIOperation) []Permission {
	byKey := map[string]*Permission{}
	for _, operation := range operations {
		key := operation.Permission + "\x00" + operation.Path
		permission, ok := byKey[key]
		if !ok {
			permission = &Permission{Name: operation.Permission, Path: operation.Path, Methods: []string{}}
			byKey[key] = permission
		}
		permission.Methods = append(permission.Methods, operation.Method)
	}
	result := make([]Permission, 0, len(byKey))
	for _, permission := range byKey {
		sort.Strings(permission.Methods)
		result = append(result, *permission)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Path < result[j].Path
		}
		return result[i].Name < result[j].Name
	})
	return result
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

// hasIdempotencyHeader reports whether any of the supplied API parameters is
// the Idempotency-Key header with `required: true`. The header is required
// for every state-changing operation (POST / PATCH / PUT / DELETE) so retry-
// safety is enforced at the schema level rather than discovered via review.
func hasIdempotencyHeader(params []APIParameter) bool {
	for _, p := range params {
		if strings.EqualFold(p.Name, "Idempotency-Key") && p.Required {
			return true
		}
	}
	return false
}
