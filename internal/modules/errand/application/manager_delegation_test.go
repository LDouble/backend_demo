package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
)

type delegatingStore struct {
	Store
	err error
}

func (store *delegatingStore) Create(
	context.Context,
	uint64,
	domain.TaskInput,
) (*domain.Task, error) {
	return nil, store.err
}

func (store *delegatingStore) Update(
	context.Context,
	uint64,
	uint64,
	uint64,
	domain.TaskInput,
	time.Time,
) (*domain.Task, error) {
	return nil, store.err
}

func (store *delegatingStore) GetVisible(
	context.Context,
	uint64,
	uint64,
) (*domain.Task, error) {
	return nil, store.err
}

func (store *delegatingStore) ListOpen(
	context.Context,
	int,
	int,
	time.Time,
) ([]domain.Task, int64, error) {
	return nil, 0, store.err
}

func (store *delegatingStore) ListMine(
	context.Context,
	uint64,
	domain.MineSearch,
	int,
	int,
) ([]domain.Task, int64, error) {
	return nil, 0, store.err
}

func (store *delegatingStore) ListAdmin(
	context.Context,
	domain.AdminSearch,
	int,
	int,
) ([]domain.Task, int64, error) {
	return nil, 0, store.err
}

func (store *delegatingStore) SubmitReview(
	context.Context,
	uint64,
	uint64,
	uint64,
) (*domain.Task, error) {
	return nil, store.err
}

func (store *delegatingStore) Review(
	context.Context,
	uint64,
	uint64,
	uint64,
	bool,
	string,
	time.Time,
) (*domain.Task, error) {
	return nil, store.err
}

func (store *delegatingStore) RevokeReview(
	context.Context,
	uint64,
	uint64,
	uint64,
	string,
	time.Time,
) (*domain.Task, error) {
	return nil, store.err
}

func (store *delegatingStore) Accept(
	context.Context,
	uint64,
	uint64,
	uint64,
	string,
	time.Time,
) (*domain.Task, *tradedomain.Order, error) {
	return nil, nil, store.err
}

func (store *delegatingStore) Pickup(
	context.Context,
	uint64,
	uint64,
	uint64,
	time.Time,
) (*domain.Task, error) {
	return nil, store.err
}

func (store *delegatingStore) Deliver(
	context.Context,
	uint64,
	uint64,
	uint64,
	time.Time,
) (*domain.Task, error) {
	return nil, store.err
}

func (store *delegatingStore) Complete(
	context.Context,
	uint64,
	uint64,
	uint64,
	time.Time,
) (*domain.Task, *tradedomain.Order, error) {
	return nil, nil, store.err
}

func (store *delegatingStore) Cancel(
	context.Context,
	uint64,
	uint64,
	uint64,
	time.Time,
) (*domain.Task, *tradedomain.Order, error) {
	return nil, nil, store.err
}

func (store *delegatingStore) CompleteOrder(
	context.Context,
	uint64,
	uint64,
	uint64,
	time.Time,
) (*tradedomain.Order, error) {
	return nil, store.err
}

func (store *delegatingStore) CancelOrder(
	context.Context,
	uint64,
	uint64,
	uint64,
	time.Time,
) (*tradedomain.Order, error) {
	return nil, store.err
}

func (store *delegatingStore) Contact(
	context.Context,
	*domain.Task,
	uint64,
) (domain.ContactDetails, error) {
	return domain.ContactDetails{}, store.err
}

func TestManagerDelegatesUseCases(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("store error")
	manager := NewManager(&delegatingStore{err: wantErr})
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	input := domain.TaskInput{
		Title: "取快递", Description: "送到宿舍", RewardCents: 500,
		PickupLocation: "快递站", DropoffLocation: "宿舍",
		Deadline: now.Add(time.Hour),
		Contact:  domain.ContactInput{Type: "wechat", Value: "contact", Provided: true},
	}
	task := &domain.Task{}
	for _, test := range []struct {
		name string
		call func() error
	}{
		{name: "contact", call: func() error {
			_, err := manager.Contact(ctx, task, 7)
			return err
		}},
		{name: "create", call: func() error {
			_, err := manager.Create(ctx, 7, input)
			return err
		}},
		{name: "update", call: func() error {
			_, err := manager.Update(ctx, 1, 7, 1, input)
			return err
		}},
		{name: "get visible", call: func() error {
			_, err := manager.GetVisible(ctx, 1, 7)
			return err
		}},
		{name: "list admin", call: func() error {
			_, _, err := manager.ListAdmin(ctx, domain.AdminSearch{}, 1, 20)
			return err
		}},
		{name: "submit review", call: func() error {
			_, err := manager.SubmitReview(ctx, 1, 7, 1)
			return err
		}},
		{name: "review", call: func() error {
			_, err := manager.Review(ctx, 1, 9, 1, true, "")
			return err
		}},
		{name: "revoke review", call: func() error {
			_, err := manager.RevokeReview(ctx, 1, 9, 1, "reason")
			return err
		}},
		{name: "list open", call: func() error {
			_, _, err := manager.ListOpen(ctx, 1, 20)
			return err
		}},
		{name: "list mine", call: func() error {
			_, _, err := manager.ListMine(ctx, 7, domain.MineSearch{}, 1, 20)
			return err
		}},
		{name: "accept", call: func() error {
			_, _, err := manager.Accept(ctx, 1, 8, 1, "key")
			return err
		}},
		{name: "pickup", call: func() error {
			_, err := manager.Pickup(ctx, 1, 8, 1)
			return err
		}},
		{name: "deliver", call: func() error {
			_, err := manager.Deliver(ctx, 1, 8, 1)
			return err
		}},
		{name: "complete", call: func() error {
			_, _, err := manager.Complete(ctx, 1, 7, 1)
			return err
		}},
		{name: "cancel", call: func() error {
			_, _, err := manager.Cancel(ctx, 1, 7, 1)
			return err
		}},
		{name: "complete order", call: func() error {
			_, err := manager.CompleteOrder(ctx, 1, 7, 1)
			return err
		}},
		{name: "cancel order", call: func() error {
			_, err := manager.CancelOrder(ctx, 1, 7, 1)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want %v", err, wantErr)
			}
		})
	}
}

func TestManagerRejectsInvalidCreateAndUpdate(t *testing.T) {
	manager := NewManager(&delegatingStore{})
	manager.now = func() time.Time {
		return time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	}
	if _, err := manager.Create(context.Background(), 7, domain.TaskInput{}); err == nil {
		t.Fatal("Create() error = nil")
	}
	if _, err := manager.Update(
		context.Background(),
		1,
		7,
		1,
		domain.TaskInput{},
	); err == nil {
		t.Fatal("Update() error = nil")
	}
}
