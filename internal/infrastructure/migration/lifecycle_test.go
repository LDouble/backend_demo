package migration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleSnapshotPlanPromoteAndChecksum(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "schemas/activity.yaml", testSchema(""))
	writeTestFile(t, root, "migrations/000001_core.up.sql", "SELECT 1;\n")
	writeTestFile(t, root, "migrations/000001_core.down.sql", "SELECT 1;\n")
	if err := Snapshot(context.Background(), root, "activity"); err != nil {
		t.Fatal(err)
	}
	plan, err := Plan(context.Background(), root, "activity")
	if err != nil || plan.UpSQL != "" {
		t.Fatalf("baseline plan=%+v err=%v", plan, err)
	}
	writeTestFile(t, root, "schemas/activity.yaml", testSchema("      - {name: location, type: string, size: 200}\n"))
	draft, err := NewDraft(context.Background(), root, "add_location", "activity")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTestFile(t, draft.Up), "ADD COLUMN location") {
		t.Fatalf("unexpected draft: %s", readTestFile(t, draft.Up))
	}
	promoted, err := Promote(context.Background(), root, draft.Name, time.Date(2026, 7, 21, 12, 34, 56, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if promoted.Version != "20260721123456" {
		t.Fatalf("version=%s", promoted.Version)
	}
	rows, err := List(root)
	if err != nil || len(rows) != 2 {
		t.Fatalf("List=%+v err=%v", rows, err)
	}
	if err = Check(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "migrations/000001_core.up.sql", "SELECT 2;\n")
	if err = Check(context.Background(), root); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("checksum error=%v", err)
	}
	before := readTestFile(t, filepath.Join(root, ".agent/schema-snapshots/activity.yaml"))
	if err = Snapshot(context.Background(), root, "activity"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Snapshot checksum error=%v", err)
	}
	after := readTestFile(t, filepath.Join(root, ".agent/schema-snapshots/activity.yaml"))
	if after != before {
		t.Fatal("Snapshot updated the baseline after immutable migration validation failed")
	}
	draft, err = NewDraft(context.Background(), root, "blocked_promote", "activity")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Promote(context.Background(), root, draft.Name, time.Date(2026, 7, 21, 12, 35, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Promote checksum error=%v", err)
	}
}

func TestCheckRejectsManifestMembershipDrift(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "schemas/activity.yaml", testSchema(""))
	writeTestFile(t, root, "migrations/000001_core.up.sql", "SELECT 1;\n")
	writeTestFile(t, root, "migrations/000001_core.down.sql", "SELECT 1;\n")
	if err := Snapshot(context.Background(), root, "activity"); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "migrations/manifest.json")
	var value manifest
	if err := json.Unmarshal([]byte(readTestFile(t, manifestPath)), &value); err != nil {
		t.Fatal(err)
	}
	value.Files = value.Files[:1]
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "migrations/manifest.json", string(data))
	if err = Check(context.Background(), root); err == nil || !strings.Contains(err.Error(), "missing file") {
		t.Fatalf("membership error=%v", err)
	}
}

func TestPlanRejectsDestructiveChanges(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "schemas/activity.yaml", testSchema("      - {name: location, type: string, size: 200}\n"))
	writeTestFile(t, root, "migrations/000001_core.up.sql", "SELECT 1;\n")
	writeTestFile(t, root, "migrations/000001_core.down.sql", "SELECT 1;\n")
	if err := Snapshot(context.Background(), root, "activity"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "schemas/activity.yaml", testSchema(""))
	plan, err := Plan(context.Background(), root, "activity")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Destructive) != 1 || !strings.Contains(plan.Destructive[0], "drop column") {
		t.Fatalf("destructive=%v", plan.Destructive)
	}
	if _, err = NewDraft(context.Background(), root, "drop_location", "activity"); err == nil {
		t.Fatal("destructive draft was accepted")
	}
}

func testSchema(extra string) string {
	return "version: 2\nmodule: activity\nentities:\n  - name: Activity\n    table: activities\n    primary: true\n    fields:\n      - {name: title, type: string, size: 200, required: true}\n" + extra + "crud: [create, get]\n"
}

func writeTestFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
