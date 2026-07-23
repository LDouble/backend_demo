package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
)

type publishingStore struct {
	Store
	calls []string
	now   time.Time
}

func (s *publishingStore) Create(context.Context, uint64, domain.ActivityInput) (*domain.Activity, error) {
	s.calls = append(s.calls, "create")
	return &domain.Activity{ID: 1}, nil
}

func (s *publishingStore) Update(
	_ context.Context,
	_,
	_,
	_ uint64,
	_ domain.ActivityInput,
	now time.Time,
) (*domain.Activity, error) {
	s.calls = append(s.calls, "update")
	s.now = now
	return &domain.Activity{ID: 1}, nil
}

func (s *publishingStore) SubmitReview(context.Context, uint64, uint64, uint64) (*domain.Activity, error) {
	s.calls = append(s.calls, "submit")
	return &domain.Activity{ID: 1}, nil
}

func (s *publishingStore) Cancel(_ context.Context, _, _, _ uint64, now time.Time) (*domain.Activity, error) {
	s.calls = append(s.calls, "cancel")
	s.now = now
	return &domain.Activity{ID: 1}, nil
}

type listMineStore struct {
	Store
	actorID uint64
	search  domain.AdminSearch
	page    int
	size    int
	err     error
}

func (s *listMineStore) ListMine(_ context.Context, actorID uint64, search domain.AdminSearch, page, size int) ([]domain.Activity, int64, error) {
	s.actorID, s.search, s.page, s.size = actorID, search, page, size
	return []domain.Activity{{ID: 11}}, 1, s.err
}

func TestListMineNormalizesKeywordAndDelegates(t *testing.T) {
	store := &listMineStore{}
	rows, total, err := NewManager(store).ListMine(
		context.Background(),
		7,
		domain.AdminSearch{Keyword: "  迎新活动  "},
		2,
		10,
	)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != 11 {
		t.Fatalf("rows=%+v total=%d err=%v", rows, total, err)
	}
	if store.actorID != 7 || store.search.Keyword != "迎新活动" || store.page != 2 || store.size != 10 {
		t.Fatalf("delegated actor=%d search=%+v page=%d size=%d", store.actorID, store.search, store.page, store.size)
	}

	wantErr := errors.New("store unavailable")
	store.err = wantErr
	if _, _, err = NewManager(store).ListMine(context.Background(), 7, domain.AdminSearch{}, 1, 20); !errors.Is(err, wantErr) {
		t.Fatalf("error=%v want=%v", err, wantErr)
	}
}

func TestPublishingLifecycleValidatesAndDelegates(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	valid := domain.ActivityInput{
		Title: "迎新活动", Summary: "活动摘要", Body: "活动正文", Location: "体育馆",
		SignupStartAt: now.Add(time.Hour), SignupEndAt: now.Add(2 * time.Hour),
		StartAt: now.Add(3 * time.Hour), EndAt: now.Add(4 * time.Hour), Capacity: 20,
		Contact: domain.ContactInput{Type: "wechat", Value: "activity_owner", Provided: true},
	}
	store := &publishingStore{}
	manager := NewManager(store)
	manager.now = func() time.Time { return now }
	ctx := context.Background()

	if _, err := manager.Create(ctx, 7, domain.ActivityInput{}); err == nil {
		t.Fatal("Create() invalid error=nil")
	}
	if _, err := manager.Create(ctx, 7, valid); err != nil {
		t.Fatal(err)
	}
	invalidUpdate := valid
	invalidUpdate.Capacity = 0
	if _, err := manager.Update(
		ctx,
		1,
		7,
		1,
		invalidUpdate,
	); err == nil {
		t.Fatal("Update() invalid error=nil")
	}
	valid.Contact = domain.ContactInput{}
	if _, err := manager.Update(
		ctx,
		1,
		7,
		1,
		valid,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SubmitReview(
		ctx,
		1,
		7,
		2,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Cancel(
		ctx,
		1,
		7,
		3,
	); err != nil {
		t.Fatal(err)
	}
	if got := len(store.calls); got != 4 {
		t.Fatalf("calls=%v", store.calls)
	}
	if !store.now.Equal(now) {
		t.Fatalf("delegated now=%v want=%v", store.now, now)
	}
}
