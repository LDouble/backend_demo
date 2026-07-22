package migration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLifecycleRootRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	link := filepath.Join(root, "migrations")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(link, "drafts", "escape.up.sql")
	if err := atomicWriteLifecycle(root, target, []byte("SELECT 1;\n")); err == nil {
		t.Fatal("migration writer accepted a symlink escaping the repository root")
	}
	if _, err := os.Stat(filepath.Join(external, "drafts", "escape.up.sql")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external migration was created: %v", err)
	}

	sentinel := filepath.Join(external, "sentinel.sql")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLifecycleFile(root, filepath.Join(link, "sentinel.sql")); err == nil {
		t.Fatal("migration reader followed a symlink outside the root")
	}
	if err := removeLifecycleFile(root, filepath.Join(link, "sentinel.sql")); err == nil {
		t.Fatal("migration remover followed a symlink outside the root")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "outside" {
		t.Fatalf("external sentinel changed: data=%q err=%v", data, err)
	}
}
