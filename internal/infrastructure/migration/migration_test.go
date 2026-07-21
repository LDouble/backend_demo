package migration

import (
	"context"
	"errors"
	"testing"
)

func TestRunRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(ctx, "unused", "up", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error=%v want context.Canceled", err)
	}
}
