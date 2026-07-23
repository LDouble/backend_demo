// Package application coordinates errand task use cases.
package application

import (
	"context"
	"net/http"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
)

// Store defines the transactional persistence contract for errand tasks.
type Store interface {
	Create(context.Context, uint64, domain.TaskInput) (*domain.Task, error)
	Update(context.Context, uint64, uint64, uint64, domain.TaskInput, time.Time) (*domain.Task, error)
	GetVisible(context.Context, uint64, uint64) (*domain.Task, error)
	ListOpen(context.Context, int, int, time.Time) ([]domain.Task, int64, error)
	ListMine(context.Context, uint64, domain.MineSearch, int, int) ([]domain.Task, int64, error)
	ListAdmin(context.Context, domain.AdminSearch, int, int) ([]domain.Task, int64, error)
	SubmitReview(context.Context, uint64, uint64, uint64) (*domain.Task, error)
	Review(context.Context, uint64, uint64, uint64, bool, string, time.Time) (*domain.Task, error)
	RevokeReview(context.Context, uint64, uint64, uint64, string, time.Time) (*domain.Task, error)
	Accept(context.Context, uint64, uint64, uint64, string, time.Time) (*domain.Task, *tradedomain.Order, error)
	Pickup(context.Context, uint64, uint64, uint64, time.Time) (*domain.Task, error)
	Deliver(context.Context, uint64, uint64, uint64, time.Time) (*domain.Task, error)
	Complete(context.Context, uint64, uint64, uint64, time.Time) (*domain.Task, *tradedomain.Order, error)
	Cancel(context.Context, uint64, uint64, uint64, time.Time) (*domain.Task, *tradedomain.Order, error)
	CompleteOrder(context.Context, uint64, uint64, uint64, time.Time) (*tradedomain.Order, error)
	CancelOrder(context.Context, uint64, uint64, uint64, time.Time) (*tradedomain.Order, error)
	Contact(context.Context, *domain.Task, uint64) (domain.ContactDetails, error)
}

// Contact returns a task contact only when the store confirms the viewer is active.
func (m *Manager) Contact(ctx context.Context, task *domain.Task, viewerID uint64) (domain.ContactDetails, error) {
	return m.store.Contact(ctx, task, viewerID)
}

// Manager validates errand input before delegating to the store.
type Manager struct {
	store Store
	now   func() time.Time
}

// NewManager creates an errand use-case manager.
func NewManager(store Store) *Manager { return &Manager{store: store, now: time.Now} }

// Create validates and creates a task.
func (m *Manager) Create(ctx context.Context, requester uint64, input domain.TaskInput) (*domain.Task, error) {
	if err := domain.ValidateTaskInput(input, m.now().UTC()); err != nil {
		return nil, apperror.Wrap(http.StatusBadRequest, "invalid_errand", err.Error(), err)
	}
	return m.store.Create(ctx, requester, input)
}

// Update validates and updates an editable task.
func (m *Manager) Update(ctx context.Context, id, requester, version uint64, input domain.TaskInput) (*domain.Task, error) {
	if err := domain.ValidateTaskUpdateInput(input, m.now().UTC()); err != nil {
		return nil, apperror.Wrap(http.StatusBadRequest, "invalid_errand", err.Error(), err)
	}
	return m.store.Update(ctx, id, requester, version, input, m.now().UTC())
}

// GetVisible returns a task only when it is public or owned by the viewer.
func (m *Manager) GetVisible(ctx context.Context, id, viewerID uint64) (*domain.Task, error) {
	return m.store.GetVisible(ctx, id, viewerID)
}

// ListAdmin returns tasks matching moderation filters.
func (m *Manager) ListAdmin(
	ctx context.Context,
	search domain.AdminSearch,
	page,
	size int,
) ([]domain.Task, int64, error) {
	return m.store.ListAdmin(ctx, search, page, size)
}

// SubmitReview resubmits an edited task for moderation.
func (m *Manager) SubmitReview(ctx context.Context, id, requester, version uint64) (*domain.Task, error) {
	return m.store.SubmitReview(ctx, id, requester, version)
}

// Review records an administrator moderation decision.
func (m *Manager) Review(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	approved bool,
	reason string,
) (*domain.Task, error) {
	return m.store.Review(ctx, id, adminID, version, approved, reason, m.now().UTC())
}

// RevokeReview moves a publicly visible, unaccepted task back to moderation.
func (m *Manager) RevokeReview(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	reason string,
) (*domain.Task, error) {
	return m.store.RevokeReview(ctx, id, adminID, version, reason, m.now().UTC())
}

// ListOpen returns open tasks available for acceptance.
func (m *Manager) ListOpen(ctx context.Context, page, size int) ([]domain.Task, int64, error) {
	return m.store.ListOpen(ctx, page, size, m.now().UTC())
}

// ListMine returns tasks related to the user after validating list filters.
func (m *Manager) ListMine(
	ctx context.Context,
	user uint64,
	search domain.MineSearch,
	page,
	size int,
) ([]domain.Task, int64, error) {
	search, err := domain.NormalizeMineSearch(search)
	if err != nil {
		return nil, 0, apperror.Wrap(
			http.StatusBadRequest,
			"invalid_errand_filter",
			err.Error(),
			err,
		)
	}
	return m.store.ListMine(ctx, user, search, page, size)
}

// ViewerContext returns the viewer relation and currently available lifecycle actions.
func (m *Manager) ViewerContext(task *domain.Task, viewerID uint64) (string, []string) {
	return domain.ViewerRelation(task, viewerID),
		domain.AvailableActions(task, viewerID, m.now().UTC())
}

// Accept atomically accepts a task and creates the trade order.
func (m *Manager) Accept(ctx context.Context, id, runner, version uint64, key string) (*domain.Task, *tradedomain.Order, error) {
	return m.store.Accept(ctx, id, runner, version, key, m.now().UTC())
}

// Pickup records that the runner has picked up the errand item.
func (m *Manager) Pickup(ctx context.Context, id, runner, version uint64) (*domain.Task, error) {
	return m.store.Pickup(ctx, id, runner, version, m.now().UTC())
}

// Deliver records that the runner has delivered the errand item.
func (m *Manager) Deliver(ctx context.Context, id, runner, version uint64) (*domain.Task, error) {
	return m.store.Deliver(ctx, id, runner, version, m.now().UTC())
}

// Complete marks a delivered task completed by the requester.
func (m *Manager) Complete(ctx context.Context, id, requester, version uint64) (*domain.Task, *tradedomain.Order, error) {
	return m.store.Complete(ctx, id, requester, version, m.now().UTC())
}

// Cancel cancels a task or its order workflow.
func (m *Manager) Cancel(ctx context.Context, id, actor, version uint64) (*domain.Task, *tradedomain.Order, error) {
	return m.store.Cancel(ctx, id, actor, version, m.now().UTC())
}

// CompleteOrder completes the trade order that belongs to a task.
func (m *Manager) CompleteOrder(ctx context.Context, id, actor, version uint64) (*tradedomain.Order, error) {
	return m.store.CompleteOrder(ctx, id, actor, version, m.now().UTC())
}

// CancelOrder cancels the trade order that belongs to a task.
func (m *Manager) CancelOrder(ctx context.Context, id, actor, version uint64) (*tradedomain.Order, error) {
	return m.store.CancelOrder(ctx, id, actor, version, m.now().UTC())
}
