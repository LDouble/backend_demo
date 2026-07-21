package application

import (
	"context"
	"errors"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
	"gorm.io/gorm"
)

type tradeStoreStub struct {
	order *domain.Order
	err   error
}

func (s tradeStoreStub) GetForUser(context.Context, uint64, uint64) (*domain.Order, error) {
	return s.order, s.err
}

func (s tradeStoreStub) ListForUser(context.Context, uint64, int, int) ([]domain.Order, int64, error) {
	if s.err != nil {
		return nil, 0, s.err
	}
	return []domain.Order{*s.order}, 1, nil
}

func TestManagerGetScopesMissingOrder(t *testing.T) {
	manager := NewManager(tradeStoreStub{err: gorm.ErrRecordNotFound})
	_, err := manager.Get(context.Background(), 1, 2)
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("Get() error = %v, want public not-found error", err)
	}
}

func TestManagerListNormalizesPaging(t *testing.T) {
	order := &domain.Order{ID: 1, BuyerId: 2, SellerId: 3}
	manager := NewManager(tradeStoreStub{order: order})
	rows, total, err := manager.List(context.Background(), 2, 0, 1000)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("List() rows=%v total=%d error=%v", rows, total, err)
	}
}
