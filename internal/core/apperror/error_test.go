package apperror

import (
	"errors"
	"testing"
)

func TestApplicationError(t *testing.T) {
	cause := errors.New("cause")
	err := Wrap(409, "conflict", "冲突", cause)
	if err.Error() != "冲突" || !errors.Is(err, cause) {
		t.Fatal("error chain invalid")
	}
	got, ok := As(err)
	if !ok || got.Code != "conflict" {
		t.Fatalf("got=%v ok=%v", got, ok)
	}
	plain := New(400, "bad", "bad")
	if _, ok = As(plain); !ok {
		t.Fatal("new error not recognized")
	}
	if _, ok = As(errors.New("other")); ok {
		t.Fatal("plain error recognized")
	}
}
