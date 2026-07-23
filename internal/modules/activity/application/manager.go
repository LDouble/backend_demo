// Package application coordinates activity use cases.
package application

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
)

// Store defines the transactional persistence contract for activity use cases.
type Store interface {
	Create(context.Context, uint64, domain.ActivityInput) (*domain.Activity, error)
	Update(context.Context, uint64, uint64, uint64, domain.ActivityInput, time.Time) (*domain.Activity, error)
	GetAdmin(context.Context, uint64) (*domain.Activity, error)
	GetPublic(context.Context, uint64, uint64) (*domain.Activity, error)
	IsViewerRegistered(context.Context, uint64, uint64) (bool, error)
	IsViewerRegisteredBatch(context.Context, uint64, []uint64) (map[uint64]bool, error)
	ListAdmin(context.Context, domain.AdminSearch, int, int) ([]domain.Activity, int64, error)
	ListPublic(context.Context, domain.PublicSearch, int, int) ([]domain.Activity, int64, error)
	SubmitReview(context.Context, uint64, uint64, uint64) (*domain.Activity, error)
	Approve(context.Context, uint64, uint64, uint64, string, time.Time) (*domain.Activity, error)
	Reject(context.Context, uint64, uint64, uint64, string) (*domain.Activity, error)
	Publish(context.Context, uint64, uint64, uint64, time.Time) (*domain.Activity, error)
	Cancel(context.Context, uint64, uint64, uint64, time.Time) (*domain.Activity, error)
	Finish(context.Context, uint64, uint64, uint64, time.Time) (*domain.Activity, error)
	Register(context.Context, uint64, uint64, string, time.Time) (*domain.ActivityRegistration, *domain.Activity, error)
	CancelRegistration(context.Context, uint64, uint64, uint64, time.Time) (*domain.ActivityRegistration, *domain.Activity, error)
	ListMyRegistrations(context.Context, uint64, int, int) ([]domain.MyRegistration, int64, error)
	Contact(context.Context, *domain.Activity, uint64) (domain.ContactDetails, error)
	ContactWithAccess(context.Context, *domain.Activity, uint64, bool) (domain.ContactDetails, error)
}

// Manager validates activity inputs before delegating to the store.
type Manager struct {
	store Store
	now   func() time.Time
}

// NewManager creates an activity use-case manager.
func NewManager(store Store) *Manager { return &Manager{store: store, now: time.Now} }

// Create validates and creates a draft activity owned by the actor.
func (m *Manager) Create(ctx context.Context, actorID uint64, input domain.ActivityInput) (*domain.Activity, error) {
	now := m.now().UTC()
	if err := domain.ValidateActivityInput(input, true, now); err != nil {
		return nil, apperror.Wrap(http.StatusBadRequest, "invalid_activity", err.Error(), err)
	}
	return m.store.Create(ctx, actorID, input)
}

// Update validates and updates an editable activity draft.
func (m *Manager) Update(ctx context.Context, id, actorID, version uint64, input domain.ActivityInput) (*domain.Activity, error) {
	now := m.now().UTC()
	if err := domain.ValidateActivityInput(input, false, now); err != nil {
		return nil, apperror.Wrap(http.StatusBadRequest, "invalid_activity", err.Error(), err)
	}
	return m.store.Update(ctx, id, actorID, version, input, now)
}

// GetAdmin returns an activity without public visibility filtering.
func (m *Manager) GetAdmin(ctx context.Context, id uint64) (*domain.Activity, error) {
	return m.store.GetAdmin(ctx, id)
}

// GetPublic returns an activity reachable by `viewerID` (publicly visible, or
// owned / registered by the viewer even after the activity enters a terminal
// state). viewerID == 0 represents anonymous endpoints that only see the
// public-visibility subset.
func (m *Manager) GetPublic(ctx context.Context, id, viewerID uint64) (*domain.Activity, error) {
	return m.store.GetPublic(ctx, id, viewerID)
}

// IsViewerRegistered reports whether the viewer holds an active registration
// for the activity. Used by handlers that need to render contact visibility
// without doing a per-row lookup in the loop.
func (m *Manager) IsViewerRegistered(ctx context.Context, viewerID, activityID uint64) (bool, error) {
	return m.store.IsViewerRegistered(ctx, viewerID, activityID)
}

// IsViewerRegisteredBatch returns the subset of activityIDs the viewer is
// actively registered for. list/contact handlers should call this once before
// iterating to avoid the N+1 pattern of an inner query per row.
func (m *Manager) IsViewerRegisteredBatch(ctx context.Context, viewerID uint64, activityIDs []uint64) (map[uint64]bool, error) {
	return m.store.IsViewerRegisteredBatch(ctx, viewerID, activityIDs)
}

// ContactWithAccess is the list-path variant of Contact that reuses a
// precomputed `hasActiveRegistration` flag in lieu of issuing a fresh query.
func (m *Manager) ContactWithAccess(ctx context.Context, activity *domain.Activity, viewerID uint64, hasActiveRegistration bool) (domain.ContactDetails, error) {
	return m.store.ContactWithAccess(ctx, activity, viewerID, hasActiveRegistration)
}

// ListAdmin returns admin-visible activities with search filters.
func (m *Manager) ListAdmin(ctx context.Context, search domain.AdminSearch, page, pageSize int) ([]domain.Activity, int64, error) {
	search.Keyword = strings.TrimSpace(search.Keyword)
	return m.store.ListAdmin(ctx, search, page, pageSize)
}

// ListPublic returns published and approved activities with search filters.
func (m *Manager) ListPublic(ctx context.Context, search domain.PublicSearch, page, pageSize int) ([]domain.Activity, int64, error) {
	search.Keyword = strings.TrimSpace(search.Keyword)
	return m.store.ListPublic(ctx, search, page, pageSize)
}

// SubmitReview moves a draft activity into review.
func (m *Manager) SubmitReview(ctx context.Context, id, actorID, version uint64) (*domain.Activity, error) {
	return m.store.SubmitReview(ctx, id, actorID, version)
}

// Approve records an approval decision and publishes the activity atomically.
func (m *Manager) Approve(ctx context.Context, id, actorID, version uint64, comment string) (*domain.Activity, error) {
	return m.store.Approve(ctx, id, actorID, version, comment, m.now().UTC())
}

// Reject records a rejection decision for a pending activity.
func (m *Manager) Reject(ctx context.Context, id, actorID, version uint64, comment string) (*domain.Activity, error) {
	if strings.TrimSpace(comment) == "" {
		err := fmt.Errorf("审核驳回意见不能为空")
		return nil, apperror.Wrap(http.StatusBadRequest, "invalid_activity_review", err.Error(), err)
	}
	return m.store.Reject(ctx, id, actorID, version, comment)
}

// Publish publishes an approved draft activity.
func (m *Manager) Publish(ctx context.Context, id, actorID, version uint64) (*domain.Activity, error) {
	return m.store.Publish(ctx, id, actorID, version, m.now().UTC())
}

// Cancel cancels a published activity.
func (m *Manager) Cancel(ctx context.Context, id, actorID, version uint64) (*domain.Activity, error) {
	return m.store.Cancel(ctx, id, actorID, version, m.now().UTC())
}

// Finish marks a published activity as finished.
func (m *Manager) Finish(ctx context.Context, id, actorID, version uint64) (*domain.Activity, error) {
	return m.store.Finish(ctx, id, actorID, version, m.now().UTC())
}

// Register creates or reactivates a user's registration for an activity.
func (m *Manager) Register(ctx context.Context, activityID, userID uint64, key string) (*domain.ActivityRegistration, *domain.Activity, error) {
	return m.store.Register(ctx, activityID, userID, key, m.now().UTC())
}

// CancelRegistration cancels an active registration before the activity starts.
func (m *Manager) CancelRegistration(ctx context.Context, activityID, userID, version uint64) (*domain.ActivityRegistration, *domain.Activity, error) {
	return m.store.CancelRegistration(ctx, activityID, userID, version, m.now().UTC())
}

// ListMyRegistrations returns the current user's registrations.
func (m *Manager) ListMyRegistrations(ctx context.Context, userID uint64, page, pageSize int) ([]domain.MyRegistration, int64, error) {
	return m.store.ListMyRegistrations(ctx, userID, page, pageSize)
}

// Contact returns a visibility-filtered contact payload for a viewer.
func (m *Manager) Contact(ctx context.Context, activity *domain.Activity, viewerID uint64) (domain.ContactDetails, error) {
	return m.store.Contact(ctx, activity, viewerID)
}
