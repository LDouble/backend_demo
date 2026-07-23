package generator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

func TestGenerateDeterministicPreservesRulesAndDetectsDrift(t *testing.T) {
	root := t.TempDir()
	schema := validSchema()
	options := Options{Root: root, Source: "schemas/activity.yaml"}
	first, err := Generate(context.Background(), schema, options)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(first.Changed) == 0 {
		t.Fatal("first generation reported no changes")
	}
	rulePath := filepath.Join(root, "internal/modules/activity/domain/rule.go")
	customRule := []byte("package domain\n\nconst handwritten = true\n")
	if err = os.WriteFile(rulePath, customRule, 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := Generate(context.Background(), schema, options)
	if err != nil {
		t.Fatalf("second Generate() error = %v", err)
	}
	if len(second.Changed) != 0 {
		t.Fatalf("second generation changed = %v", second.Changed)
	}
	actualRule, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(actualRule) != string(customRule) {
		t.Fatal("handwritten rule was overwritten")
	}
	entityPath := filepath.Join(root, "internal/modules/activity/domain/entity.gen.go")
	if err = os.WriteFile(entityPath, []byte("drift"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := options
	check.Check = true
	result, err := Generate(context.Background(), schema, check)
	if !errors.Is(err, ErrDrift) || len(result.Changed) != 1 || result.Changed[0] != "internal/modules/activity/domain/entity.gen.go" {
		t.Fatalf("Generate(check) result=%#v error=%v", result, err)
	}
}

func TestGeneratedArtifacts(t *testing.T) {
	root := t.TempDir()
	schema := validSchema()
	schema.Components = map[string]map[string]any{
		"schemas": {
			"ActivityResponseBody": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data": map[string]any{"type": "object"},
				},
			},
		},
		"responses": {
			"ActivityResponse": map[string]any{
				"description": "Activity response",
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{
							"$ref": "#/components/schemas/ActivityResponseBody",
						},
					},
				},
			},
		},
	}
	if _, err := Generate(context.Background(), schema, Options{Root: root, Source: "schemas/activity.yaml"}); err != nil {
		t.Fatal(err)
	}
	files := []string{
		"internal/modules/activity/domain/entity.gen.go",
		"internal/modules/activity/infrastructure/repository.gen.go",
		"api/modules/activity.yaml",
		"permissions/modules/activity.json",
		".agent/modules.json",
		"internal/infrastructure/mysql/generator/modules.gen.go",
	}
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
	loader := openapi3.NewLoader()
	document, err := loader.LoadFromFile(filepath.Join(root, "api/modules/activity.yaml"))
	if err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}
	if err = document.Validate(context.Background()); err != nil {
		t.Fatalf("validate generated OpenAPI: %v", err)
	}
	if document.Components == nil ||
		document.Components.Schemas["ActivityResponseBody"] == nil ||
		document.Components.Responses["ActivityResponse"] == nil {
		t.Fatal("generated OpenAPI omitted schema-owned components")
	}
	modules, err := ListModules(context.Background(), root)
	if err != nil || len(modules) != 1 || modules[0].Name != "activity" {
		t.Fatalf("ListModules() = %#v, %v", modules, err)
	}
	if _, err := os.Stat(filepath.Join(root, "migrations/modules/activity.up.sql")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ordinary generation created a migration draft: %v", err)
	}
}

func TestGeneratedOpenAPIGroupsMethodsAndPreservesQueryConstraints(t *testing.T) {
	minimum, maximum, maxLength := int64(1), int64(100), int64(32)
	schema := validSchema()
	schema.Operations = []APIOperation{
		{
			OperationID: "ListActivities", Method: "GET", Path: "/api/v1/activities",
			Permission: "activity:list", Summary: "List activities",
			Query: []APIParameter{
				{Name: "page", Type: "integer", Format: "int64", Minimum: &minimum, Maximum: &maximum},
				{Name: "keyword", Type: "string", MaxLength: &maxLength},
			},
		},
		{
			OperationID: "CreateActivity", Method: "POST", Path: "/api/v1/activities",
			Permission: "activity:create", Idempotency: "inherent", Summary: "Create activity",
		},
	}
	root := t.TempDir()
	if _, err := Generate(context.Background(), schema, Options{Root: root, Source: "schemas/activity.yaml"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "api/modules/activity.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	type parameterSchema struct {
		Minimum   *int64 `yaml:"minimum"`
		Maximum   *int64 `yaml:"maximum"`
		MaxLength *int64 `yaml:"maxLength"`
	}
	type operation struct {
		Parameters []struct {
			Schema parameterSchema `yaml:"schema"`
		} `yaml:"parameters"`
	}
	type pathItem struct {
		Get  *operation `yaml:"get"`
		Post *operation `yaml:"post"`
	}
	var document struct {
		Paths map[string]pathItem `yaml:"paths"`
	}
	if err = yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	item, ok := document.Paths["/api/v1/activities"]
	if !ok || item.Get == nil || item.Post == nil {
		t.Fatalf("generated path item = %#v", item)
	}
	parameters := item.Get.Parameters
	if len(parameters) != 2 {
		t.Fatalf("query parameters = %#v", parameters)
	}
	page := parameters[0].Schema
	if page.Minimum == nil || *page.Minimum != 1 || page.Maximum == nil || *page.Maximum != 100 {
		t.Fatalf("page schema = %#v", page)
	}
	keyword := parameters[1].Schema
	if keyword.MaxLength == nil || *keyword.MaxLength != 32 {
		t.Fatalf("keyword schema = %#v", keyword)
	}
}

func TestGenerateV2MultipleEntitiesAndDependencies(t *testing.T) {
	root := t.TempDir()
	schema := Schema{Version: 2, Module: "notice", CRUD: []string{"create", "get"}, Entities: []Entity{
		{Name: "Recipient", Table: "notice_recipients", Fields: []Field{{Name: "notice_id", Type: "uint64", Required: true}, {Name: "user_id", Type: "uint64", Required: true}}, Indexes: []Index{{Name: "uk_notice_recipient", Fields: []string{"notice_id", "user_id"}, Unique: true}}, ForeignKeys: []ForeignKey{{Field: "notice_id", References: "notices.id", OnDelete: "CASCADE"}}},
		{Name: "Notice", Table: "notices", Primary: true, Fields: []Field{{Name: "title", Type: "string", Required: true, Index: true}}},
	}}
	if _, err := Generate(context.Background(), schema, Options{Root: root, Source: "schemas/notice.yaml"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"notices.gen.go", "notice_recipients.gen.go"} {
		if _, err := os.Stat(filepath.Join(root, "internal/modules/notice/domain", name)); err != nil {
			t.Fatalf("generated entity %s: %v", name, err)
		}
	}
	registration, err := os.ReadFile(filepath.Join(root, "internal/infrastructure/mysql/generator/modules.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(registration), "noticedomain.Notice{}") || !strings.Contains(string(registration), "noticedomain.Recipient{}") {
		t.Fatalf("registration does not include every entity:\n%s", registration)
	}
}

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	if _, err := safeJoin(root, "../../escape"); err == nil {
		t.Fatal("safeJoin accepted path traversal")
	}
	if _, err := safeJoin(root, filepath.Join(root, "absolute")); err == nil {
		t.Fatal("safeJoin accepted absolute path")
	}
}

func TestListModulesMissingAndInvalid(t *testing.T) {
	root := t.TempDir()
	modules, err := ListModules(context.Background(), root)
	if err != nil || len(modules) != 0 {
		t.Fatalf("ListModules(missing) = %#v, %v", modules, err)
	}
	path := filepath.Join(root, ".agent", "modules.json")
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = ListModules(context.Background(), root); err == nil || !strings.Contains(err.Error(), "decode module registry") {
		t.Fatalf("ListModules(invalid) error = %v", err)
	}
	if err = os.WriteFile(path, []byte(`{"version":2,"modules":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = ListModules(context.Background(), root); err == nil || !strings.Contains(err.Error(), "unsupported module registry") {
		t.Fatalf("ListModules(version) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = ListModules(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListModules(cancelled) error = %v", err)
	}
}

func TestDiscoverModulesDetectsDeletedSchemaAsStale(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "schemas"), 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(validSchema())
	if err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join(root, "schemas", "activity.yaml")
	if err = os.WriteFile(schemaPath, data, 0o640); err != nil {
		t.Fatal(err)
	}
	modules, err := DiscoverModules(context.Background(), root)
	if err != nil || len(modules) != 1 {
		t.Fatalf("discovered=%v err=%v", modules, err)
	}
	if err = SyncModuleRegistry(context.Background(), root, modules, false); err != nil {
		t.Fatal(err)
	}
	if err = SyncModuleRegistry(context.Background(), root, modules, true); err != nil {
		t.Fatal(err)
	}
	global, err := GlobalManagedFiles(context.Background(), root, modules)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(global, "api/openapi.yaml") || !containsString(global, "internal/infrastructure/mysql/query/activities.gen.go") {
		t.Fatalf("global managed files=%v", global)
	}
	result, err := Generate(context.Background(), validSchema(), Options{Root: root, Source: modules[0].Schema})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ReconcileManagedFiles(context.Background(), root, result.Managed, false, false); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(schemaPath); err != nil {
		t.Fatal(err)
	}
	modules, err = DiscoverModules(context.Background(), root)
	if err != nil || len(modules) != 0 {
		t.Fatalf("after deletion discovered=%v err=%v", modules, err)
	}
	stale, err := FindStaleManagedFiles(root, []string{".agent/modules.json"})
	if err != nil || len(stale) == 0 {
		t.Fatalf("stale=%v err=%v", stale, err)
	}
}

func TestDiscoverModulesRejectsDuplicateDeclarations(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "schemas"), 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(validSchema())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.yaml", "second.yaml"} {
		if err = os.WriteFile(filepath.Join(root, "schemas", name), data, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = DiscoverModules(context.Background(), root); err == nil || !strings.Contains(err.Error(), "declared by both") {
		t.Fatalf("duplicate discovery error=%v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
