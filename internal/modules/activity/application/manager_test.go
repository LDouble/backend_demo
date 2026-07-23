package application

import (
	"context"
	"errors"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
)

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
