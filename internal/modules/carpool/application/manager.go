// Package application coordinates carpool trip use cases.
package application

import (
	"context"
	"net/http"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
)

// Store defines the transactional persistence contract for carpool trips.
type Store interface {
	CreateTrip(context.Context, uint64, domain.TripInput, time.Time) (*domain.Trip, error)
	UpdateTrip(context.Context, uint64, uint64, uint64, domain.TripInput, time.Time) (*domain.Trip, error)
	GetTrip(context.Context, uint64, uint64) (*domain.Trip, bool, error)
	SearchTrips(context.Context, domain.Search, int, int, time.Time) ([]domain.Trip, int64, error)
	ListAdmin(context.Context, domain.AdminSearch, int, int) ([]domain.Trip, int64, error)
	ListMine(context.Context, uint64, domain.AdminSearch, int, int) ([]domain.Trip, int64, error)
	SubmitReview(context.Context, uint64, uint64, uint64) (*domain.Trip, error)
	Review(context.Context, uint64, uint64, uint64, bool, string, time.Time) (*domain.Trip, error)
	RevokeReview(context.Context, uint64, uint64, uint64, string, time.Time) (*domain.Trip, error)
	Join(context.Context, uint64, uint64, uint64, time.Time) (*domain.Trip, error)
	Leave(context.Context, uint64, uint64, uint64, time.Time) (*domain.Trip, error)
	Cancel(context.Context, uint64, uint64, uint64, time.Time) (*domain.Trip, error)
	CompleteDue(context.Context, time.Time) (int64, error)
	RevealContact(*domain.Trip) (string, error)
}

// Update validates and updates an unoccupied trip.
func (m *Manager) Update(ctx context.Context, id, user, version uint64, in domain.TripInput) (*domain.Trip, error) {
	now := m.now().UTC()
	if err := domain.ValidateTripUpdateInput(in, now); err != nil {
		return nil, apperror.Wrap(http.StatusBadRequest, "invalid_carpool_trip", err.Error(), err)
	}
	return m.store.UpdateTrip(ctx, id, user, version, in, now)
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
		return nil, apperror.Wrap(http.StatusBadRequest, "invalid_carpool_trip", err.Error(), err)
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

// ListAdmin returns trips matching moderation filters.
func (m *Manager) ListAdmin(ctx context.Context, search domain.AdminSearch, page, size int) ([]domain.Trip, int64, error) {
	return m.store.ListAdmin(ctx, search, page, size)
}

// ListMine returns all trips organized by the current user, including trips
// that are not publicly visible yet.
func (m *Manager) ListMine(ctx context.Context, userID uint64, search domain.AdminSearch, page, size int) ([]domain.Trip, int64, error) {
	return m.store.ListMine(ctx, userID, search, page, size)
}

// SubmitReview resubmits an edited trip for moderation.
func (m *Manager) SubmitReview(ctx context.Context, id, user, version uint64) (*domain.Trip, error) {
	return m.store.SubmitReview(ctx, id, user, version)
}

// Review records an administrator moderation decision.
func (m *Manager) Review(ctx context.Context, id, adminID, version uint64, approved bool, reason string) (*domain.Trip, error) {
	return m.store.Review(ctx, id, adminID, version, approved, reason, m.now().UTC())
}

// RevokeReview hides an approved, unoccupied trip and returns it to moderation.
func (m *Manager) RevokeReview(ctx context.Context, id, adminID, version uint64, reason string) (*domain.Trip, error) {
	return m.store.RevokeReview(ctx, id, adminID, version, reason, m.now().UTC())
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
