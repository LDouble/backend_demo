package application_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	commentapp "github.com/weouc-plus/campus-platform/internal/modules/comment/application"
	commentdomain "github.com/weouc-plus/campus-platform/internal/modules/comment/domain"
	commentinfra "github.com/weouc-plus/campus-platform/internal/modules/comment/infrastructure"
	"gorm.io/gorm"
)

type targetResolver struct {
	ownerID uint64
	err     error
	calls   *int
}

func (r targetResolver) Resolve(
	context.Context,
	string,
	uint64,
	uint64,
) (commentapp.Target, error) {
	if r.calls != nil {
		*r.calls++
	}
	if r.err != nil {
		return commentapp.Target{}, r.err
	}
	return commentapp.Target{OwnerID: r.ownerID}, nil
}

type threadStore struct {
	commentapp.Store
	root        commentapp.Item
	descendants []commentapp.Item
}

func (s threadStore) Thread(
	context.Context,
	uint64,
	uint64,
	bool,
) (commentapp.Item, []commentapp.Item, error) {
	return s.root, s.descendants, nil
}

func TestManagerAdminThreadSkipsTargetResolution(t *testing.T) {
	rootID := uint64(1)
	root := commentapp.Item{Comment: commentdomain.Comment{
		ID: 1, RootId: &rootID, TargetType: commentapp.TargetCampusCirclePost,
		TargetId: 9, Status: commentdomain.StatusPendingReview,
	}}
	descendants := []commentapp.Item{{Comment: commentdomain.Comment{
		ID: 2, ParentId: &rootID, RootId: &rootID,
		TargetType: commentapp.TargetCampusCirclePost, TargetId: 9,
		Status: commentdomain.StatusRejected,
	}}}
	calls := 0
	manager := commentapp.NewManager(
		threadStore{root: root, descendants: descendants},
		targetResolver{err: errors.New("hidden target"), calls: &calls},
	)

	thread, err := manager.Thread(context.Background(), rootID, 99, true)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("target resolver calls = %d, want 0", calls)
	}
	if thread.Root.Comment.ID != rootID ||
		len(thread.Descendants) != 1 ||
		thread.Descendants[0].Comment.ID != 2 {
		t.Fatalf("thread = %+v", thread)
	}
}

func TestManagerAcceptsSupportedTargetTypes(t *testing.T) {
	targetTypes := []string{
		commentapp.TargetActivity,
		commentapp.TargetMarketplace,
		commentapp.TargetErrand,
		commentapp.TargetCarpool,
		commentapp.TargetCampusCirclePost,
	}
	for _, targetType := range targetTypes {
		t.Run(targetType, func(t *testing.T) {
			manager := commentapp.NewManager(
				commentinfra.NewStore(commentDB(t)),
				targetResolver{ownerID: 99},
			)
			item, err := manager.Create(context.Background(), 1, commentapp.CreateInput{
				TargetType: targetType,
				TargetID:   1,
				Content:    "comment",
			})
			if err != nil {
				t.Fatal(err)
			}
			if item.Comment.TargetType != targetType {
				t.Fatalf("target type = %q, want %q", item.Comment.TargetType, targetType)
			}
		})
	}
}

func TestManagerModeratedNestedLifecycle(t *testing.T) {
	ctx := context.Background()
	db := commentDB(t)
	manager := commentapp.NewManager(commentinfra.NewStore(db), targetResolver{ownerID: 99})

	assertErrorCode(t, create(manager, 1, "unknown", 1, nil, "hello"), "unsupported_comment_target")
	assertErrorCode(t, create(manager, 1, commentapp.TargetActivity, 0, nil, "hello"), "invalid_comment_target")
	assertErrorCode(t, create(manager, 1, commentapp.TargetActivity, 1, nil, " "), "invalid_comment")
	assertErrorCode(
		t,
		create(manager, 1, commentapp.TargetActivity, 1, nil, strings.Repeat("界", 2001)),
		"invalid_comment",
	)

	root, err := manager.Create(ctx, 1, commentapp.CreateInput{
		TargetType: " ACTIVITY ", TargetID: 1, Content: " root ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if root.Comment.Status != commentdomain.StatusPendingReview ||
		root.Comment.Content != "root" ||
		root.Comment.RootId == nil ||
		*root.Comment.RootId != root.Comment.ID ||
		root.TargetOwnerID != 99 {
		t.Fatalf("unexpected root: %+v", root)
	}

	public, total, err := manager.ListRoots(ctx, commentapp.TargetActivity, 1, 2, 0, 0)
	if err != nil || total != 0 || len(public) != 0 {
		t.Fatalf("pending public list: rows=%v total=%d err=%v", public, total, err)
	}
	owned, total, err := manager.ListRoots(ctx, commentapp.TargetActivity, 1, 1, 1, 20)
	if err != nil || total != 1 || len(owned) != 1 {
		t.Fatalf("author list: rows=%v total=%d err=%v", owned, total, err)
	}
	if _, err = manager.Thread(ctx, root.Comment.ID, 2, false); err == nil {
		t.Fatal("pending root leaked to another viewer")
	}
	if _, err = manager.Thread(ctx, root.Comment.ID, 1, false); err != nil {
		t.Fatalf("author thread: %v", err)
	}

	_, err = manager.Review(ctx, root.Comment.ID, 500, 1, false, " ")
	assertCode(t, err, "comment_review_reason_required")
	root, err = manager.Review(ctx, root.Comment.ID, 500, 1, true, "")
	if err != nil || root.Comment.Status != commentdomain.StatusApproved || root.Comment.Version != 2 {
		t.Fatalf("approve root: %+v err=%v", root, err)
	}
	_, err = manager.Review(ctx, root.Comment.ID, 500, 2, true, "")
	assertCode(t, err, "invalid_comment_state")

	child, err := manager.Create(ctx, 2, commentapp.CreateInput{
		TargetType: commentapp.TargetActivity,
		TargetID:   1,
		ParentID:   &root.Comment.ID,
		Content:    "child",
	})
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := manager.Create(ctx, 2, commentapp.CreateInput{
		TargetType: commentapp.TargetActivity,
		TargetID:   1,
		ParentID:   &child.Comment.ID,
		Content:    "grandchild",
	})
	if err != nil || grandchild.Comment.Depth != 2 {
		t.Fatalf("author reply to pending: %+v err=%v", grandchild, err)
	}
	_, err = manager.Create(ctx, 3, commentapp.CreateInput{
		TargetType: commentapp.TargetActivity,
		TargetID:   1,
		ParentID:   &child.Comment.ID,
		Content:    "hidden parent reply",
	})
	assertCode(t, err, "comment_not_found")
	_, err = manager.Create(ctx, 2, commentapp.CreateInput{
		TargetType: commentapp.TargetCarpool,
		TargetID:   1,
		ParentID:   &root.Comment.ID,
		Content:    "wrong target",
	})
	assertCode(t, err, "comment_parent_target_mismatch")

	child = mustReview(t, manager, child, true, "")
	grandchild = mustReview(t, manager, grandchild, true, "")
	sibling, err := manager.Create(ctx, 3, commentapp.CreateInput{
		TargetType: commentapp.TargetActivity,
		TargetID:   1,
		ParentID:   &root.Comment.ID,
		Content:    "sibling",
	})
	if err != nil {
		t.Fatal(err)
	}
	sibling = mustReview(t, manager, sibling, true, "")

	thread, err := manager.Thread(ctx, root.Comment.ID, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	gotOrder := []uint64{}
	for _, item := range thread.Descendants {
		gotOrder = append(gotOrder, item.Comment.ID)
	}
	wantOrder := []uint64{child.Comment.ID, grandchild.Comment.ID, sibling.Comment.ID}
	if fmt.Sprint(gotOrder) != fmt.Sprint(wantOrder) {
		t.Fatalf("depth-first order=%v want=%v", gotOrder, wantOrder)
	}
	public, total, err = manager.ListRoots(ctx, commentapp.TargetActivity, 1, 4, 1, 20)
	if err != nil || total != 1 || public[0].ReplyCount != 3 {
		t.Fatalf("approved root list: %+v total=%d err=%v", public, total, err)
	}

	_, err = manager.Pin(ctx, root.Comment.ID, 4, root.Comment.Version)
	assertCode(t, err, "not_comment_target_owner")
	root, err = manager.Pin(ctx, root.Comment.ID, 99, root.Comment.Version)
	if err != nil || !root.Pinned {
		t.Fatalf("pin root: %+v err=%v", root, err)
	}
	_, err = manager.Pin(ctx, child.Comment.ID, 99, child.Comment.Version)
	assertCode(t, err, "comment_not_pinnable")

	second, err := manager.Create(ctx, 5, commentapp.CreateInput{
		TargetType: commentapp.TargetActivity, TargetID: 1, Content: "second root",
	})
	if err != nil {
		t.Fatal(err)
	}
	second = mustReview(t, manager, second, true, "")
	second, err = manager.Pin(ctx, second.Comment.ID, 99, second.Comment.Version)
	if err != nil || !second.Pinned {
		t.Fatalf("replace pin: %+v err=%v", second, err)
	}
	root, err = commentinfra.NewStore(db).Get(ctx, root.Comment.ID, 1, false)
	if err != nil || root.Pinned {
		t.Fatalf("old root remained pinned: %+v err=%v", root, err)
	}
	_, err = manager.Unpin(ctx, root.Comment.ID, 99, root.Comment.Version)
	assertCode(t, err, "comment_not_pinned")
	second, err = manager.Unpin(ctx, second.Comment.ID, 99, second.Comment.Version)
	if err != nil || second.Pinned {
		t.Fatalf("unpin: %+v err=%v", second, err)
	}

	_, err = manager.Update(ctx, root.Comment.ID, 2, root.Comment.Version, "not mine")
	assertCode(t, err, "not_comment_author")
	_, err = manager.Update(ctx, root.Comment.ID, 1, 999, "stale")
	assertCode(t, err, "version_conflict")
	root, err = manager.Update(ctx, root.Comment.ID, 1, root.Comment.Version, " edited ")
	if err != nil || root.Comment.Status != commentdomain.StatusPendingReview ||
		root.Comment.Content != "edited" {
		t.Fatalf("update: %+v err=%v", root, err)
	}
	root = mustReview(t, manager, root, true, "")
	_, err = manager.RevokeReview(ctx, root.Comment.ID, 500, root.Comment.Version, " ")
	assertCode(t, err, "comment_review_reason_required")
	root, err = manager.RevokeReview(ctx, root.Comment.ID, 500, root.Comment.Version, "needs another look")
	if err != nil || root.Comment.Status != commentdomain.StatusPendingReview {
		t.Fatalf("revoke review: %+v err=%v", root, err)
	}
	root = mustReview(t, manager, root, false, "rejected")
	root, err = manager.SubmitReview(ctx, root.Comment.ID, 1, root.Comment.Version)
	if err != nil || root.Comment.Status != commentdomain.StatusPendingReview ||
		root.Comment.ReviewReason != nil {
		t.Fatalf("submit review: %+v err=%v", root, err)
	}
	_, err = manager.SubmitReview(ctx, root.Comment.ID, 1, root.Comment.Version)
	assertCode(t, err, "invalid_comment_state")
	root, err = manager.Withdraw(ctx, root.Comment.ID, 1, root.Comment.Version)
	if err != nil || root.Comment.Status != commentdomain.StatusWithdrawn {
		t.Fatalf("withdraw: %+v err=%v", root, err)
	}
	_, err = manager.Withdraw(ctx, root.Comment.ID, 1, root.Comment.Version)
	assertCode(t, err, "invalid_comment_state")

	mine, total, err := manager.ListMine(ctx, 1, commentdomain.StatusWithdrawn, 0, 101)
	if err != nil || total != 1 || len(mine) != 1 {
		t.Fatalf("list mine: rows=%v total=%d err=%v", mine, total, err)
	}
	if _, _, err = manager.ListMine(ctx, 1, "bad", 1, 20); err == nil {
		t.Fatal("invalid mine status accepted")
	}
	adminRows, total, err := manager.ListAdmin(ctx, commentapp.Search{
		TargetType: " activity ",
		TargetID:   1,
		Status:     commentdomain.StatusApproved,
		Keyword:    "child",
	}, 1, 20)
	if err != nil || total != 2 || len(adminRows) != 2 {
		t.Fatalf("admin filters: rows=%v total=%d err=%v", adminRows, total, err)
	}
	if _, _, err = manager.ListAdmin(ctx, commentapp.Search{Status: "bad"}, 1, 20); err == nil {
		t.Fatal("invalid admin status accepted")
	}

	relation, actions, err := manager.ViewerContext(ctx, &second, 99, false)
	if err != nil || relation != "target_owner" ||
		!contains(actions, commentdomain.ActionPin) {
		t.Fatalf("target owner context: %s %v %v", relation, actions, err)
	}
	relation, actions, err = manager.ViewerContext(ctx, &second, 5, false)
	if err != nil || relation != "author" ||
		!contains(actions, commentdomain.ActionEdit) {
		t.Fatalf("author context: %s %v %v", relation, actions, err)
	}
	relation, _, err = manager.ViewerContext(ctx, &second, 0, false)
	if err != nil || relation != "guest" {
		t.Fatalf("guest context: %s %v", relation, err)
	}
	relation, _, err = manager.ViewerContext(ctx, &second, 500, true)
	if err != nil || relation != "admin" {
		t.Fatalf("admin context: %s %v", relation, err)
	}
	relation, actions, err = manager.ViewerContext(ctx, nil, 0, false)
	if err != nil || relation != "guest" || len(actions) != 0 {
		t.Fatalf("nil context: %s %v %v", relation, actions, err)
	}
}

func TestManagerDepthAndResolverFailures(t *testing.T) {
	ctx := context.Background()
	db := commentDB(t)
	manager := commentapp.NewManager(commentinfra.NewStore(db), targetResolver{ownerID: 1})
	parentID := uint64(0)
	for depth := int64(0); depth <= commentdomain.MaxDepth; depth++ {
		comment := commentdomain.Comment{
			TargetType: commentapp.TargetErrand,
			TargetId:   7,
			AuthorId:   1,
			Depth:      depth,
			Content:    "chain",
			Status:     commentdomain.StatusApproved,
			Version:    1,
		}
		if parentID != 0 {
			comment.ParentId = &parentID
			rootID := uint64(1)
			comment.RootId = &rootID
		}
		if err := db.Create(&comment).Error; err != nil {
			t.Fatal(err)
		}
		if comment.RootId == nil {
			comment.RootId = &comment.ID
			if err := db.Save(&comment).Error; err != nil {
				t.Fatal(err)
			}
		}
		parentID = comment.ID
	}
	_, err := manager.Create(ctx, 1, commentapp.CreateInput{
		TargetType: commentapp.TargetErrand,
		TargetID:   7,
		ParentID:   &parentID,
		Content:    "too deep",
	})
	assertCode(t, err, "comment_depth_exceeded")

	targetErr := errors.New("target unavailable")
	failing := commentapp.NewManager(commentinfra.NewStore(db), targetResolver{err: targetErr})
	_, err = failing.Create(ctx, 1, commentapp.CreateInput{
		TargetType: commentapp.TargetErrand, TargetID: 7, Content: "hello",
	})
	if !errors.Is(err, targetErr) {
		t.Fatalf("create resolver error=%v", err)
	}
	if _, _, err = failing.ListRoots(ctx, commentapp.TargetErrand, 7, 1, 1, 20); !errors.Is(err, targetErr) {
		t.Fatalf("list resolver error=%v", err)
	}
}

func TestGeneratedServiceCRUD(t *testing.T) {
	ctx := context.Background()
	service := commentapp.NewService(commentinfra.NewRepository(commentDB(t)))
	comment := &commentdomain.Comment{
		TargetType: commentapp.TargetActivity,
		TargetId:   3,
		AuthorId:   2,
		Content:    "generated service",
		Status:     commentdomain.StatusPendingReview,
		Version:    1,
	}
	if err := service.Create(ctx, comment); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, comment.ID)
	if err != nil || got.Content != comment.Content {
		t.Fatalf("get=%+v err=%v", got, err)
	}
	got.Content = "updated"
	if err = service.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	rows, total, err := service.List(ctx, 1, 20)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].Content != "updated" {
		t.Fatalf("rows=%+v total=%d err=%v", rows, total, err)
	}
}

func commentDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{TranslateError: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(
		&commentdomain.Comment{},
		&commentdomain.CommentPin{},
		&domainevent.Event{},
	); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(
		"CREATE UNIQUE INDEX uk_comment_pin_target ON comment_pins(target_type, target_id)",
	).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func create(
	manager *commentapp.Manager,
	authorID uint64,
	targetType string,
	targetID uint64,
	parentID *uint64,
	content string,
) error {
	_, err := manager.Create(context.Background(), authorID, commentapp.CreateInput{
		TargetType: targetType,
		TargetID:   targetID,
		ParentID:   parentID,
		Content:    content,
	})
	return err
}

func mustReview(
	t *testing.T,
	manager *commentapp.Manager,
	item commentapp.Item,
	approved bool,
	reason string,
) commentapp.Item {
	t.Helper()
	result, err := manager.Review(
		context.Background(),
		item.Comment.ID,
		500,
		item.Comment.Version,
		approved,
		reason,
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s", code)
	}
	assertCode(t, err, code)
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error=%v want code=%s", err, code)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
