package generator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
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
	if _, err := Generate(context.Background(), validSchema(), Options{Root: root, Source: "schemas/activity.yaml"}); err != nil {
		t.Fatal(err)
	}
	files := []string{
		"internal/modules/activity/domain/entity.gen.go",
		"internal/modules/activity/infrastructure/repository.gen.go",
		"api/modules/activity.yaml",
		"migrations/modules/activity.up.sql",
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
	modules, err := ListModules(context.Background(), root)
	if err != nil || len(modules) != 1 || modules[0].Name != "activity" {
		t.Fatalf("ListModules() = %#v, %v", modules, err)
	}
	migration, err := os.ReadFile(filepath.Join(root, "migrations/modules/activity.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migration), "BIGINT UNSIGNED") || !strings.Contains(string(migration), "idx_activities_title") {
		t.Fatalf("unexpected migration:\n%s", migration)
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
