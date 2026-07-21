package user

import (
	"context"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/core/model"
	"gorm.io/gorm"
)

type fakeRepo struct {
	users map[uint64]*model.User
	next  uint64
}
type fakeGuard struct{ allowed bool }

func (g fakeGuard) CanDisable(context.Context, uint64) (bool, error) { return g.allowed, nil }

func newFakeRepo() *fakeRepo { return &fakeRepo{users: map[uint64]*model.User{}, next: 1} }
func (f *fakeRepo) Create(_ context.Context, u *model.User) error {
	for _, v := range f.users {
		if v.Username == u.Username {
			return gorm.ErrDuplicatedKey
		}
	}
	u.ID = f.next
	f.next++
	clone := *u
	f.users[u.ID] = &clone
	return nil
}
func (f *fakeRepo) GetByID(_ context.Context, id uint64) (*model.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *u
	return &clone, nil
}
func (f *fakeRepo) GetByUsername(_ context.Context, name string) (*model.User, error) {
	for _, u := range f.users {
		if u.Username == name {
			clone := *u
			return &clone, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}
func (f *fakeRepo) List(_ context.Context, _, _ int) ([]model.User, int64, error) {
	return nil, int64(len(f.users)), nil
}
func (f *fakeRepo) Update(_ context.Context, u *model.User) error {
	clone := *u
	f.users[u.ID] = &clone
	return nil
}
func TestCreateAndPassword(t *testing.T) {
	repo := newFakeRepo()
	svc := NewService(repo, nil)
	u, err := svc.Create(context.Background(), "admin.user", "long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(u.PasswordHash, "long-enough-password") {
		t.Fatal("password must verify")
	}
	if _, err = svc.Create(context.Background(), "bad name", "long-enough-password"); err == nil {
		t.Fatal("expected invalid username")
	}
	if _, err = svc.Create(context.Background(), "admin.user", "long-enough-password"); err == nil {
		t.Fatal("expected duplicate")
	}
}
func TestHashPasswordLength(t *testing.T) {
	if _, err := HashPassword("too-short"); err == nil {
		t.Fatal("expected short password rejection")
	}
}

func TestUserManagement(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(repo, fakeGuard{allowed: true})
	u, err := svc.Create(ctx, "first.user", "initial-password-123")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, u.ID)
	if err != nil || got.Username != "first.user" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	byName, err := svc.FindByUsername(ctx, u.Username)
	if err != nil || byName.ID != u.ID {
		t.Fatalf("byName=%v err=%v", byName, err)
	}
	name := "renamed.user"
	password := "changed-password-123"
	updated, err := svc.Update(ctx, u.ID, &name, &password)
	if err != nil || updated.Username != name || !CheckPassword(updated.PasswordHash, password) {
		t.Fatalf("updated=%v err=%v", updated, err)
	}
	disabled, err := svc.SetStatus(ctx, u.ID, model.UserDisabled)
	if err != nil || disabled.Status != model.UserDisabled {
		t.Fatalf("disabled=%v err=%v", disabled, err)
	}
	if _, err = svc.SetStatus(ctx, u.ID, "bad"); err == nil {
		t.Fatal("invalid status must fail")
	}
	_, total, err := svc.List(ctx, 1, 20)
	if err != nil || total != 1 {
		t.Fatalf("total=%d err=%v", total, err)
	}
	if _, err = svc.Get(ctx, 999); err == nil {
		t.Fatal("missing user must fail")
	}
}
func TestLastAdminCannotDisable(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(repo, fakeGuard{allowed: false})
	u, err := svc.Create(ctx, "only.admin", "initial-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SetStatus(ctx, u.ID, model.UserDisabled); err == nil {
		t.Fatal("last admin must be protected")
	}
}
