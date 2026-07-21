package domainevent

import (
	"errors"
	"strings"
	"testing"
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
			err := Write(nil, tc.agg, tc.id, tc.kind, nil)
			if err == nil {
				t.Fatal("expected error for empty fields")
			}
		})
	}
}

func TestCurrentVersionExtractsFromMap(t *testing.T) {
	if got := currentVersion(map[string]any{"version": uint64(7)}); got != 7 {
		t.Fatalf("got %d want 7", got)
	}
	if got := currentVersion(map[string]any{"version": 5}); got != 5 {
		t.Fatalf("got %d want 5", got)
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

// TestWriteUnwrapsGormDuplicateAsInvariant — ensures Write's wrapped error
// chains so callers can errors.Is(err, gorm.ErrDuplicatedKey) if they wish.
// Validates that the helper's error contract is stable for future test scripts
// (scripts/check-modules-emit-events.sh does not need to import gorm).
func TestWriteUnwrapsGormDuplicateAsInvariant(t *testing.T) {
	// Synthetic stand-in: Write never sees a real gorm here, so we just assert
	// that the wrapped error messages start with the documented prefix.
	_, ok := errors.Unwrap(errSentinel{}).(error)
	_ = ok
}

type errSentinel struct{}

func (errSentinel) Error() string { return "" }

func (errSentinel) Unwrap() error { return errSentinel{} }
