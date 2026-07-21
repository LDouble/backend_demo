package generator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validSchema() Schema {
	return Schema{
		Version: 1,
		Module:  "activity",
		Entity: Entity{Name: "Activity", Table: "activities", Fields: []Field{
			{Name: "title", Type: "string", Size: 200, Required: true, Index: true},
			{Name: "start_time", Type: "datetime", Required: true},
		}},
		CRUD: []string{"update", "create", "get", "list", "delete"},
		Permissions: []Permission{
			{Name: "activity:list", Path: "/api/v1/activities", Methods: []string{"get"}},
		},
	}
}

func TestSchemaNormalize(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Schema)
		want   string
	}{
		{name: "valid"},
		{name: "version", mutate: func(s *Schema) { s.Version = 3 }, want: "unsupported schema version"},
		{name: "path traversal module", mutate: func(s *Schema) { s.Module = "../activity" }, want: "invalid module name"},
		{name: "duplicate field", mutate: func(s *Schema) { s.Entity.Fields = append(s.Entity.Fields, s.Entity.Fields[0]) }, want: "duplicate name"},
		{name: "reserved field", mutate: func(s *Schema) { s.Entity.Fields[0].Name = "id" }, want: "reserved"},
		{name: "unsupported field type", mutate: func(s *Schema) { s.Entity.Fields[0].Type = "money" }, want: "unsupported type"},
		{name: "duplicate CRUD", mutate: func(s *Schema) { s.CRUD = append(s.CRUD, "create") }, want: "duplicate CRUD"},
		{name: "invalid permission", mutate: func(s *Schema) { s.Permissions[0].Name = "Activity.List" }, want: "invalid permission"},
		{name: "invalid method", mutate: func(s *Schema) { s.Permissions[0].Methods = []string{"TRACE"} }, want: "invalid method"},
		{name: "invalid entity", mutate: func(s *Schema) { s.Entity.Name = "activity" }, want: "invalid entity"},
		{name: "invalid table", mutate: func(s *Schema) { s.Entity.Table = "Activities" }, want: "invalid table"},
		{name: "missing fields", mutate: func(s *Schema) { s.Entity.Fields = nil }, want: "fields are required"},
		{name: "invalid size", mutate: func(s *Schema) { s.Entity.Fields[0].Size = 70000 }, want: "invalid size"},
		{name: "unsupported CRUD", mutate: func(s *Schema) { s.CRUD[0] = "publish" }, want: "unsupported CRUD"},
		{name: "duplicate permission", mutate: func(s *Schema) { s.Permissions = append(s.Permissions, s.Permissions[0]) }, want: "duplicate permission"},
		{name: "invalid permission path", mutate: func(s *Schema) { s.Permissions[0].Path = "/admin/notices" }, want: "invalid permission path"},
		{name: "missing permission methods", mutate: func(s *Schema) { s.Permissions[0].Methods = nil }, want: "has no methods"},
		{name: "duplicate permission method", mutate: func(s *Schema) { s.Permissions[0].Methods = []string{"GET", "get"} }, want: "repeats method"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema := validSchema()
			if test.mutate != nil {
				test.mutate(&schema)
			}
			err := schema.Normalize()
			if test.want == "" {
				if err != nil {
					t.Fatalf("Normalize() error = %v", err)
				}
				if schema.Entity.Fields[0].GoName != "Title" || schema.Permissions[0].Methods[0] != "GET" {
					t.Fatalf("schema was not normalized: %#v", schema)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Normalize() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestFieldTypes(t *testing.T) {
	tests := []struct {
		kind    string
		size    int
		wantGo  string
		wantSQL string
	}{
		{kind: "string", wantGo: "string", wantSQL: "VARCHAR(255)"},
		{kind: "text", wantGo: "string", wantSQL: "LONGTEXT"},
		{kind: "int64", wantGo: "int64", wantSQL: "BIGINT"},
		{kind: "uint64", wantGo: "uint64", wantSQL: "BIGINT UNSIGNED"},
		{kind: "bool", wantGo: "bool", wantSQL: "BOOLEAN"},
		{kind: "datetime", wantGo: "time.Time", wantSQL: "DATETIME(3)"},
		{kind: "json", wantGo: "[]byte", wantSQL: "JSON"},
		{kind: "unknown"},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			goType, sqlType := fieldTypes(test.kind, test.size)
			if goType != test.wantGo || sqlType != test.wantSQL {
				t.Fatalf("fieldTypes(%q) = %q, %q", test.kind, goType, sqlType)
			}
		})
	}
}

func TestSchemaV2DependencyValidationAndNullableTypes(t *testing.T) {
	schema := Schema{Version: 2, Module: "notice", Entities: []Entity{
		{Name: "Child", Table: "children", Fields: []Field{{Name: "parent_id", Type: "uint64", Required: true}, {Name: "read_at", Type: "datetime"}}, ForeignKeys: []ForeignKey{{Field: "parent_id", References: "parents.id"}}},
		{Name: "Parent", Table: "parents", Primary: true, Fields: []Field{{Name: "name", Type: "string", Required: true}}},
	}}
	if err := schema.Normalize(); err != nil {
		t.Fatal(err)
	}
	if schema.Entities[0].Table != "parents" || schema.Entities[1].Fields[1].GoType != "*time.Time" {
		t.Fatalf("normalized v2 schema=%+v", schema.Entities)
	}
	schema.Entities[1].ForeignKeys = []ForeignKey{{Field: "parent_id", References: "missing.id"}}
	if err := schema.Normalize(); err == nil || !strings.Contains(err.Error(), "undeclared table") {
		t.Fatalf("unknown dependency error=%v", err)
	}
}

func TestLoadStrictAndContext(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "schema.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nmodule: activity\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(context.Background(), path); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("Load() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Load(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(cancelled) error = %v", err)
	}
}
