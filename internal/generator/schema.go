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

// SchemaVersion is the only module schema and registry version currently supported.
const SchemaVersion = 1

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
	CRUD        []string     `yaml:"crud"`
	Permissions []Permission `yaml:"permissions"`
}

// Entity describes the module's primary persistent entity.
type Entity struct {
	Name   string  `yaml:"name"`
	Table  string  `yaml:"table"`
	Fields []Field `yaml:"fields"`
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
	if s.Version != SchemaVersion {
		return fmt.Errorf("unsupported schema version %d", s.Version)
	}
	s.Module = strings.TrimSpace(s.Module)
	if !packagePattern.MatchString(s.Module) || strings.Contains(s.Module, "..") {
		return fmt.Errorf("invalid module name %q", s.Module)
	}
	s.Entity.Name = strings.TrimSpace(s.Entity.Name)
	s.Entity.Table = strings.TrimSpace(s.Entity.Table)
	if !typePattern.MatchString(s.Entity.Name) {
		return fmt.Errorf("invalid entity name %q", s.Entity.Name)
	}
	if !tablePattern.MatchString(s.Entity.Table) {
		return fmt.Errorf("invalid table name %q", s.Entity.Table)
	}
	if len(s.Entity.Fields) == 0 {
		return fmt.Errorf("entity fields are required")
	}
	seenFields := map[string]struct{}{}
	for i := range s.Entity.Fields {
		if err := normalizeField(&s.Entity.Fields[i], seenFields); err != nil {
			return fmt.Errorf("field %d: %w", i+1, err)
		}
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
