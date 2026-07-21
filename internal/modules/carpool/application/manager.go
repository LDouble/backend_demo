// Package application coordinates carpool trip use cases.
package application

import (
	"context"
	"time"

	"github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
)

// Store defines the transactional persistence contract for carpool trips.
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

// Manager validates carpool input before delegating to the store.
type Manager struct {
	store Store
	now   func() time.Time
}

// NewManager creates a carpool use-case manager.
func NewManager(store Store) *Manager { return &Manager{store: store, now: time.Now} }

// Create validates and creates an open trip.
func (m *Manager) Create(ctx context.Context, user uint64, in domain.TripInput) (*domain.Trip, error) {
	now := m.now().UTC()
	if err := domain.ValidateTripInput(in, now); err != nil {
		return nil, err
	}
	return m.store.CreateTrip(ctx, user, in, now)
}

// Get returns a trip and whether the viewer may see full contact details.
func (m *Manager) Get(ctx context.Context, id, viewer uint64) (*domain.Trip, bool, error) {
	return m.store.GetTrip(ctx, id, viewer)
}

// Search returns matching trips for the supplied filters.
func (m *Manager) Search(ctx context.Context, s domain.Search, p, size int) ([]domain.Trip, int64, error) {
	return m.store.SearchTrips(ctx, s, p, size, m.now().UTC())
}

// Join atomically joins a trip.
func (m *Manager) Join(ctx context.Context, id, user, version uint64) (*domain.Trip, error) {
	return m.store.Join(ctx, id, user, version, m.now().UTC())
}

// Leave removes a participant from a trip.
func (m *Manager) Leave(ctx context.Context, id, user, version uint64) (*domain.Trip, error) {
	return m.store.Leave(ctx, id, user, version, m.now().UTC())
}

// Cancel cancels a trip.
func (m *Manager) Cancel(ctx context.Context, id, user, version uint64) (*domain.Trip, error) {
	return m.store.Cancel(ctx, id, user, version, m.now().UTC())
}

// CompleteDue marks due trips as completed.
func (m *Manager) CompleteDue(ctx context.Context) (int64, error) {
	return m.store.CompleteDue(ctx, m.now().UTC())
}

// RevealContact decrypts the raw contact value for internal use.
func (m *Manager) RevealContact(trip *domain.Trip) (string, error) {
	return m.store.RevealContact(trip)
}
