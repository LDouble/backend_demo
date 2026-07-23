package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
)

type mineStore struct {
	Store
	search domain.MineSearch
	called bool
	err    error
}

func (store *mineStore) ListMine(
	_ context.Context,
	_ uint64,
	search domain.MineSearch,
	_,
	_ int,
) ([]domain.Task, int64, error) {
	store.called = true
	store.search = search
	return []domain.Task{}, 0, store.err
}

func TestManagerListMineNormalizesAndDelegates(t *testing.T) {
	wantErr := errors.New("store failure")
	store := &mineStore{err: wantErr}
	_, _, err := NewManager(store).ListMine(
		context.Background(),
		7,
		domain.MineSearch{
			Relation: " published ", Status: " open ", ReviewStatus: " approved ",
		},
		1,
		20,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ListMine() error = %v, want %v", err, wantErr)
	}
	want := domain.MineSearch{
		Relation: domain.MineRelationPublished,
		Status:   domain.TaskOpen, ReviewStatus: domain.ReviewApproved,
	}
	if store.search != want {
		t.Fatalf("search = %+v, want %+v", store.search, want)
	}
}

func TestManagerListMineRejectsInvalidFilters(t *testing.T) {
	store := &mineStore{}
	_, _, err := NewManager(store).ListMine(
		context.Background(),
		7,
		domain.MineSearch{Relation: "invalid"},
		1,
		20,
	)
	appErr, ok := apperror.As(err)
	if !ok || appErr.Status != 400 || appErr.Code != "invalid_errand_filter" {
		t.Fatalf("ListMine() error = %v", err)
	}
	if store.called {
		t.Fatal("ListMine() called store for invalid filters")
	}
}

func TestManagerViewerContext(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	manager := NewManager(&mineStore{})
	manager.now = func() time.Time { return now }
	task := &domain.Task{
		Status: domain.TaskOpen, ReviewStatus: domain.ReviewApproved,
		RequesterId: 7, Deadline: now.Add(time.Hour),
	}
	relation, actions := manager.ViewerContext(task, 8)
	if relation != domain.ViewerRelationNone {
		t.Fatalf("relation = %q", relation)
	}
	if len(actions) != 1 || actions[0] != domain.ActionAccept {
		t.Fatalf("actions = %v", actions)
	}
}
