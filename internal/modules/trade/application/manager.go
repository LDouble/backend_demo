// Package application coordinates trade-order queries and shared authorization.
package application

import (
	"context"
	"errors"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
	"gorm.io/gorm"
)

// Store is the participant-scoped trade query contract.
type Store interface {
	GetForUser(context.Context, uint64, uint64) (*domain.Order, error)
	ListForUser(context.Context, uint64, int, int) ([]domain.Order, int64, error)
}

// Manager exposes trade orders without leaking another user's transactions.
type Manager struct{ store Store }

// NewManager creates a trade application manager.
func NewManager(store Store) *Manager { return &Manager{store: store} }

// Get returns an order only to one of its trade parties.
func (m *Manager) Get(ctx context.Context, userID, orderID uint64) (*domain.Order, error) {
	order, err := m.store.GetForUser(ctx, userID, orderID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.New(404, "order_not_found", "订单不存在或不属于当前用户")
	}
	return order, err
}

// List returns the current user's buy and sell orders.
func (m *Manager) List(ctx context.Context, userID uint64, page, size int) ([]domain.Order, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return m.store.ListForUser(ctx, userID, page, size)
}
