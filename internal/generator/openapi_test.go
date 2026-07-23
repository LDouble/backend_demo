package generator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateOpenAPIIsDeterministic(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, ".agent/modules.json"), `{"version":1,"modules":[{"name":"market","entity":"Listing","schema":"schemas/market.yaml"}]}`)
	mustWriteTestFile(t, filepath.Join(root, "api/openapi.base.yaml"), "openapi: 3.0.3\ninfo: {title: Test, version: 1.0.0}\npaths: {}\ncomponents:\n  responses:\n    Success: {description: ok}\n    Error: {description: error}\n")
	mustWriteTestFile(t, filepath.Join(root, "api/modules/market.yaml"), "openapi: 3.0.3\ninfo: {title: Market, version: 1.0.0}\npaths:\n  /api/v1/items:\n    post:\n      operationId: CreateItem\n      x-generated-module: market\n      responses: {'200': {$ref: '#/components/responses/MarketResponse'}}\ncomponents:\n  responses:\n    MarketResponse: {description: market response, content: {application/json: {schema: {$ref: '#/components/schemas/MarketResponseBody'}}}}\n  schemas:\n    MarketResponseBody: {type: object, properties: {data: {type: object}}}\n")
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
	if !strings.Contains(string(data), "MarketResponseBody") ||
		!strings.Contains(string(data), "MarketResponse") {
		t.Fatalf("composed contract omitted module components = %s", data)
	}
}

func TestGenerateOpenAPIDerivesCorePermissionManifest(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, ".agent/modules.json"), `{"version":1,"modules":[]}`)
	mustWriteTestFile(t, filepath.Join(root, "api/openapi.base.yaml"), `openapi: 3.0.3
info: {title: Test, version: 1.0.0}
paths:
  /api/v1/configs/{id}:
    parameters: [{name: id, in: path, required: true, schema: {type: integer}}]
    get: {operationId: GetConfig, responses: {'200': {description: ok}}}
  /api/v1/auth/login:
    post: {operationId: Login, responses: {'200': {description: ok}}}
  /api/v1/auth/me:
    get: {operationId: GetMe, responses: {'200': {description: ok}}}
components: {}
`)
	changed, err := GenerateOpenAPI(context.Background(), GenerateOpenAPIOptions{Root: root})
	if err != nil || !changed {
		t.Fatalf("GenerateOpenAPI() changed=%v error=%v", changed, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "permissions/modules/core.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"core:getconfig"`) || !strings.Contains(text, `"/api/v1/configs/:id"`) || !strings.Contains(text, `"core:getme"`) || strings.Contains(text, `"core:login"`) {
		t.Fatalf("core permission manifest=%s", text)
	}
	if err = os.WriteFile(filepath.Join(root, "permissions/modules/core.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = GenerateOpenAPI(context.Background(), GenerateOpenAPIOptions{Root: root, Check: true}); !errors.Is(err, ErrDrift) {
		t.Fatalf("permission drift error=%v", err)
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
