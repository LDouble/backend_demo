package generator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateOpenAPIIsDeterministic(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, ".agent/modules.json"), `{"version":1,"modules":[{"name":"market","entity":"Listing","schema":"schemas/market.yaml"}]}`)
	mustWriteTestFile(t, filepath.Join(root, "api/openapi.yaml"), "openapi: 3.0.3\ninfo: {title: Test, version: 1.0.0}\npaths: {}\ncomponents:\n  responses:\n    Success: {description: ok}\n    Error: {description: error}\n")
	mustWriteTestFile(t, filepath.Join(root, "api/modules/market.yaml"), "openapi: 3.0.3\ninfo: {title: Market, version: 1.0.0}\npaths:\n  /api/v1/items:\n    post:\n      operationId: CreateItem\n      x-generated-module: market\n      responses: {'200': {description: ok}}\n")
	changed, err := GenerateOpenAPI(context.Background(), GenerateOpenAPIOptions{Root: root})
	if err != nil || !changed {
		t.Fatalf("GenerateOpenAPI() changed=%v error=%v", changed, err)
	}
	changed, err = GenerateOpenAPI(context.Background(), GenerateOpenAPIOptions{Root: root, Check: true})
	if err != nil || changed {
		t.Fatalf("GenerateOpenAPI(check) changed=%v error=%v", changed, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "api/openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "CreateItem") {
		t.Fatalf("composed contract = %s", data)
	}
}

func mustWriteTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
