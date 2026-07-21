package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/generator"
)

const testSchema = `version: 1
module: notice
entity:
  name: Notice
  table: notices
  fields:
    - name: title
      type: string
      required: true
crud: [create, get, list]
permissions:
  - name: notice:list
    path: /api/v1/notices
    methods: [GET]
`

func TestModuleCommands(t *testing.T) {
	root := t.TempDir()
	schemaPath := filepath.Join(root, "notice.yaml")
	if err := os.WriteFile(schemaPath, []byte(testSchema), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "validate", args: []string{"module", "validate", schemaPath}, want: "module=notice entity=Notice"},
		{name: "generate", args: []string{"generate", "module", schemaPath, "--root", root}, want: "generated module=notice"},
		{name: "check", args: []string{"generate", "module", schemaPath, "--check", "--root", root}, want: "checked module=notice"},
		{name: "list", args: []string{"module", "list", "--root", root}, want: "notice\tNotice"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := run(context.Background(), test.args, &output); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if !strings.Contains(output.String(), test.want) {
				t.Fatalf("run() output = %q, want %q", output.String(), test.want)
			}
		})
	}
	entity := filepath.Join(root, "internal/modules/notice/domain/entity.gen.go")
	if err := os.WriteFile(entity, []byte("drift"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"generate", "module", schemaPath, "--check", "--root", root}, &bytes.Buffer{}); !errors.Is(err, generator.ErrDrift) {
		t.Fatalf("run(check drift) error = %v", err)
	}
}

func TestModuleCommandUsage(t *testing.T) {
	tests := [][]string{
		nil,
		{"module"},
		{"module", "validate"},
		{"generate", "module"},
		{"generate", "unknown", "schema.yaml"},
	}
	for _, args := range tests {
		if err := run(context.Background(), args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Fatalf("run(%v) error = %v", args, err)
		}
	}
}
