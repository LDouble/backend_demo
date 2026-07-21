// Package infrastructure persists participant-scoped trade queries.
package infrastructure

import (
	"context"

	platformquery "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
	"gorm.io/gorm"
)

// Store implements trade query operations.
type Store struct{ db *gorm.DB }

// NewStore creates a trade store.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// GetForUser returns an order scoped to its buyer or seller.
func (s *Store) GetForUser(ctx context.Context, userID, orderID uint64) (*domain.Order, error) {
	q := platformquery.Use(s.db).Order
	return q.WithContext(ctx).
		Where(q.ID.Eq(orderID), q.BuyerId.Eq(userID)).
		Or(q.ID.Eq(orderID), q.SellerId.Eq(userID)).
		First()
}

// ListForUser returns a deterministic page of a user's buy and sell orders.
func (s *Store) ListForUser(ctx context.Context, userID uint64, page, size int) ([]domain.Order, int64, error) {
	q := platformquery.Use(s.db).Order
	base := q.WithContext(ctx).Where(q.BuyerId.Eq(userID)).Or(q.SellerId.Eq(userID))
	total, err := base.Count()
	if err != nil {
		return nil, 0, err
	}
	values, err := base.Order(q.ID.Desc()).Offset((page - 1) * size).Limit(size).Find()
	rows := make([]domain.Order, len(values))
	for i := range values {
		rows[i] = *values[i]
	}
	return rows, total, err
}
