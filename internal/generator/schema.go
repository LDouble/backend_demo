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
	packagePattern      = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)
	tablePattern        = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	typePattern         = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{1,63}$`)
	permissionID        = regexp.MustCompile(`^[a-z][a-z0-9_]*:[a-z][a-z0-9_]*$`)
	apiLiteralSegment   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	apiPathParameter    = regexp.MustCompile(`^\{([a-z][a-z0-9_]{1,63})\}$`)
	casbinPathParameter = regexp.MustCompile(`^:[a-z][a-z0-9_]{1,63}$`)
	apiHeaderName       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)
)

// Schema is the versioned declaration used to generate one business module.
type Schema struct {
	Version     int                       `yaml:"version"`
	Module      string                    `yaml:"module"`
	Entity      Entity                    `yaml:"entity"`
	Entities    []Entity                  `yaml:"entities"`
	CRUD        []string                  `yaml:"crud"`
	Permissions []Permission              `yaml:"permissions"`
	Operations  []APIOperation            `yaml:"operations,omitempty"`
	Components  map[string]map[string]any `yaml:"components,omitempty"`
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
	Name       string `yaml:"name"`
	RenameFrom string `yaml:"rename_from,omitempty"`
	Type       string `yaml:"type"`
	Required   bool   `yaml:"required"`
	Size       int    `yaml:"size,omitempty"`
	Unique     bool   `yaml:"unique,omitempty"`
	Index      bool   `yaml:"index,omitempty"`

	GoName   string `yaml:"-"`
	GoType   string `yaml:"-"`
	SQLType  string `yaml:"-"`
	JSONName string `yaml:"-"`
	GORMTag  string `yaml:"-"`
}

// Permission describes a stable Casbin permission emitted for a module API.
type Permission struct {
	Name         string   `yaml:"name" json:"name"`
	Path         string   `yaml:"path" json:"path_pattern"`
	Methods      []string `yaml:"methods" json:"methods"`
	DefaultRoles []string `yaml:"default_roles,omitempty" json:"default_roles,omitempty"`
}

// APIOperation declares one HTTP capability and its entry permission.
type APIOperation struct {
	OperationID          string         `yaml:"operation_id"`
	Method               string         `yaml:"method"`
	Path                 string         `yaml:"path"`
	Permission           string         `yaml:"permission"`
	DefaultRoles         []string       `yaml:"default_roles,omitempty"`
	Idempotency          string         `yaml:"idempotency,omitempty"`
	AcademicVerification string         `yaml:"academic_verification,omitempty"`
	RequestContent       string         `yaml:"request_content,omitempty"`
	Summary              string         `yaml:"summary,omitempty"`
	Headers              []APIParameter `yaml:"headers,omitempty"`
	Query                []APIParameter `yaml:"query,omitempty"`
	Body                 *APIObject     `yaml:"body,omitempty"`
	Responses            []APIResponse  `yaml:"responses,omitempty"`
}

// APIParameter declares a generated header or query parameter.
type APIParameter struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Required  bool     `yaml:"required,omitempty"`
	Format    string   `yaml:"format,omitempty"`
	Enum      []string `yaml:"enum,omitempty"`
	Minimum   *int64   `yaml:"minimum,omitempty"`
	Maximum   *int64   `yaml:"maximum,omitempty"`
	MaxLength *int64   `yaml:"max_length,omitempty"`
}

// APIObject declares an inline JSON request object.
type APIObject struct {
	Ref    string     `yaml:"ref,omitempty"`
	Fields []APIField `yaml:"fields"`
}

// APIField declares one JSON request field.
type APIField struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Required  bool     `yaml:"required,omitempty"`
	Format    string   `yaml:"format,omitempty"`
	Items     string   `yaml:"items,omitempty"`
	Enum      []string `yaml:"enum,omitempty"`
	Minimum   *int64   `yaml:"minimum,omitempty"`
	Maximum   *int64   `yaml:"maximum,omitempty"`
	MaxLength *int64   `yaml:"max_length,omitempty"`
}

// APIResponse declares one operation response status and shared envelope kind.
type APIResponse struct {
	Status int    `yaml:"status"`
	Kind   string `yaml:"kind"`
	Ref    string `yaml:"ref,omitempty"`
}

// Load reads, strictly decodes, normalizes and validates one schema.
func Load(ctx context.Context, path string) (Schema, error) {
	if err := ctx.Err(); err != nil {
		return Schema{}, err
	}
	// #nosec G304 -- callers resolve this explicit schema input; all generated outputs remain repository-scoped.
	data, err := os.ReadFile(path)
	if err != nil {
		return Schema{}, fmt.Errorf("read module schema: %w", err)
	}
	return decodeSchema(ctx, data)
}

func loadRepositorySchema(ctx context.Context, root, relative string) (Schema, error) {
	if err := ctx.Err(); err != nil {
		return Schema{}, err
	}
	data, err := repositoryReadFile(root, relative)
	if err != nil {
		return Schema{}, fmt.Errorf("read module schema: %w", err)
	}
	return decodeSchema(ctx, data)
}

// LoadRepository reads a schema through a symlink-safe repository file boundary.
func LoadRepository(ctx context.Context, root, relative string) (Schema, error) {
	return loadRepositorySchema(ctx, root, relative)
}

func decodeSchema(ctx context.Context, data []byte) (Schema, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var schema Schema
	if err := decoder.Decode(&schema); err != nil {
		return Schema{}, fmt.Errorf("decode module schema: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Schema{}, err
	}
	if err := schema.Normalize(); err != nil {
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
	if err := s.validateComponents(); err != nil {
		return err
	}
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
		if !validPermissionPath(permission.Path) {
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
		op.DefaultRoles = uniqueStrings(op.DefaultRoles)
		op.Idempotency = strings.ToLower(strings.TrimSpace(op.Idempotency))
		op.AcademicVerification = strings.ToLower(strings.TrimSpace(op.AcademicVerification))
		op.RequestContent = strings.ToLower(strings.TrimSpace(op.RequestContent))
		if !typePattern.MatchString(op.OperationID) {
			return fmt.Errorf("invalid operation_id %q", op.OperationID)
		}
		if !validMethod(op.Method) {
			return fmt.Errorf("operation %q has invalid method %q", op.OperationID, op.Method)
		}
		if !validOperationPath(op.Path) {
			return fmt.Errorf("operation %q has invalid path %q", op.OperationID, op.Path)
		}
		if !permissionID.MatchString(op.Permission) {
			return fmt.Errorf("operation %q has invalid permission %q", op.OperationID, op.Permission)
		}
		for _, role := range op.DefaultRoles {
			if !packagePattern.MatchString(role) {
				return fmt.Errorf("operation %q has invalid default role %q", op.OperationID, role)
			}
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
		switch {
		case isWriteMethod(op.Method):
			if err := normalizeIdempotency(op); err != nil {
				return err
			}
			if op.AcademicVerification == "" {
				op.AcademicVerification = "none"
			}
			if op.AcademicVerification != "required" && op.AcademicVerification != "none" {
				return fmt.Errorf("operation %q has invalid academic_verification %q", op.OperationID, op.AcademicVerification)
			}
		case op.Idempotency != "":
			return fmt.Errorf("read operation %q must not declare idempotency", op.OperationID)
		case op.AcademicVerification != "":
			return fmt.Errorf("read operation %q must not declare academic_verification", op.OperationID)
		}
		if op.RequestContent == "" {
			op.RequestContent = "application/json"
		}
		if op.RequestContent != "application/json" && op.RequestContent != "multipart/form-data" {
			return fmt.Errorf("operation %q has invalid request_content %q", op.OperationID, op.RequestContent)
		}
		if op.RequestContent == "multipart/form-data" && op.Body == nil {
			return fmt.Errorf("operation %q multipart request requires a body", op.OperationID)
		}
		seenHeaders := map[string]struct{}{}
		for j := range op.Headers {
			if err := normalizeAPIParameter(&op.Headers[j], "header"); err != nil {
				return fmt.Errorf("operation %q header: %w", op.OperationID, err)
			}
			name := strings.ToLower(op.Headers[j].Name)
			if _, exists := seenHeaders[name]; exists {
				return fmt.Errorf("operation %q repeats header %q", op.OperationID, op.Headers[j].Name)
			}
			seenHeaders[name] = struct{}{}
		}
		seenQuery := map[string]struct{}{}
		for j := range op.Query {
			if err := normalizeAPIParameter(&op.Query[j], "query"); err != nil {
				return fmt.Errorf("operation %q query: %w", op.OperationID, err)
			}
			if _, exists := seenQuery[op.Query[j].Name]; exists {
				return fmt.Errorf("operation %q repeats query parameter %q", op.OperationID, op.Query[j].Name)
			}
			seenQuery[op.Query[j].Name] = struct{}{}
		}
		if op.Body != nil {
			if op.Body.Ref != "" && len(op.Body.Fields) > 0 {
				return fmt.Errorf("operation %q body cannot declare both ref and fields", op.OperationID)
			}
			if op.Body.Ref != "" && !typePattern.MatchString(op.Body.Ref) {
				return fmt.Errorf("operation %q body has invalid ref %q", op.OperationID, op.Body.Ref)
			}
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
			if response.Ref != "" && !typePattern.MatchString(response.Ref) {
				return fmt.Errorf("operation %q has invalid response ref %q", op.OperationID, response.Ref)
			}
			if response.Ref != "" && len(s.Components["responses"]) > 0 {
				if _, ok := s.Components["responses"][response.Ref]; !ok {
					return fmt.Errorf(
						"operation %q references undefined local response %q",
						op.OperationID,
						response.Ref,
					)
				}
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

func (s *Schema) validateComponents() error {
	for group, definitions := range s.Components {
		if group != "schemas" && group != "responses" {
			return fmt.Errorf("unsupported OpenAPI component group %q", group)
		}
		for name := range definitions {
			if !typePattern.MatchString(name) {
				return fmt.Errorf("invalid OpenAPI component %s.%s", group, name)
			}
		}
	}
	return nil
}

func normalizeAPIParameter(parameter *APIParameter, location string) error {
	parameter.Name = strings.TrimSpace(parameter.Name)
	parameter.Type = strings.ToLower(strings.TrimSpace(parameter.Type))
	parameter.Format = strings.ToLower(strings.TrimSpace(parameter.Format))
	if !validAPIType(parameter.Type) {
		return fmt.Errorf("invalid parameter %q", parameter.Name)
	}
	switch location {
	case "header":
		if !apiHeaderName.MatchString(parameter.Name) {
			return fmt.Errorf("invalid parameter %q", parameter.Name)
		}
	case "query":
		if !tablePattern.MatchString(parameter.Name) {
			return fmt.Errorf("invalid parameter %q", parameter.Name)
		}
	default:
		return fmt.Errorf("invalid parameter location %q", location)
	}
	if !validAPIFormat(parameter.Type, parameter.Format) {
		return fmt.Errorf("parameter %q has invalid format %q", parameter.Name, parameter.Format)
	}
	if len(parameter.Enum) > 0 {
		if parameter.Type != "string" {
			return fmt.Errorf("parameter %q has enum values for non-string type", parameter.Name)
		}
		seen := make(map[string]struct{}, len(parameter.Enum))
		for i, value := range parameter.Enum {
			value = strings.TrimSpace(value)
			if value == "" {
				return fmt.Errorf("parameter %q has an empty enum value", parameter.Name)
			}
			if _, exists := seen[value]; exists {
				return fmt.Errorf("parameter %q has duplicate enum value %q", parameter.Name, value)
			}
			seen[value] = struct{}{}
			parameter.Enum[i] = value
		}
	}
	if (parameter.Minimum != nil || parameter.Maximum != nil) && parameter.Type != "integer" {
		return fmt.Errorf("parameter %q has numeric bounds for non-integer type", parameter.Name)
	}
	if parameter.Minimum != nil && parameter.Maximum != nil && *parameter.Minimum > *parameter.Maximum {
		return fmt.Errorf("parameter %q has minimum greater than maximum", parameter.Name)
	}
	if parameter.MaxLength != nil && (*parameter.MaxLength < 1 || parameter.Type != "string") {
		return fmt.Errorf("parameter %q has invalid max_length", parameter.Name)
	}
	return nil
}

func isWriteMethod(method string) bool {
	switch method {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

func normalizeIdempotency(operation *APIOperation) error {
	switch operation.Idempotency {
	case "required":
		const maxKeyLength int64 = 128
		for i := range operation.Headers {
			header := &operation.Headers[i]
			if !strings.EqualFold(header.Name, "Idempotency-Key") {
				continue
			}
			if !header.Required || strings.ToLower(strings.TrimSpace(header.Type)) != "string" {
				return fmt.Errorf("operation %q Idempotency-Key must be a required string", operation.OperationID)
			}
			header.Name = "Idempotency-Key"
			header.MaxLength = pointer(maxKeyLength)
			return nil
		}
		operation.Headers = append(operation.Headers, APIParameter{
			Name:      "Idempotency-Key",
			Type:      "string",
			Required:  true,
			MaxLength: pointer(maxKeyLength),
		})
		return nil
	case "inherent", "none":
		if hasIdempotencyHeader(operation.Headers) {
			return fmt.Errorf("operation %q with idempotency %q must not require Idempotency-Key", operation.OperationID, operation.Idempotency)
		}
		return nil
	case "":
		return fmt.Errorf("write operation %q must declare idempotency: required|inherent|none", operation.OperationID)
	default:
		return fmt.Errorf("operation %q has invalid idempotency %q", operation.OperationID, operation.Idempotency)
	}
}

func pointer[T any](value T) *T { return &value }

func normalizeAPIField(field *APIField) error {
	field.Name = strings.TrimSpace(field.Name)
	field.Type = strings.ToLower(strings.TrimSpace(field.Type))
	field.Format = strings.ToLower(strings.TrimSpace(field.Format))
	field.Items = strings.ToLower(strings.TrimSpace(field.Items))
	if !tablePattern.MatchString(field.Name) || !validAPIType(field.Type) {
		return fmt.Errorf("invalid field %q", field.Name)
	}
	if !validAPIFormat(field.Type, field.Format) {
		return fmt.Errorf("field %q has invalid format %q", field.Name, field.Format)
	}
	if len(field.Enum) > 0 {
		if field.Type != "string" {
			return fmt.Errorf("field %q has enum values for non-string type", field.Name)
		}
		seen := make(map[string]struct{}, len(field.Enum))
		for i, value := range field.Enum {
			value = strings.TrimSpace(value)
			if value == "" {
				return fmt.Errorf("field %q has an empty enum value", field.Name)
			}
			if _, exists := seen[value]; exists {
				return fmt.Errorf("field %q has duplicate enum value %q", field.Name, value)
			}
			seen[value] = struct{}{}
			field.Enum[i] = value
		}
	}
	if field.Type == "array" && (field.Items == "array" || !validAPIType(field.Items)) {
		return fmt.Errorf("array field %q has invalid items", field.Name)
	}
	if field.Type != "array" && field.Items != "" {
		return fmt.Errorf("non-array field %q must not declare items", field.Name)
	}
	if (field.Minimum != nil || field.Maximum != nil) && field.Type != "integer" {
		return fmt.Errorf("field %q has numeric bounds for non-integer type", field.Name)
	}
	if field.Minimum != nil && field.Maximum != nil && *field.Minimum > *field.Maximum {
		return fmt.Errorf("field %q has minimum greater than maximum", field.Name)
	}
	if field.MaxLength != nil && (*field.MaxLength < 1 || field.Type != "string") {
		return fmt.Errorf("field %q has invalid max_length", field.Name)
	}
	return nil
}

func validOperationPath(path string) bool {
	return validAPIPath(path, func(segment string) bool {
		return apiLiteralSegment.MatchString(segment) || apiPathParameter.MatchString(segment)
	})
}

func validPermissionPath(path string) bool {
	return validAPIPath(path, func(segment string) bool {
		return apiLiteralSegment.MatchString(segment) || casbinPathParameter.MatchString(segment)
	})
}

func validAPIPath(path string, validSegment func(string) bool) bool {
	if !strings.HasPrefix(path, "/api/v1/") || strings.HasSuffix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if !validSegment(segment) {
			return false
		}
	}
	return true
}

func validAPIFormat(kind, format string) bool {
	switch kind {
	case "string":
		return format == "" || format == "date" || format == "date-time" || format == "binary"
	case "integer":
		return format == "" || format == "int32" || format == "int64" || format == "uint64"
	case "boolean", "array":
		return format == ""
	default:
		return false
	}
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
		permissionPath := openAPIPathToCasbin(operation.Path)
		roles := uniqueStrings(operation.DefaultRoles)
		key := operation.Permission + "\x00" + permissionPath + "\x00" + strings.Join(roles, "\x00")
		permission, ok := byKey[key]
		if !ok {
			permission = &Permission{
				Name:         operation.Permission,
				Path:         permissionPath,
				Methods:      []string{},
				DefaultRoles: roles,
			}
			byKey[key] = permission
		}
		permission.Methods = append(permission.Methods, operation.Method)
	}
	result := make([]Permission, 0, len(byKey))
	for _, permission := range byKey {
		sort.Strings(permission.Methods)
		permission.DefaultRoles = uniqueStrings(permission.DefaultRoles)
		result = append(result, *permission)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			if result[i].Path == result[j].Path {
				return strings.Join(result[i].DefaultRoles, "\x00") < strings.Join(result[j].DefaultRoles, "\x00")
			}
			return result[i].Path < result[j].Path
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func openAPIPathToCasbin(path string) string {
	return regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_]*)\}`).ReplaceAllString(path, `:$1`)
}

func normalizeField(field *Field, seen map[string]struct{}) error {
	field.Name = strings.TrimSpace(field.Name)
	field.RenameFrom = strings.TrimSpace(field.RenameFrom)
	if !tablePattern.MatchString(field.Name) || field.Name == "id" || field.Name == "created_at" || field.Name == "updated_at" {
		return fmt.Errorf("invalid or reserved name %q", field.Name)
	}
	if _, exists := seen[field.Name]; exists {
		return fmt.Errorf("duplicate name %q", field.Name)
	}
	seen[field.Name] = struct{}{}
	if field.RenameFrom != "" && !tablePattern.MatchString(field.RenameFrom) {
		return fmt.Errorf("invalid rename_from %q", field.RenameFrom)
	}
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
