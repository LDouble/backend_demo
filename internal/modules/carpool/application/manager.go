package application

import (
	"context"
	"github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
	"time"
)

type Store interface {
	CreateTrip(context.Context, uint64, domain.TripInput, time.Time) (*domain.Trip, error)
	GetTrip(context.Context, uint64, uint64) (*domain.Trip, bool, error)
	SearchTrips(context.Context, domain.Search, int, int, time.Time) ([]domain.Trip, int64, error)
	Join(context.Context, uint64, uint64, uint64, time.Time) (*domain.Trip, error)
	Leave(context.Context, uint64, uint64, uint64, time.Time) (*domain.Trip, error)
	Cancel(context.Context, uint64, uint64, uint64, time.Time) (*domain.Trip, error)
	CompleteDue(context.Context, time.Time) (int64, error)
	RevealContact(*domain.Trip) (string, error)
}
type Manager struct {
	store Store
	now   func() time.Time
}

func NewManager(store Store) *Manager { return &Manager{store: store, now: time.Now} }
func (m *Manager) Create(ctx context.Context, user uint64, in domain.TripInput) (*domain.Trip, error) {
	now := m.now().UTC()
	if err := domain.ValidateTripInput(in, now); err != nil {
		return nil, err
	}
	return m.store.CreateTrip(ctx, user, in, now)
}
func (m *Manager) Get(ctx context.Context, id, viewer uint64) (*domain.Trip, bool, error) {
	return m.store.GetTrip(ctx, id, viewer)
}
func (m *Manager) Search(ctx context.Context, s domain.Search, p, size int) ([]domain.Trip, int64, error) {
	return m.store.SearchTrips(ctx, s, p, size, m.now().UTC())
}
func (m *Manager) Join(ctx context.Context, id, user, version uint64) (*domain.Trip, error) {
	return m.store.Join(ctx, id, user, version, m.now().UTC())
}
func (m *Manager) Leave(ctx context.Context, id, user, version uint64) (*domain.Trip, error) {
	return m.store.Leave(ctx, id, user, version, m.now().UTC())
}
func (m *Manager) Cancel(ctx context.Context, id, user, version uint64) (*domain.Trip, error) {
	return m.store.Cancel(ctx, id, user, version, m.now().UTC())
}
func (m *Manager) CompleteDue(ctx context.Context) (int64, error) {
	return m.store.CompleteDue(ctx, m.now().UTC())
}
func (m *Manager) RevealContact(trip *domain.Trip) (string, error) {
	return m.store.RevealContact(trip)
}
