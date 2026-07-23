package application

import (
	"context"
	"errors"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
)

type viewerContextStore struct {
	Store
	active map[uint64]bool
	err    error
	ids    []uint64
	viewer uint64
}

func (s *viewerContextStore) ActiveBuyerListings(
	_ context.Context,
	viewerID uint64,
	listingIDs []uint64,
) (map[uint64]bool, error) {
	s.viewer = viewerID
	s.ids = append([]uint64{}, listingIDs...)
	return s.active, s.err
}

func TestViewerContexts(t *testing.T) {
	store := &viewerContextStore{active: map[uint64]bool{2: true}}
	manager := NewManager(store)
	listings := []domain.ListingDetails{
		{Listing: domain.Listing{ID: 1, OwnerId: 7, Status: domain.ListingPublished}},
		{Listing: domain.Listing{ID: 2, OwnerId: 8, Status: domain.ListingReserved}},
	}

	relations, actions, err := manager.ViewerContexts(context.Background(), listings, 9)
	if err != nil {
		t.Fatal(err)
	}
	if store.viewer != 9 || len(store.ids) != 2 || store.ids[0] != 1 || store.ids[1] != 2 {
		t.Fatalf("delegated viewer=%d ids=%v", store.viewer, store.ids)
	}
	if relations[1] != domain.ViewerRelationNone ||
		len(actions[1]) != 1 ||
		actions[1][0] != domain.ActionPurchase {
		t.Fatalf("listing 1 relation=%q actions=%v", relations[1], actions[1])
	}
	if relations[2] != domain.ViewerRelationBuyer || len(actions[2]) != 0 {
		t.Fatalf("listing 2 relation=%q actions=%v", relations[2], actions[2])
	}
}

func TestViewerContextsPreservesStoreError(t *testing.T) {
	want := errors.New("orders unavailable")
	manager := NewManager(&viewerContextStore{err: want})
	relations, actions, err := manager.ViewerContexts(
		context.Background(),
		[]domain.ListingDetails{{Listing: domain.Listing{ID: 1}}},
		9,
	)
	if !errors.Is(err, want) || relations != nil || actions != nil {
		t.Fatalf("relations=%v actions=%v err=%v", relations, actions, err)
	}
}
