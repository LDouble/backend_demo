//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/generator"
)

func TestGeneratedModuleCompiles(t *testing.T) {
	root := t.TempDir()
	schema, err := generator.Load(context.Background(), filepath.Join("..", "..", "schemas", "examples", "activity.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	const modulePath = "example.com/campus-generated"
	if _, err = generator.Generate(context.Background(), schema, generator.Options{
		Root: root, Source: "schemas/examples/activity.yaml", ModulePath: modulePath,
	}); err != nil {
		t.Fatal(err)
	}
	goMod := []byte("module " + modulePath + "\n\ngo 1.25.0\n\nrequire (\n\tgorm.io/gen v0.3.28\n\tgorm.io/gorm v1.31.2\n\tgorm.io/plugin/dbresolver v1.6.2\n\tgolang.org/x/tools v0.48.0\n)\n")
	if err = os.WriteFile(filepath.Join(root, "go.mod"), goMod, 0o600); err != nil {
		t.Fatal(err)
	}
	generatorMain := []byte(`package main

import "gorm.io/gen"

func main() {
	g := gen.NewGenerator(gen.Config{OutPath: "./internal/infrastructure/mysql/query", Mode: gen.WithDefaultQuery | gen.WithQueryInterface})
	applyGeneratedModules(g)
	g.Execute()
}
`)
	if err = os.WriteFile(filepath.Join(root, "internal", "infrastructure", "mysql", "generator", "main.go"), generatorMain, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	generateCommand := exec.CommandContext(ctx, "go", "run", "-mod=mod", "./internal/infrastructure/mysql/generator")
	generateCommand.Dir = root
	generateCommand.Env = append(os.Environ(), "GOWORK=off")
	if output, generateErr := generateCommand.CombinedOutput(); generateErr != nil {
		t.Fatalf("GORM Query generation failed: %v\n%s", generateErr, output)
	}
	command := exec.CommandContext(ctx, "go", "test", "-mod=mod", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated module does not compile: %v\n%s", err, output)
	}
}
