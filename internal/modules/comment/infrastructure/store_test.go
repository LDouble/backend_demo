package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	commentapp "github.com/weouc-plus/campus-platform/internal/modules/comment/application"
	"github.com/weouc-plus/campus-platform/internal/modules/comment/domain"
	"gorm.io/gorm"
)

func TestStoreLifecycleAndVisibility(t *testing.T) {
	ctx := context.Background()
	store := NewStore(storeDB(t))
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)

	root, err := store.Create(ctx, 1, commentapp.CreateInput{
		TargetType: commentapp.TargetActivity, TargetID: 8, Content: "root",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get(ctx, root.Comment.ID, 2, false); err == nil {
		t.Fatal("pending comment leaked")
	}
	if _, err = store.Get(ctx, root.Comment.ID, 0, true); err != nil {
		t.Fatalf("admin get: %v", err)
	}
	if _, _, err = store.Thread(ctx, root.Comment.ID, 2, false); err == nil {
		t.Fatal("pending thread leaked")
	}
	root, err = store.Review(ctx, root.Comment.ID, 90, 1, true, "", now)
	if err != nil {
		t.Fatal(err)
	}

	child, err := store.Create(ctx, 2, commentapp.CreateInput{
		TargetType: commentapp.TargetActivity,
		TargetID:   8,
		ParentID:   &root.Comment.ID,
		Content:    "child",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if child.Comment.RootId == nil || *child.Comment.RootId != root.Comment.ID ||
		child.Comment.ReplyToUserId == nil || *child.Comment.ReplyToUserId != 1 {
		t.Fatalf("unexpected child: %+v", child)
	}
	if _, err = store.Create(ctx, 3, commentapp.CreateInput{
		TargetType: commentapp.TargetActivity,
		TargetID:   8,
		ParentID:   &child.Comment.ID,
		Content:    "hidden",
	}, now); err == nil {
		t.Fatal("non-author replied to pending comment")
	}
	child, err = store.Review(ctx, child.Comment.ID, 90, 1, true, "", now)
	if err != nil {
		t.Fatal(err)
	}

	roots, total, err := store.ListRoots(ctx, commentapp.TargetActivity, 8, 3, 1, 20)
	if err != nil || total != 1 || len(roots) != 1 || roots[0].ReplyCount != 1 {
		t.Fatalf("roots=%+v total=%d err=%v", roots, total, err)
	}
	threadRoot, descendants, err := store.Thread(ctx, child.Comment.ID, 3, false)
	if err != nil || threadRoot.Comment.ID != root.Comment.ID ||
		len(descendants) != 1 || descendants[0].Comment.ID != child.Comment.ID {
		t.Fatalf("thread root=%+v descendants=%+v err=%v", threadRoot, descendants, err)
	}
	mine, total, err := store.ListMine(ctx, 2, domain.StatusApproved, 1, 20)
	if err != nil || total != 1 || len(mine) != 1 {
		t.Fatalf("mine=%+v total=%d err=%v", mine, total, err)
	}
	adminRows, total, err := store.ListAdmin(ctx, commentapp.Search{
		TargetType: commentapp.TargetActivity,
		TargetID:   8,
		AuthorID:   2,
		Status:     domain.StatusApproved,
		Keyword:    "hil",
	}, 1, 20)
	if err != nil || total != 1 || len(adminRows) != 1 {
		t.Fatalf("admin=%+v total=%d err=%v", adminRows, total, err)
	}

	root, err = store.Pin(ctx, root.Comment.ID, 99, root.Comment.Version, now)
	if err != nil || !root.Pinned {
		t.Fatalf("pin=%+v err=%v", root, err)
	}
	newer := approvedRoot(t, store, 4, now.Add(time.Second))
	pinnedPage, pinnedTotal, err := store.ListRoots(
		ctx,
		commentapp.TargetActivity,
		8,
		4,
		1,
		1,
	)
	if err != nil || pinnedTotal != 2 || len(pinnedPage) != 1 ||
		pinnedPage[0].Comment.ID != root.Comment.ID {
		t.Fatalf("pinned page=%+v total=%d err=%v", pinnedPage, pinnedTotal, err)
	}
	secondPage, _, err := store.ListRoots(ctx, commentapp.TargetActivity, 8, 4, 2, 1)
	if err != nil || len(secondPage) != 1 || secondPage[0].Comment.ID != newer.Comment.ID {
		t.Fatalf("second page=%+v err=%v", secondPage, err)
	}
	if _, err = store.Unpin(ctx, child.Comment.ID, 99, child.Comment.Version, now); err == nil {
		t.Fatal("unpinned a non-pinned reply")
	}
	root, err = store.Unpin(ctx, root.Comment.ID, 99, root.Comment.Version, now)
	if err != nil || root.Pinned {
		t.Fatalf("unpin=%+v err=%v", root, err)
	}

	if _, err = store.Update(ctx, child.Comment.ID, 3, child.Comment.Version, "x", now); err == nil {
		t.Fatal("non-author updated comment")
	}
	if _, err = store.Update(ctx, child.Comment.ID, 2, 999, "x", now); err == nil {
		t.Fatal("stale update succeeded")
	}
	child, err = store.Update(ctx, child.Comment.ID, 2, child.Comment.Version, "edited", now)
	if err != nil || child.Comment.Status != domain.StatusPendingReview {
		t.Fatalf("update=%+v err=%v", child, err)
	}
	child, err = store.Review(ctx, child.Comment.ID, 90, child.Comment.Version, false, "no", now)
	if err != nil || child.Comment.Status != domain.StatusRejected {
		t.Fatalf("reject=%+v err=%v", child, err)
	}
	child, err = store.SubmitReview(ctx, child.Comment.ID, 2, child.Comment.Version, now)
	if err != nil || child.Comment.Status != domain.StatusPendingReview {
		t.Fatalf("submit=%+v err=%v", child, err)
	}
	child, err = store.Review(ctx, child.Comment.ID, 90, child.Comment.Version, true, "", now)
	if err != nil {
		t.Fatal(err)
	}
	child, err = store.RevokeReview(ctx, child.Comment.ID, 90, child.Comment.Version, "audit", now)
	if err != nil || child.Comment.Status != domain.StatusPendingReview {
		t.Fatalf("revoke=%+v err=%v", child, err)
	}
	child, err = store.Withdraw(ctx, child.Comment.ID, 2, child.Comment.Version, now)
	if err != nil || child.Comment.Status != domain.StatusWithdrawn {
		t.Fatalf("withdraw=%+v err=%v", child, err)
	}
	if _, err = store.Withdraw(ctx, child.Comment.ID, 2, child.Comment.Version, now); err == nil {
		t.Fatal("second withdraw succeeded")
	}
}

func TestStorePinReplacementAndErrors(t *testing.T) {
	ctx := context.Background()
	store := NewStore(storeDB(t))
	now := time.Now().UTC()
	first := approvedRoot(t, store, 1, now)
	second := approvedRoot(t, store, 2, now)

	first, err := store.Pin(ctx, first.Comment.ID, 99, first.Comment.Version, now)
	if err != nil || !first.Pinned {
		t.Fatal(err)
	}
	second, err = store.Pin(ctx, second.Comment.ID, 99, second.Comment.Version, now.Add(time.Second))
	if err != nil || !second.Pinned {
		t.Fatal(err)
	}
	first, err = store.Get(ctx, first.Comment.ID, 1, false)
	if err != nil || first.Pinned {
		t.Fatalf("pin replacement failed: %+v %v", first, err)
	}

	reply, err := store.Create(ctx, 3, commentapp.CreateInput{
		TargetType: commentapp.TargetActivity,
		TargetID:   8,
		ParentID:   &second.Comment.ID,
		Content:    "reply",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pin(ctx, reply.Comment.ID, 99, reply.Comment.Version, now); err == nil {
		t.Fatal("pending reply pinned")
	}
	if _, err = store.Get(ctx, 9999, 1, false); err == nil {
		t.Fatal("missing comment returned")
	}
	if _, err = store.Review(ctx, second.Comment.ID, 90, second.Comment.Version, true, "", now); err == nil {
		t.Fatal("approved comment reviewed again")
	}
}

func TestGeneratedRepositoryCRUD(t *testing.T) {
	ctx := context.Background()
	repository := NewRepository(storeDB(t))
	comment := &domain.Comment{
		TargetType: commentapp.TargetActivity,
		TargetId:   8,
		AuthorId:   1,
		Content:    "generated repository",
		Status:     domain.StatusPendingReview,
		Version:    1,
	}
	if err := repository.Create(ctx, comment); err != nil {
		t.Fatal(err)
	}
	got, err := repository.Get(ctx, comment.ID)
	if err != nil || got.Content != comment.Content {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	got.Content = "updated"
	if err = repository.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	rows, total, err := repository.List(ctx, 0, 0)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Content != "updated" {
		t.Fatalf("rows=%+v total=%d err=%v", rows, total, err)
	}
	if _, err = repository.Get(ctx, 9999); err == nil {
		t.Fatal("missing generated repository row returned")
	}
}

func approvedRoot(t *testing.T, store *Store, authorID uint64, now time.Time) commentapp.Item {
	t.Helper()
	item, err := store.Create(context.Background(), authorID, commentapp.CreateInput{
		TargetType: commentapp.TargetActivity, TargetID: 8, Content: "root",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	item, err = store.Review(context.Background(), item.Comment.ID, 90, item.Comment.Version, true, "", now)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func storeDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{TranslateError: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&domain.Comment{}, &domain.CommentPin{}, &domainevent.Event{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(
		"CREATE UNIQUE INDEX uk_comment_pin_target ON comment_pins(target_type, target_id)",
	).Error; err != nil {
		t.Fatal(err)
	}
	return db
}
