package domainevent

import (
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestWriteRejectsNilTx(t *testing.T) {
	err := Write(nil, "activity", 1, "activity.created", nil)
	if err == nil {
		t.Fatal("expected error when tx is nil")
	}
	if !strings.Contains(err.Error(), "tx is nil") {
		t.Fatalf("error=%v, want error mentioning tx is nil", err)
	}
}

func TestWriteRejectsEmptyFields(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		agg  string
		id   uint64
		kind string
	}{
		{"empty aggregate", "", 1, "x"},
		{"zero id", "activity", 0, "x"},
		{"empty kind", "activity", 1, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Write(db, tc.agg, tc.id, tc.kind, nil)
			if err == nil {
				t.Fatal("expected error for empty fields")
			}
		})
	}
}

func TestWriteVersionedReplayAndMarshalErrors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&Event{}); err != nil {
		t.Fatal(err)
	}
	payload := versionedPayload{Version: 3}
	if err = Write(db, "activity", 7, "updated", payload); err != nil {
		t.Fatal(err)
	}
	if err = Write(db, "activity", 7, "updated", payload); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err = Write(db, "activity", 7, "updated", map[string]any{"version": uint64(3), "different": true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict=%v", err)
	}
	if err = Write(db, "activity", 7, "marshal", make(chan int)); err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Fatalf("marshal error=%v", err)
	}
	if err = WriteWithKey(db, "activity", 7, "marshal", "key", make(chan int)); err == nil || !strings.Contains(err.Error(), "marshal") {
		t.Fatalf("keyed marshal error=%v", err)
	}
	if err = WriteWithKey(db, "", 7, "updated", "key", nil); err == nil {
		t.Fatal("invalid keyed event was accepted")
	}
}

func TestCurrentVersionExtractsFromMap(t *testing.T) {
	if got := currentVersion(map[string]any{"version": uint64(7)}); got != 7 {
		t.Fatalf("got %d want 7", got)
	}
	if got := currentVersion(map[string]any{"version": 5}); got != 5 {
		t.Fatalf("got %d want 5", got)
	}
	for name, test := range map[string]struct {
		value any
		want  uint64
	}{
		"uint32":   {value: uint32(2), want: 2},
		"uint":     {value: uint(3), want: 3},
		"int64":    {value: int64(4), want: 4},
		"float64":  {value: float64(6), want: 6},
		"negative": {value: -1, want: 0},
	} {
		t.Run(name, func(t *testing.T) {
			if got := currentVersion(map[string]any{"version": test.value}); got != test.want {
				t.Fatalf("got=%d want=%d", got, test.want)
			}
		})
	}
	if got := currentVersion(map[string]any{}); got != 0 {
		t.Fatalf("got %d want 0", got)
	}
	if got := currentVersion(nil); got != 0 {
		t.Fatalf("got %d want 0 for nil", got)
	}
	if got := currentVersion(struct {
		Version uint64
		ID      uint64
	}{Version: 9, ID: 3}); got != 0 {
		// struct without GetVersion falls back to 0
		t.Fatalf("got %d want 0 for plain struct", got)
	}
}

type versionedPayload struct {
	Version uint64
}

func (v versionedPayload) GetVersion() uint64 { return v.Version }

func TestCurrentVersionExtractsFromVersionedInterface(t *testing.T) {
	if got := currentVersion(versionedPayload{Version: 42}); got != 42 {
		t.Fatalf("got %d want 42", got)
	}
}

func TestWriteWithKeyReplayAndConflict(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&Event{}); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"id": 1, "version": 1}
	if err = WriteWithKey(db, "activity", 1, "created", strings.Repeat("k", 128), payload); err != nil {
		t.Fatal(err)
	}
	if err = WriteWithKey(db, "activity", 1, "created", strings.Repeat("k", 128), payload); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err = WriteWithKey(db, "activity", 1, "created", strings.Repeat("k", 128), map[string]any{"id": 2}); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	var event Event
	if err = db.Take(&event).Error; err != nil {
		t.Fatal(err)
	}
	if len(event.IdempotencyKey) != sha256.Size*2 {
		t.Fatalf("key length = %d", len(event.IdempotencyKey))
	}
}
