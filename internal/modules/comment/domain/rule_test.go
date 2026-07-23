package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestRules(t *testing.T) {
	t.Run("content", func(t *testing.T) {
		if got, err := NormalizeContent("  hello  "); err != nil || got != "hello" {
			t.Fatalf("NormalizeContent()=%q,%v", got, err)
		}
		if _, err := NormalizeContent(" "); !errors.Is(err, ErrContentRequired) {
			t.Fatalf("empty error=%v", err)
		}
		if _, err := NormalizeContent(strings.Repeat("界", MaxContentLength+1)); !errors.Is(err, ErrContentTooLong) {
			t.Fatalf("long error=%v", err)
		}
	})
	t.Run("depth", func(t *testing.T) {
		if depth, err := ReplyDepth(nil); err != nil || depth != 0 {
			t.Fatalf("root depth=%d,%v", depth, err)
		}
		if depth, err := ReplyDepth(&Comment{Depth: 7}); err != nil || depth != 8 {
			t.Fatalf("reply depth=%d,%v", depth, err)
		}
		if _, err := ReplyDepth(&Comment{Depth: 8}); !errors.Is(err, ErrDepthExceeded) {
			t.Fatalf("depth error=%v", err)
		}
	})
	t.Run("visibility", func(t *testing.T) {
		pending := &Comment{AuthorId: 7, Status: StatusPendingReview}
		if VisibleTo(nil, 7, false) || VisibleTo(pending, 8, false) {
			t.Fatal("pending comment leaked")
		}
		if !VisibleTo(pending, 7, false) || !VisibleTo(pending, 8, true) ||
			!VisibleTo(&Comment{Status: StatusApproved}, 0, false) {
			t.Fatal("visible comment hidden")
		}
	})
	t.Run("states", func(t *testing.T) {
		if !CanEdit(StatusApproved) || CanEdit(StatusWithdrawn) ||
			!CanWithdraw(StatusRejected) || CanWithdraw(StatusWithdrawn) ||
			!CanSubmitReview(StatusRejected) || CanSubmitReview(StatusApproved) ||
			!CanReview(StatusPendingReview) || CanReview(StatusRejected) ||
			!CanRevokeReview(StatusApproved) || !CanRevokeReview(StatusRejected) ||
			CanRevokeReview(StatusPendingReview) {
			t.Fatal("unexpected state rule")
		}
	})
	t.Run("actions", func(t *testing.T) {
		if got := AvailableActions(nil, 1, true, false); len(got) != 0 {
			t.Fatalf("nil actions=%v", got)
		}
		comment := &Comment{AuthorId: 1, Status: StatusApproved}
		got := AvailableActions(comment, 1, true, false)
		want := []string{ActionEdit, ActionWithdraw, ActionReply, ActionPin}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("actions=%v want=%v", got, want)
		}
		comment.ParentId = new(uint64)
		if got = AvailableActions(comment, 2, true, true); strings.Join(got, ",") != ActionReply {
			t.Fatalf("reply actions=%v", got)
		}
		comment.ParentId = nil
		if got = AvailableActions(comment, 2, true, true); strings.Join(got, ",") != ActionReply+","+ActionUnpin {
			t.Fatalf("pinned actions=%v", got)
		}
		comment.Status = StatusRejected
		if got = AvailableActions(comment, 1, false, false); strings.Join(got, ",") != ActionEdit+","+ActionWithdraw+","+ActionSubmitReview {
			t.Fatalf("rejected actions=%v", got)
		}
	})
}
