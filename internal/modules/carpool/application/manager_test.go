package application

import (
	"context"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
)

type managerStore struct {
	calls []string
	trip  *domain.Trip
}

func (s *managerStore) called(name string) *domain.Trip {
	s.calls = append(s.calls, name)
	return s.trip
}
func (s *managerStore) CreateTrip(context.Context, uint64, domain.TripInput, time.Time) (*domain.Trip, error) {
	return s.called("create"), nil
}
func (s *managerStore) UpdateTrip(context.Context, uint64, uint64, uint64, domain.TripInput, time.Time) (*domain.Trip, error) {
	return s.called("update"), nil
}
func (s *managerStore) GetTrip(context.Context, uint64, uint64) (*domain.Trip, bool, error) {
	return s.called("get"), true, nil
}
func (s *managerStore) SearchTrips(context.Context, domain.Search, int, int, time.Time) ([]domain.Trip, int64, error) {
	s.called("search")
	return []domain.Trip{*s.trip}, 1, nil
}
func (s *managerStore) ListAdmin(context.Context, domain.AdminSearch, int, int) ([]domain.Trip, int64, error) {
	s.called("list-admin")
	return []domain.Trip{*s.trip}, 1, nil
}
func (s *managerStore) SubmitReview(context.Context, uint64, uint64, uint64) (*domain.Trip, error) {
	return s.called("submit-review"), nil
}
func (s *managerStore) Review(context.Context, uint64, uint64, uint64, bool, string, time.Time) (*domain.Trip, error) {
	return s.called("review"), nil
}
func (s *managerStore) RevokeReview(context.Context, uint64, uint64, uint64, string, time.Time) (*domain.Trip, error) {
	return s.called("revoke-review"), nil
}
func (s *managerStore) Join(context.Context, uint64, uint64, uint64, time.Time) (*domain.Trip, error) {
	return s.called("join"), nil
}
func (s *managerStore) Leave(context.Context, uint64, uint64, uint64, time.Time) (*domain.Trip, error) {
	return s.called("leave"), nil
}
func (s *managerStore) Cancel(context.Context, uint64, uint64, uint64, time.Time) (*domain.Trip, error) {
	return s.called("cancel"), nil
}
func (s *managerStore) CompleteDue(context.Context, time.Time) (int64, error) {
	s.called("complete-due")
	return 1, nil
}
func (s *managerStore) RevealContact(*domain.Trip) (string, error) {
	s.called("reveal-contact")
	return "contact", nil
}

func TestManagerValidatesAndDelegatesEveryOperation(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	store := &managerStore{trip: &domain.Trip{ID: 1}}
	manager := NewManager(store)
	manager.now = func() time.Time { return now }
	ctx := context.Background()
	valid := domain.TripInput{
		Title:           "东门到高铁站",
		Origin:          "东门",
		Destination:     "高铁站",
		DepartureAt:     now.Add(time.Hour),
		TotalSeats:      3,
		ContactType:     "wechat",
		Contact:         "carpool_test",
		ContactProvided: true,
	}

	if _, err := manager.Create(ctx, 7, domain.TripInput{}); err == nil {
		t.Fatal("Create() invalid error=nil")
	}
	if _, err := manager.Update(ctx, 1, 7, 1, domain.TripInput{}); err == nil {
		t.Fatal("Update() invalid error=nil")
	}
	if _, err := manager.Create(ctx, 7, valid); err != nil {
		t.Fatal(err)
	}
	valid.Contact = ""
	valid.ContactType = ""
	valid.ContactProvided = false
	if _, err := manager.Update(ctx, 1, 7, 1, valid); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Get(ctx, 1, 7); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Search(ctx, domain.Search{}, 1, 20); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ListAdmin(ctx, domain.AdminSearch{}, 1, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SubmitReview(ctx, 1, 7, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Review(ctx, 1, 8, 1, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RevokeReview(ctx, 1, 8, 1, "复核"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Join(ctx, 1, 9, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Leave(ctx, 1, 9, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Cancel(ctx, 1, 7, 1); err != nil {
		t.Fatal(err)
	}
	if count, err := manager.CompleteDue(ctx); err != nil || count != 1 {
		t.Fatalf("CompleteDue() count=%d err=%v", count, err)
	}
	if contact, err := manager.RevealContact(store.trip); err != nil || contact != "contact" {
		t.Fatalf("RevealContact() contact=%q err=%v", contact, err)
	}
	if len(store.calls) != 13 {
		t.Fatalf("delegated calls=%v", store.calls)
	}
}
