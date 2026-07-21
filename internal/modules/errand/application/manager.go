// Package application coordinates errand task use cases.
package application

import (
	"context"
	"time"

	"github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
)

type Store interface {
	Create(context.Context, uint64, domain.TaskInput) (*domain.Task, error)
	Update(context.Context, uint64, uint64, uint64, domain.TaskInput, time.Time) (*domain.Task, error)
	Get(context.Context, uint64) (*domain.Task, error)
	ListOpen(context.Context, int, int, time.Time) ([]domain.Task, int64, error)
	ListMine(context.Context, uint64, int, int) ([]domain.Task, int64, error)
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

type Manager struct {
	store Store
	now   func() time.Time
}

func NewManager(store Store) *Manager { return &Manager{store: store, now: time.Now} }
func (m *Manager) Create(ctx context.Context, requester uint64, input domain.TaskInput) (*domain.Task, error) {
	if err := domain.ValidateTaskInput(input, m.now().UTC()); err != nil {
		return nil, err
	}
	return m.store.Create(ctx, requester, input)
}
func (m *Manager) Update(ctx context.Context, id, requester, version uint64, input domain.TaskInput) (*domain.Task, error) {
	if err := domain.ValidateTaskUpdateInput(input, m.now().UTC()); err != nil {
		return nil, err
	}
	return m.store.Update(ctx, id, requester, version, input, m.now().UTC())
}
func (m *Manager) Get(ctx context.Context, id uint64) (*domain.Task, error) {
	return m.store.Get(ctx, id)
}
func (m *Manager) ListOpen(ctx context.Context, page, size int) ([]domain.Task, int64, error) {
	return m.store.ListOpen(ctx, page, size, m.now().UTC())
}
func (m *Manager) ListMine(ctx context.Context, user uint64, page, size int) ([]domain.Task, int64, error) {
	return m.store.ListMine(ctx, user, page, size)
}
func (m *Manager) Accept(ctx context.Context, id, runner, version uint64, key string) (*domain.Task, *tradedomain.Order, error) {
	return m.store.Accept(ctx, id, runner, version, key, m.now().UTC())
}
func (m *Manager) Pickup(ctx context.Context, id, runner, version uint64) (*domain.Task, error) {
	return m.store.Pickup(ctx, id, runner, version, m.now().UTC())
}
func (m *Manager) Deliver(ctx context.Context, id, runner, version uint64) (*domain.Task, error) {
	return m.store.Deliver(ctx, id, runner, version, m.now().UTC())
}
func (m *Manager) Complete(ctx context.Context, id, requester, version uint64) (*domain.Task, *tradedomain.Order, error) {
	return m.store.Complete(ctx, id, requester, version, m.now().UTC())
}
func (m *Manager) Cancel(ctx context.Context, id, actor, version uint64) (*domain.Task, *tradedomain.Order, error) {
	return m.store.Cancel(ctx, id, actor, version, m.now().UTC())
}
func (m *Manager) CompleteOrder(ctx context.Context, id, actor, version uint64) (*tradedomain.Order, error) {
	return m.store.CompleteOrder(ctx, id, actor, version, m.now().UTC())
}
func (m *Manager) CancelOrder(ctx context.Context, id, actor, version uint64) (*tradedomain.Order, error) {
	return m.store.CancelOrder(ctx, id, actor, version, m.now().UTC())
}
