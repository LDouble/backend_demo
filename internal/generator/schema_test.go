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
		{name: "permission path YAML injection", mutate: func(s *Schema) { s.Permissions[0].Path = "/api/v1/activities\ninjected: true" }, want: "invalid permission path"},
		{name: "missing permission methods", mutate: func(s *Schema) { s.Permissions[0].Methods = nil }, want: "has no methods"},
		{name: "duplicate permission method", mutate: func(s *Schema) { s.Permissions[0].Methods = []string{"GET", "get"} }, want: "repeats method"},
		{
			name: "undefined local response component",
			mutate: func(s *Schema) {
				s.Permissions = nil
				s.Components = map[string]map[string]any{
					"responses": {"OtherResponse": map[string]any{"description": "other"}},
				}
				s.Operations = []APIOperation{{
					OperationID: "ListActivities",
					Method:      "GET",
					Path:        "/api/v1/activities",
					Permission:  "activity:list",
					Responses: []APIResponse{{
						Status: 200,
						Kind:   "success",
						Ref:    "ActivityResponse",
					}},
				}}
			},
			want: "undefined local response",
		},
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

func TestOperationsRejectUnsafeTemplateValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*APIOperation)
	}{
		{name: "path newline", mutate: func(op *APIOperation) { op.Path = "/api/v1/items\ninjected: true" }},
		{name: "path YAML metacharacter", mutate: func(op *APIOperation) { op.Path = "/api/v1/items#hidden" }},
		{name: "header newline", mutate: func(op *APIOperation) { op.Headers = []APIParameter{{Name: "X-Test\ninjected", Type: "string"}} }},
		{name: "query metacharacter", mutate: func(op *APIOperation) { op.Query = []APIParameter{{Name: "page: injected", Type: "integer"}} }},
		{name: "parameter format", mutate: func(op *APIOperation) {
			op.Query = []APIParameter{{Name: "start_date", Type: "string", Format: "date\ninjected"}}
		}},
		{name: "field format", mutate: func(op *APIOperation) {
			op.Body = &APIObject{Fields: []APIField{{Name: "start_at", Type: "string", Format: "date-time # injected"}}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema := validSchema()
			schema.Permissions = nil
			schema.Operations = []APIOperation{{
				OperationID: "ListItems",
				Method:      "GET",
				Path:        "/api/v1/items",
				Permission:  "activity:list",
			}}
			test.mutate(&schema.Operations[0])
			if err := schema.Normalize(); err == nil {
				t.Fatal("Normalize() accepted unsafe template value")
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

func TestOperationsDerivePermissions(t *testing.T) {
	minimum := int64(1)
	schema := validSchema()
	schema.Permissions = nil
	schema.Operations = []APIOperation{{
		OperationID:  "CreateActivityRegistration",
		Method:       "post",
		Idempotency:  "required",
		Path:         "/api/v1/activities/{id}/registrations",
		Permission:   "activity:register",
		DefaultRoles: []string{"member", "member"},
		Headers:      []APIParameter{{Name: "Idempotency-Key", Type: "string", Required: true}},
		Body:         &APIObject{Fields: []APIField{{Name: "expected_version", Type: "integer", Format: "uint64", Required: true, Minimum: &minimum}}},
	}}
	if err := schema.Normalize(); err != nil {
		t.Fatal(err)
	}
	if len(schema.Permissions) != 1 || schema.Permissions[0].Methods[0] != "POST" {
		t.Fatalf("derived permissions = %#v", schema.Permissions)
	}
	if len(schema.Permissions[0].DefaultRoles) != 1 || schema.Permissions[0].DefaultRoles[0] != "member" {
		t.Fatalf("derived default roles = %#v", schema.Permissions[0].DefaultRoles)
	}
}

func TestOperationsKeepDefaultRolesMethodSpecific(t *testing.T) {
	schema := validSchema()
	schema.Permissions = nil
	schema.Operations = []APIOperation{
		{
			OperationID: "ListActivities", Method: "GET", Path: "/api/v1/activities",
			Permission: "activity:manage", DefaultRoles: []string{"member"},
		},
		{
			OperationID: "CreateActivity", Method: "POST", Path: "/api/v1/activities",
			Permission: "activity:manage", Idempotency: "inherent",
		},
	}
	if err := schema.Normalize(); err != nil {
		t.Fatal(err)
	}
	if len(schema.Permissions) != 2 {
		t.Fatalf("derived permissions = %#v", schema.Permissions)
	}
	memberRule := schema.Permissions[1]
	memberHasRole := len(memberRule.DefaultRoles) == 1 && memberRule.DefaultRoles[0] == "member"
	memberHasMethod := len(memberRule.Methods) == 1 && memberRule.Methods[0] == "GET"
	if !memberHasRole || !memberHasMethod {
		t.Fatalf("member rule = %#v", memberRule)
	}
	adminRule := schema.Permissions[0]
	adminHasMethod := len(adminRule.Methods) == 1 && adminRule.Methods[0] == "POST"
	if len(adminRule.DefaultRoles) != 0 || !adminHasMethod {
		t.Fatalf("admin rule = %#v", adminRule)
	}
}

func TestOperationBodyStringMaxLength(t *testing.T) {
	maxLength := int64(500)
	schema := validSchema()
	schema.Operations = []APIOperation{{
		OperationID: "CreateActivity",
		Method:      "POST",
		Idempotency: "required",
		Path:        "/api/v1/activities",
		Permission:  "activity:create",
		Body: &APIObject{Fields: []APIField{{
			Name: "location", Type: "string", MaxLength: &maxLength,
		}}},
	}}
	if err := schema.Normalize(); err != nil {
		t.Fatal(err)
	}
	invalid := int64(0)
	schema.Operations[0].Body.Fields[0].MaxLength = &invalid
	if err := schema.Normalize(); err == nil || !strings.Contains(err.Error(), "max_length") {
		t.Fatalf("Normalize() error = %v", err)
	}
}

func TestOperationsRejectDuplicateRoute(t *testing.T) {
	schema := validSchema()
	schema.Operations = []APIOperation{
		{OperationID: "CreateOne", Method: "POST", Path: "/api/v1/items", Permission: "activity:create", Idempotency: "required"},
		{OperationID: "CreateTwo", Method: "post", Path: "/api/v1/items", Permission: "activity:create_two", Idempotency: "required"},
	}
	err := schema.Normalize()
	if err == nil || !strings.Contains(err.Error(), "duplicate operation route") {
		t.Fatalf("Normalize() error = %v", err)
	}
}

// TestOperationsRequireIdempotencyKey validates B.2: every state-changing
// operation (POST/PATCH/DELETE/PUT) must declare an Idempotency-Key header
// with `required: true`. GET endpoints remain exempt.
func TestOperationsRequireIdempotencyDeclaration(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		idempotency string
		headers     []APIParameter
		wantErr     bool
	}{
		{name: "post without declaration", method: "POST", wantErr: true},
		{name: "required derives header", method: "POST", idempotency: "required"},
		{name: "required rejects optional header", method: "POST", idempotency: "required", headers: []APIParameter{{Name: "Idempotency-Key", Type: "string"}}, wantErr: true},
		{name: "inherent", method: "PATCH", idempotency: "inherent"},
		{name: "none", method: "DELETE", idempotency: "none"},
		{name: "invalid", method: "PUT", idempotency: "sometimes", wantErr: true},
		{name: "get without declaration", method: "GET"},
		{name: "get with declaration", method: "GET", idempotency: "none", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := validSchema()
			s.Operations = []APIOperation{{
				OperationID: "Op", Method: tc.method, Path: "/api/v1/only", Permission: "x:op",
				Idempotency: tc.idempotency, Headers: tc.headers,
			}}
			err := s.Normalize()
			if tc.wantErr {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), "idempotency") {
					t.Fatalf("expected idempotency error, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.idempotency == "required" {
				header := s.Operations[0].Headers[0]
				if !header.Required || header.MaxLength == nil || *header.MaxLength != 128 {
					t.Fatalf("derived header = %#v", header)
				}
			}
		})
	}
}

func TestOperationAcademicVerificationAndMultipart(t *testing.T) {
	schema := validSchema()
	schema.Operations = []APIOperation{{
		OperationID:          "UploadMaterial",
		Method:               "POST",
		Path:                 "/api/v1/materials",
		Permission:           "academic:upload",
		Idempotency:          "inherent",
		AcademicVerification: "none",
		RequestContent:       "multipart/form-data",
		Body: &APIObject{Fields: []APIField{{
			Name: "file", Type: "string", Format: "binary", Required: true,
		}}},
	}}
	if err := schema.Normalize(); err != nil {
		t.Fatal(err)
	}
	if schema.Operations[0].RequestContent != "multipart/form-data" {
		t.Fatalf("request content = %q", schema.Operations[0].RequestContent)
	}

	schema.Operations[0].AcademicVerification = "bypass"
	if err := schema.Normalize(); err == nil || !strings.Contains(err.Error(), "academic_verification") {
		t.Fatalf("Normalize() error = %v", err)
	}
}
