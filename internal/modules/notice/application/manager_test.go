package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/modules/notice/domain"
	"gorm.io/gorm"
)

type fakeStore struct {
	notice   *domain.Notice
	audience []domain.NoticeAudience
	validErr error
	updateOK bool
	queueOK  bool
	revokeOK bool
	deleted  bool
}

func (f *fakeStore) ValidateAudience(context.Context, domain.Audience) error { return f.validErr }
func (f *fakeStore) Create(_ context.Context, n *domain.Notice, audience []domain.NoticeAudience) error {
	n.ID = 7
	f.notice = n
	f.audience = audience
	return nil
}
func (f *fakeStore) Get(context.Context, uint64) (*domain.Notice, []domain.NoticeAudience, error) {
	if f.notice == nil {
		return nil, nil, gorm.ErrRecordNotFound
	}
	return f.notice, f.audience, nil
}
func (f *fakeStore) ListAdmin(context.Context, int, int) ([]domain.Notice, int64, error) {
	return []domain.Notice{*f.notice}, 1, nil
}
func (f *fakeStore) UpdateDraft(_ context.Context, n *domain.Notice, _ uint64, audience []domain.NoticeAudience) (bool, error) {
	if f.updateOK {
		n.Version++
		f.notice = n
		f.audience = audience
	}
	return f.updateOK, nil
}
func (f *fakeStore) DeleteDraft(context.Context, uint64, uint64) (bool, error) { return f.deleted, nil }
func (f *fakeStore) QueuePublish(_ context.Context, _ uint64, _ uint64, _ uint64, _ time.Time) (bool, error) {
	if f.queueOK {
		f.notice.Status = domain.StatusPublishing
		f.notice.Version++
	}
	return f.queueOK, nil
}
func (f *fakeStore) Revoke(_ context.Context, _ uint64, _ uint64, _ uint64, _ time.Time) (bool, error) {
	if f.revokeOK {
		f.notice.Status = domain.StatusRevoked
		f.notice.Version++
	}
	return f.revokeOK, nil
}
func (f *fakeStore) ListInbox(context.Context, uint64, InboxFilter) ([]domain.Notice, int64, error) {
	return []domain.Notice{*f.notice}, 1, nil
}
func (f *fakeStore) GetInbox(context.Context, uint64, uint64) (*domain.Notice, error) {
	if f.notice == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return f.notice, nil
}
func (f *fakeStore) UnreadCount(context.Context, uint64) (int64, error)            { return 2, nil }
func (f *fakeStore) MarkRead(context.Context, uint64, uint64, time.Time) error     { return nil }
func (f *fakeStore) MarkAllRead(context.Context, uint64, time.Time) (int64, error) { return 2, nil }
func (f *fakeStore) ListDeliveries(context.Context, uint64, int, int) ([]domain.NoticeDelivery, int64, error) {
	return nil, 0, nil
}
func (f *fakeStore) RetryDeliveries(context.Context, uint64, time.Time) (int64, error) { return 1, nil }

func validInput() domain.DraftInput {
	return domain.DraftInput{Title: "停水通知", Summary: "摘要", Body: "正文", Category: "campus", Priority: domain.PriorityImportant, Channels: []string{domain.ChannelInApp, domain.ChannelPush}, Audience: domain.Audience{All: true}}
}

func TestManagerCreateAndValidateAudience(t *testing.T) {
	store := &fakeStore{}
	manager := NewManager(store)
	notice, err := manager.Create(context.Background(), 9, validInput())
	if err != nil || notice.ID != 7 || !notice.PushEnabled || notice.Version != 1 || len(store.audience) != 1 {
		t.Fatalf("Create() notice=%+v audience=%+v err=%v", notice, store.audience, err)
	}
	store.validErr = errors.New("invalid user")
	if _, err = manager.Create(context.Background(), 9, validInput()); err == nil {
		t.Fatal("expected audience validation error")
	}
}

func TestManagerOptimisticLockAndState(t *testing.T) {
	store := &fakeStore{notice: &domain.Notice{ID: 7, Status: domain.StatusDraft, Version: 2}}
	manager := NewManager(store)
	if _, err := manager.Update(context.Background(), 7, 9, 1, validInput()); err == nil {
		t.Fatal("expected version conflict")
	}
	store.notice.Version = 1
	if _, err := manager.Update(context.Background(), 7, 9, 1, validInput()); err == nil {
		t.Fatal("expected invalid state when store rejects matching version")
	}
	store.updateOK = true
	if notice, err := manager.Update(context.Background(), 7, 9, 1, validInput()); err != nil || notice.Version != 2 {
		t.Fatalf("Update() notice=%+v err=%v", notice, err)
	}
}

func TestManagerPublishRevokeAndInbox(t *testing.T) {
	store := &fakeStore{notice: &domain.Notice{ID: 7, Status: domain.StatusDraft, Version: 1}, queueOK: true, revokeOK: true}
	manager := NewManager(store)
	manager.now = func() time.Time { return time.Unix(100, 0).UTC() }
	notice, err := manager.Publish(context.Background(), 7, 9, 1, nil)
	if err != nil || notice.Status != domain.StatusPublishing {
		t.Fatalf("Publish()=%+v, %v", notice, err)
	}
	notice, err = manager.Revoke(context.Background(), 7, 9, 2)
	if err != nil || notice.Status != domain.StatusRevoked {
		t.Fatalf("Revoke()=%+v, %v", notice, err)
	}
	if count, err := manager.UnreadCount(context.Background(), 9); err != nil || count != 2 {
		t.Fatalf("UnreadCount()=%d,%v", count, err)
	}
	if count, err := manager.MarkAllRead(context.Background(), 9); err != nil || count != 2 {
		t.Fatalf("MarkAllRead()=%d,%v", count, err)
	}
}

func TestManagerQueriesDeleteReadAndDeliveries(t *testing.T) {
	store := &fakeStore{notice: &domain.Notice{ID: 7, Status: domain.StatusPublished, Version: 3}, deleted: true}
	manager := NewManager(store)
	if rows, total, err := manager.ListAdmin(context.Background(), 0, 1000); err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("ListAdmin()=%v,%d,%v", rows, total, err)
	}
	if rows, total, err := manager.ListInbox(context.Background(), 9, InboxFilter{Page: 0, PageSize: 0}); err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("ListInbox()=%v,%d,%v", rows, total, err)
	}
	if _, err := manager.GetInbox(context.Background(), 9, 7); err != nil {
		t.Fatal(err)
	}
	if err := manager.MarkRead(context.Background(), 9, 7); err != nil {
		t.Fatal(err)
	}
	if rows, total, err := manager.ListDeliveries(context.Background(), 7, 0, 1000); err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("ListDeliveries()=%v,%d,%v", rows, total, err)
	}
	if count, err := manager.RetryDeliveries(context.Background(), 7); err != nil || count != 1 {
		t.Fatalf("RetryDeliveries()=%d,%v", count, err)
	}
	store.notice.Status = domain.StatusDraft
	if err := manager.Delete(context.Background(), 7, 3); err != nil {
		t.Fatal(err)
	}
	store.notice = nil
	if _, _, err := manager.GetAdmin(context.Background(), 99); err == nil {
		t.Fatal("missing admin notice must fail")
	}
	if _, err := manager.GetInbox(context.Background(), 9, 99); err == nil {
		t.Fatal("missing inbox notice must fail")
	}
}

type generatedRepo struct{ notice *domain.Notice }

func (r *generatedRepo) Create(_ context.Context, notice *domain.Notice) error {
	notice.ID = 11
	r.notice = notice
	return nil
}
func (r *generatedRepo) Get(context.Context, uint64) (*domain.Notice, error) { return r.notice, nil }
func (r *generatedRepo) List(context.Context, int, int) ([]domain.Notice, int64, error) {
	return []domain.Notice{*r.notice}, 1, nil
}
func (r *generatedRepo) Update(_ context.Context, notice *domain.Notice) error {
	r.notice = notice
	return nil
}
func (r *generatedRepo) Delete(context.Context, uint64) error { return nil }

func TestGeneratedServiceDelegatesToRepository(t *testing.T) {
	repo := &generatedRepo{}
	service := NewService(repo)
	notice := &domain.Notice{Title: "generated"}
	if err := service.Create(context.Background(), notice); err != nil {
		t.Fatal(err)
	}
	if got, err := service.Get(context.Background(), notice.ID); err != nil || got.ID != 11 {
		t.Fatalf("Get()=%+v,%v", got, err)
	}
	if rows, total, err := service.List(context.Background(), 1, 20); err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("List()=%+v,%d,%v", rows, total, err)
	}
	if err := service.Update(context.Background(), notice); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), notice.ID); err != nil {
		t.Fatal(err)
	}
}

func TestManagerValidationAndRejectedOperations(t *testing.T) {
	ctx := context.Background()
	store := &fakeStore{notice: &domain.Notice{ID: 7, Status: domain.StatusDraft, Version: 4}}
	manager := NewManager(store)
	invalid := validInput()
	invalid.Title = ""
	if _, err := manager.Create(ctx, 1, invalid); err == nil {
		t.Fatal("invalid draft must fail")
	}
	if _, err := manager.Update(ctx, 7, 1, 0, validInput()); err == nil {
		t.Fatal("zero expected version must fail")
	}
	input := validInput()
	input.ActionPath = "/pages/notices/detail"
	input.Audience = domain.Audience{Roles: []string{"member"}, UserIDs: []uint64{3, 2}}
	if _, err := manager.Create(ctx, 1, input); err != nil {
		t.Fatal(err)
	}
	store.notice = &domain.Notice{ID: 7, Status: domain.StatusDraft, Version: 4}
	if err := manager.Delete(ctx, 7, 3); err == nil {
		t.Fatal("stale delete must fail")
	}
	if _, err := manager.Publish(ctx, 7, 1, 4, nil); err == nil {
		t.Fatal("rejected publish must fail")
	}
	if _, err := manager.Revoke(ctx, 7, 1, 4); err == nil {
		t.Fatal("rejected revoke must fail")
	}
	store.notice = nil
	if err := manager.MarkRead(ctx, 1, 99); err == nil {
		t.Fatal("missing read must fail")
	}
	if _, _, err := manager.ListDeliveries(ctx, 99, 1, 20); err == nil {
		t.Fatal("missing delivery notice must fail")
	}
	if _, err := manager.RetryDeliveries(ctx, 99); err == nil {
		t.Fatal("missing retry notice must fail")
	}
}
