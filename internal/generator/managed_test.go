package generator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReconcileManagedFilesLifecycle(t *testing.T) {
	root := t.TempDir()
	generatedPath := filepath.Join(root, "internal", "old.gen.go")
	if err := os.MkdirAll(filepath.Dir(generatedPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(generatedPath, []byte("// "+generatedHeader+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := ReconcileManagedFiles(ctx, root, []string{"internal/old.gen.go"}, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileManagedFiles(ctx, root, []string{"internal/old.gen.go"}, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileManagedFiles(ctx, root, []string{}, false, false); !errors.Is(err, ErrStaleArtifacts) {
		t.Fatalf("stale error = %v", err)
	}
	stale, err := ReconcileManagedFiles(ctx, root, []string{}, false, true)
	if err != nil || len(stale) != 1 {
		t.Fatalf("stale=%v err=%v", stale, err)
	}
	if _, err = os.Stat(generatedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generated file still exists: %v", err)
	}
}

func TestReconcileManagedFilesRefusesHumanFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "human.go")
	if err := os.WriteFile(path, []byte("package human\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := ReconcileManagedFiles(ctx, root, []string{"human.go"}, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileManagedFiles(ctx, root, nil, false, true); err == nil {
		t.Fatal("expected prune to reject an unmarked file")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("human file was removed: %v", err)
	}
}

func TestReconcileManagedFilesCheckDetectsManifestDrift(t *testing.T) {
	root := t.TempDir()
	_, err := ReconcileManagedFiles(context.Background(), root, []string{"a.gen.go"}, true, false)
	if !errors.Is(err, ErrDrift) {
		t.Fatalf("check error = %v", err)
	}
}
