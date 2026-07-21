package generator

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryRootRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	link := filepath.Join(root, "generated")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(link, "artifact.go")
	if err := atomicWrite(root, target, []byte("package escaped\n")); err == nil {
		t.Fatal("atomicWrite accepted a symlink escaping the repository root")
	}
	if _, err := os.Stat(filepath.Join(external, "artifact.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external artifact was created: %v", err)
	}

	sentinel := filepath.Join(external, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repositoryReadFile(root, filepath.Join(link, "sentinel.txt")); err == nil {
		t.Fatal("repositoryReadFile followed a symlink outside the root")
	}
	if err := repositoryRemove(root, filepath.Join(link, "sentinel.txt")); err == nil {
		t.Fatal("repositoryRemove followed a symlink outside the root")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "outside" {
		t.Fatalf("external sentinel changed: data=%q err=%v", data, err)
	}
}
