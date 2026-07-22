package user

import (
	"context"
	"errors"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"gorm.io/gorm"
)

type fakeRepo struct {
	users map[uint64]*model.User
	next  uint64
}
type fakeGuard struct{ allowed bool }

type atomicGuard struct {
	disabled uint64
	err      error
}

func (g *atomicGuard) CanDisable(context.Context, uint64) (bool, error) { return true, nil }
func (g *atomicGuard) DisableUser(_ context.Context, userID uint64) error {
	g.disabled = userID
	return g.err
}

type fakeRevoker struct {
	users []uint64
	err   error
}

func (r *fakeRevoker) RevokeUser(_ context.Context, userID uint64) error {
	r.users = append(r.users, userID)
	return r.err
}

type assigningGuard struct {
	assigned uint64
	err      error
}

func (g *assigningGuard) CanDisable(context.Context, uint64) (bool, error) { return true, nil }
func (g *assigningGuard) EnsureGuestForUser(_ context.Context, id uint64) error {
	g.assigned = id
	return g.err
}

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
func (f *fakeRepo) UpdateFields(_ context.Context, id uint64, changes UpdateFields) error {
	u := f.users[id]
	if changes.Username != nil {
		u.Username = *changes.Username
	}
	if changes.PasswordHash != nil {
		u.PasswordHash = *changes.PasswordHash
	}
	if changes.Status != nil {
		u.Status = *changes.Status
	}
	if changes.IncrementSessionVersion {
		u.SessionVersion++
	}
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

func TestCreateAssignsGuestRole(t *testing.T) {
	repo := newFakeRepo()
	guard := &assigningGuard{}
	svc := NewService(repo, guard)
	user, err := svc.Create(context.Background(), "new.member", "initial-password-123")
	if err != nil || guard.assigned != user.ID {
		t.Fatalf("user=%+v assigned=%d err=%v", user, guard.assigned, err)
	}
	guard.err = errors.New("casbin unavailable")
	if _, err = svc.Create(context.Background(), "other.member", "initial-password-123"); err == nil {
		t.Fatal("assignment failure must be returned")
	}
}

func TestUserManagement(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	revoker := &fakeRevoker{}
	svc := NewService(repo, fakeGuard{allowed: true}).WithSessionRevoker(revoker)
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
	if len(revoker.users) != 2 || revoker.users[0] != u.ID || revoker.users[1] != u.ID {
		t.Fatalf("revoked users=%v", revoker.users)
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

func TestSessionRevocationFailureIsReturned(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	revoker := &fakeRevoker{err: errors.New("redis unavailable")}
	service := NewService(repo, fakeGuard{allowed: true}).WithSessionRevoker(revoker)
	created, err := service.Create(ctx, "secure.user", "initial-password-123")
	if err != nil {
		t.Fatal(err)
	}
	password := "changed-password-123"
	if _, err = service.Update(ctx, created.ID, nil, &password); err == nil {
		t.Fatal("password change ignored revocation failure")
	}
	if _, err = service.SetStatus(ctx, created.ID, model.UserDisabled); err == nil {
		t.Fatal("disable ignored revocation failure")
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

func TestAtomicDisableAndSessionRevocation(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	guard := &atomicGuard{}
	revoker := &fakeRevoker{}
	service := NewService(repo, guard).WithSessionRevoker(revoker)
	created, err := service.Create(ctx, "atomic.admin", "initial-password-123")
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := service.SetStatus(ctx, created.ID, model.UserDisabled)
	if err != nil || disabled.Status != model.UserDisabled || guard.disabled != created.ID {
		t.Fatalf("disabled=%+v guard=%d err=%v", disabled, guard.disabled, err)
	}
	if len(revoker.users) != 1 || revoker.users[0] != created.ID {
		t.Fatalf("revoked=%v", revoker.users)
	}

	guard.err = errors.New("last administrator")
	if _, err = service.SetStatus(ctx, created.ID, model.UserDisabled); err == nil {
		t.Fatal("atomic guard failure was ignored")
	}
	guard.err = nil
	revoker.err = errors.New("redis unavailable")
	if _, err = service.SetStatus(ctx, created.ID, model.UserDisabled); err == nil {
		t.Fatal("atomic disable ignored session revocation failure")
	}
}

func TestSessionRevocationIsDeferredUntilCommit(t *testing.T) {
	ctx, callbacks := idempotency.WithAfterCommit(context.Background())
	repo := newFakeRepo()
	revoker := &fakeRevoker{}
	service := NewService(repo, fakeGuard{allowed: true}).WithSessionRevoker(revoker)
	created, err := service.Create(ctx, "deferred.user", "initial-password-123")
	if err != nil {
		t.Fatal(err)
	}
	password := "changed-password-123"
	if _, err = service.Update(ctx, created.ID, nil, &password); err != nil {
		t.Fatal(err)
	}
	if len(revoker.users) != 0 {
		t.Fatalf("sessions revoked before commit: %v", revoker.users)
	}
	if err = callbacks.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(revoker.users) != 1 || revoker.users[0] != created.ID {
		t.Fatalf("sessions not revoked after commit: %v", revoker.users)
	}
}
