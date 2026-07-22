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

func TestPlanDiffsExistingIndexAndForeignKeyDefinitions(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "schemas/relation.yaml", relationSchema("[parent_id]", false, true, "CASCADE"))
	writeTestFile(t, root, "migrations/000001_core.up.sql", "SELECT 1;\n")
	writeTestFile(t, root, "migrations/000001_core.down.sql", "SELECT 1;\n")
	if err := Snapshot(context.Background(), root, "relation"); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "schemas/relation.yaml", relationSchema("[parent_id, label]", true, true, "RESTRICT"))
	plan, err := Plan(context.Background(), root, "relation")
	if err != nil {
		t.Fatal(err)
	}
	assertOrderedSQL(t, plan.UpSQL,
		"DROP FOREIGN KEY fk_children_parent_id",
		"DROP INDEX idx_children_parent",
		"ADD UNIQUE INDEX idx_children_parent (parent_id, label)",
		"ADD CONSTRAINT fk_children_parent_id FOREIGN KEY (parent_id) REFERENCES parents(id) ON DELETE RESTRICT",
	)
	assertOrderedSQL(t, plan.DownSQL,
		"DROP FOREIGN KEY fk_children_parent_id",
		"DROP INDEX idx_children_parent",
		"ADD INDEX idx_children_parent (parent_id)",
		"ADD CONSTRAINT fk_children_parent_id FOREIGN KEY (parent_id) REFERENCES parents(id) ON DELETE CASCADE",
	)
	if !containsChange(plan.Destructive, "change index children.idx_children_parent") ||
		!containsChange(plan.Destructive, "change foreign key children.parent_id") {
		t.Fatalf("destructive=%v", plan.Destructive)
	}
}

func TestPlanGeneratesReversibleForeignKeyAddAndDrop(t *testing.T) {
	tests := []struct {
		name            string
		previousHasKey  bool
		currentHasKey   bool
		wantDestructive bool
	}{
		{name: "add", currentHasKey: true},
		{name: "drop", previousHasKey: true, wantDestructive: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "schemas/relation.yaml", relationSchema("[parent_id]", false, test.previousHasKey, "CASCADE"))
			writeTestFile(t, root, "migrations/000001_core.up.sql", "SELECT 1;\n")
			writeTestFile(t, root, "migrations/000001_core.down.sql", "SELECT 1;\n")
			if err := Snapshot(context.Background(), root, "relation"); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, root, "schemas/relation.yaml", relationSchema("[parent_id]", false, test.currentHasKey, "CASCADE"))
			plan, err := Plan(context.Background(), root, "relation")
			if err != nil {
				t.Fatal(err)
			}
			combinedSQL := plan.UpSQL + "\n" + plan.DownSQL
			if !strings.Contains(combinedSQL, "ADD CONSTRAINT fk_children_parent_id FOREIGN KEY") ||
				!strings.Contains(combinedSQL, "DROP FOREIGN KEY fk_children_parent_id") {
				t.Fatalf("plan=%+v", plan)
			}
			if test.wantDestructive != containsChange(plan.Destructive, "drop foreign key children.parent_id") {
				t.Fatalf("destructive=%v", plan.Destructive)
			}
		})
	}
}

func relationSchema(indexFields string, unique, withForeignKey bool, onDelete string) string {
	uniqueValue := ""
	if unique {
		uniqueValue = ", unique: true"
	}
	foreignKey := ""
	if withForeignKey {
		foreignKey = "    foreign_keys: [{field: parent_id, references: parents.id, on_delete: " + onDelete + "}]\n"
	}
	return "version: 2\nmodule: relation\nentities:\n" +
		"  - name: Parent\n    table: parents\n    primary: true\n    fields:\n      - {name: name, type: string, size: 100, required: true}\n" +
		"  - name: Child\n    table: children\n    fields:\n      - {name: parent_id, type: uint64, required: true}\n      - {name: label, type: string, size: 100, required: true}\n" +
		"    indexes: [{name: idx_children_parent, fields: " + indexFields + uniqueValue + "}]\n" + foreignKey +
		"crud: [create, get]\n"
}

func assertOrderedSQL(t *testing.T, sql string, fragments ...string) {
	t.Helper()
	position := -1
	for _, fragment := range fragments {
		next := strings.Index(sql, fragment)
		if next <= position {
			t.Fatalf("SQL fragment %q is missing or out of order:\n%s", fragment, sql)
		}
		position = next
	}
}

func containsChange(changes []string, want string) bool {
	for _, change := range changes {
		if change == want {
			return true
		}
	}
	return false
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
