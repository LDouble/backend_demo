package user

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
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

type preservingGuard struct {
	guestAssigned   uint64
	currentAssigned uint64
	err             error
}

func (g *preservingGuard) CanDisable(context.Context, uint64) (bool, error) { return true, nil }
func (g *preservingGuard) EnsureGuestForUser(_ context.Context, id uint64) error {
	g.guestAssigned = id
	return nil
}
func (g *preservingGuard) EnsureCurrentBaseRoleForUser(_ context.Context, id uint64) error {
	g.currentAssigned = id
	return g.err
}

// txAssigningGuard proves the service prefers the transaction-aware variant
// of EnsureGuestForUser when one is exposed by the role guard, so the user
// insert and the role assignment are observable to a single SQL transaction.
type txAssigningGuard struct {
	assigned uint64
	txSeen   bool
	err      error
}

func (g *txAssigningGuard) CanDisable(context.Context, uint64) (bool, error) { return true, nil }
func (g *txAssigningGuard) EnsureGuestForUser(_ context.Context, id uint64) error {
	g.assigned = id
	return g.err
}
func (g *txAssigningGuard) EnsureGuestForUserTx(_ context.Context, _ *gorm.DB, id uint64) error {
	g.assigned = id
	g.txSeen = true
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
func (f *fakeRepo) GetByAppOpenID(_ context.Context, appID, openID string) (*model.User, error) {
	for _, u := range f.users {
		if u.AppID == nil || u.OpenID == nil {
			continue
		}
		if *u.AppID == appID && *u.OpenID == openID {
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
	if changes.UnionID != nil {
		value := *changes.UnionID
		u.UnionID = &value
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
	if _, err := HashPassword(strings.Repeat("a", 73)); err == nil {
		t.Fatal("expected overlong password rejection")
	} else if appErr, ok := apperror.As(err); !ok || appErr.Code != "invalid_password" {
		t.Fatalf("expected invalid_password, got %v", err)
	}
}

func TestChangePasswordVerifiesCurrentPasswordAndRevokesSessions(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	revoker := &fakeRevoker{}
	service := NewService(repo, nil).WithSessionRevoker(revoker)
	created, err := service.Create(ctx, "password.user", "initial-password-123")
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ChangePassword(ctx, created.ID, "wrong-password", "replacement-password-123"); err == nil {
		t.Fatal("incorrect current password was accepted")
	}
	if len(revoker.users) != 0 {
		t.Fatalf("sessions revoked after rejected password change: %v", revoker.users)
	}
	if err = service.ChangePassword(ctx, created.ID, "initial-password-123", "replacement-password-123"); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(stored.PasswordHash, "replacement-password-123") || stored.SessionVersion != 2 {
		t.Fatalf("password/session version not updated: %+v", stored)
	}
	if len(revoker.users) != 1 || revoker.users[0] != created.ID {
		t.Fatalf("sessions not revoked: %v", revoker.users)
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

func TestFindOrCreateForWechatCreatesNewUser(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	guard := &assigningGuard{}
	svc := NewService(repo, guard)

	u, created, err := svc.FindOrCreateForWechat(ctx, "wxapp-1", "oX1", "uX1")
	if err != nil {
		t.Fatalf("FindOrCreateForWechat: %v", err)
	}
	if !created {
		t.Fatal("expected a new user to be created")
	}
	if u.AppID == nil || *u.AppID != "wxapp-1" {
		t.Fatalf("AppID not persisted: %+v", u.AppID)
	}
	if u.OpenID == nil || *u.OpenID != "oX1" {
		t.Fatalf("OpenID not persisted: %+v", u.OpenID)
	}
	if u.UnionID == nil || *u.UnionID != "uX1" {
		t.Fatalf("UnionID not persisted: %+v", u.UnionID)
	}
	if u.Status != model.UserActive {
		t.Fatalf("status=%q", u.Status)
	}
	if u.PasswordHash == "" || CheckPassword(u.PasswordHash, "anything") {
		t.Fatal("locked password must reject any candidate")
	}
	if guard.assigned != u.ID {
		t.Fatalf("guest role not assigned, got %d", guard.assigned)
	}
	if _, _, err = svc.FindOrCreateForWechat(ctx, "wxapp-1", "oX1", "uX1"); err != nil {
		t.Fatalf("second lookup: %v", err)
	} else if len(repo.users) != 1 {
		t.Fatalf("expected single account, got %d", len(repo.users))
	}
}

func TestFindOrCreateForWechatBackfillsUnionID(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(repo, nil)

	first, created, err := svc.FindOrCreateForWechat(ctx, "wxapp-1", "oX1", "")
	if err != nil || !created {
		t.Fatalf("seed: created=%v err=%v", created, err)
	}
	if first.UnionID != nil {
		t.Fatalf("expected nil unionid on first login, got %+v", first.UnionID)
	}
	second, created, err := svc.FindOrCreateForWechat(ctx, "wxapp-1", "oX1", "uX1")
	if err != nil || created {
		t.Fatalf("expected existing user, got created=%v err=%v", created, err)
	}
	if second.UnionID == nil || *second.UnionID != "uX1" {
		t.Fatalf("UnionID not backfilled: %+v", second.UnionID)
	}
}

func TestFindOrCreateForWechatRejectsDisabled(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(repo, nil)
	u, _, err := svc.FindOrCreateForWechat(ctx, "wxapp-1", "oX1", "uX1")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	disabled := model.UserDisabled
	if err = repo.UpdateFields(ctx, u.ID, UpdateFields{Status: &disabled}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, _, err = svc.FindOrCreateForWechat(ctx, "wxapp-1", "oX1", "uX1"); err == nil {
		t.Fatal("disabled wechat user must not log in")
	} else if appErr, ok := apperror.As(err); !ok || appErr.Code != "user_disabled" {
		t.Fatalf("expected user_disabled, got %v", err)
	}
}

func TestFindOrCreateForWechatRejectsEmptyInput(t *testing.T) {
	svc := NewService(newFakeRepo(), nil)
	if _, _, err := svc.FindOrCreateForWechat(context.Background(), "", "oX1", ""); err == nil {
		t.Fatal("empty appid must be rejected")
	}
	if _, _, err := svc.FindOrCreateForWechat(context.Background(), "wxapp-1", "", ""); err == nil {
		t.Fatal("empty openid must be rejected")
	}
}

// TestFindOrCreateForWechatReconcilesGuestRole guards the fix for Codex P1:
// a half-applied provisioning from a previous failure must self-heal on the
// next successful load, so every WeChat login that resolves an existing
// account re-asserts the guest role.
func TestFindOrCreateForWechatReconcilesGuestRole(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	guard := &assigningGuard{}
	svc := NewService(repo, guard)

	first, _, err := svc.FindOrCreateForWechat(ctx, "wxapp-1", "oX1", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if guard.assigned != first.ID {
		t.Fatalf("expected guest role to be assigned on first create, got %d", guard.assigned)
	}
	// Simulate a prior failure that left the role unassigned.
	guard.assigned = 0
	second, created, err := svc.FindOrCreateForWechat(ctx, "wxapp-1", "oX1", "")
	if err != nil || created {
		t.Fatalf("expected existing user, got created=%v err=%v", created, err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected same user, got %d", second.ID)
	}
	if guard.assigned != first.ID {
		t.Fatalf("guest role was not reconciled for existing user, got %d", guard.assigned)
	}
}

func TestFindOrCreateForWechatPreservesCurrentBaseRole(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	guard := &preservingGuard{}
	svc := NewService(repo, guard)

	user, created, err := svc.FindOrCreateForWechat(ctx, "wxapp-member", "oMember", "")
	if err != nil || !created {
		t.Fatalf("seed: created=%v err=%v", created, err)
	}
	guard.guestAssigned = 0
	if _, created, err = svc.FindOrCreateForWechat(ctx, "wxapp-member", "oMember", ""); err != nil || created {
		t.Fatalf("reload: created=%v err=%v", created, err)
	}
	if guard.currentAssigned != user.ID {
		t.Fatalf("current base role was not reconciled for user %d", user.ID)
	}
	if guard.guestAssigned != 0 {
		t.Fatalf("existing account was forced back to guest: user=%d", guard.guestAssigned)
	}
	guard.err = errors.New("role reconciliation unavailable")
	if _, _, err = svc.FindOrCreateForWechat(ctx, "wxapp-member", "oMember", ""); !errors.Is(err, guard.err) {
		t.Fatalf("expected role reconciliation error, got %v", err)
	}
}

// TestFindOrCreateForWechatPropagatesRandFailure locks in the P2 fix that
// refuses to create an account when the locked-password randomness source
// is unavailable, rather than silently writing a publicly known hash.
func TestFindOrCreateForWechatPropagatesRandFailure(t *testing.T) {
	original := lockedSecretGenerator
	lockedSecretGenerator = func() (string, error) { return "", errors.New("entropy unavailable") }
	defer func() { lockedSecretGenerator = original }()

	svc := NewService(newFakeRepo(), &assigningGuard{})
	_, _, err := svc.FindOrCreateForWechat(context.Background(), "wxapp-1", "oX1", "")
	if err == nil {
		t.Fatal("rand failure must propagate")
	}
	if !errors.Is(err, err) || !strings.Contains(err.Error(), "entropy unavailable") {
		t.Fatalf("expected entropy error, got %v", err)
	}
}

func TestFindOrCreateForWechatRejectsOverlongLockedSecret(t *testing.T) {
	original := lockedSecretGenerator
	lockedSecretGenerator = func() (string, error) { return strings.Repeat("x", 73), nil }
	defer func() { lockedSecretGenerator = original }()

	svc := NewService(newFakeRepo(), nil)
	_, _, err := svc.FindOrCreateForWechat(context.Background(), "wxapp-1", "oX1", "")
	if err == nil || !strings.Contains(err.Error(), "hash locked password") {
		t.Fatalf("expected locked-password length error, got %v", err)
	}
}

// TestFindOrCreateForWechatFallbackPath covers the no-DB code path used by
// unit tests that do not wire a *gorm.DB. It must still create the user,
// assign the guest role, surface a disabled user, and tolerate a missing
// guard. The same paths run in production when (for any reason) a runtime
// Build never wires the database, so they cannot panic or silently swallow
// errors.
func TestFindOrCreateForWechatFallbackPath(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	svc := NewService(repo, nil)

	u, created, err := svc.FindOrCreateForWechat(ctx, "wxapp-fb", "oF1", "")
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if u.AppID == nil || *u.AppID != "wxapp-fb" {
		t.Fatalf("AppID not persisted: %+v", u)
	}

	// Existing user with no unionid yet: backfill must update the row.
	second, created, err := svc.FindOrCreateForWechat(ctx, "wxapp-fb", "oF1", "uF1")
	if err != nil || created {
		t.Fatalf("expected existing user, got created=%v err=%v", created, err)
	}
	if second.UnionID == nil || *second.UnionID != "uF1" {
		t.Fatalf("UnionID not backfilled: %+v", second.UnionID)
	}

	// Existing user already populated: a redundant call must not change it.
	third, _, err := svc.FindOrCreateForWechat(ctx, "wxapp-fb", "oF1", "uF2")
	if err != nil {
		t.Fatal(err)
	}
	if third.UnionID == nil || *third.UnionID != "uF1" {
		t.Fatalf("UnionID was overwritten: %+v", third.UnionID)
	}

	// Disabled user must be rejected.
	disabled := model.UserDisabled
	if err = repo.UpdateFields(ctx, u.ID, UpdateFields{Status: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.FindOrCreateForWechat(ctx, "wxapp-fb", "oF1", ""); err == nil {
		t.Fatal("disabled wechat user must not log in")
	} else if appErr, ok := apperror.As(err); !ok || appErr.Code != "user_disabled" {
		t.Fatalf("expected user_disabled, got %v", err)
	}
}

// TestFindOrCreateForWechatRepoAndBackfillErrors covers the error branches
// that the happy-path tests bypass: a Get lookup failure, an UpdateFields
// backfill failure, and the dup-key recovery path on a fallback repo.
func TestFindOrCreateForWechatRepoAndBackfillErrors(t *testing.T) {
	ctx := context.Background()

	lookupErr := errors.New("db unavailable")
	boom := &explodingRepo{lookupErr: lookupErr}
	svc := NewService(boom, nil)
	if _, _, err := svc.FindOrCreateForWechat(ctx, "wxapp-boom", "oB1", ""); err == nil || !errors.Is(err, lookupErr) {
		t.Fatalf("expected lookup error, got %v", err)
	}

	// Backfill UpdateFields failure path on an existing row.
	stored := &model.User{ID: 42, Username: "wx_existing", Status: model.UserActive}
	backfill := &backfillErrRepo{stored: stored, updateErr: errors.New("update unavailable")}
	svc = NewService(backfill, nil)
	if _, _, err := svc.FindOrCreateForWechat(ctx, "wxapp-bf", "oBF", "uBF"); err == nil || !strings.Contains(err.Error(), "backfill") {
		t.Fatalf("expected backfill error, got %v", err)
	}

	// Dup-key recovery on the fallback path: when a concurrent login won
	// the race, the new caller must read the winner and return it.
	dup := &dupKeyRepo{}
	svc = NewService(dup, &assigningGuard{})
	first, created, err := svc.FindOrCreateForWechat(ctx, "wxapp-dup", "oD1", "uD1")
	if err != nil || !created {
		t.Fatalf("first call: created=%v err=%v", created, err)
	}
	// Second call hits dup-key on Create, then reloads the winner.
	dup.existing = first
	second, created, err := svc.FindOrCreateForWechat(ctx, "wxapp-dup", "oD1", "uD1")
	if err != nil || created {
		t.Fatalf("dup-key recovery: created=%v err=%v", created, err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected winner, got id=%d", second.ID)
	}

	// Dup-key recovery where the winner is disabled.
	dup = &dupKeyRepo{}
	dup.existing = first
	disabled := model.UserDisabled
	dup.existing.Status = disabled
	svc = NewService(dup, &assigningGuard{})
	if _, _, err = svc.FindOrCreateForWechat(ctx, "wxapp-dup", "oD1", "uD1"); err == nil {
		t.Fatal("disabled winner must surface 403")
	} else if appErr, ok := apperror.As(err); !ok || appErr.Code != "user_disabled" {
		t.Fatalf("expected user_disabled, got %v", err)
	}
}

func TestFindOrCreateForWechatFallbackCreateFailures(t *testing.T) {
	ctx := context.Background()
	winner := &model.User{ID: 77, Username: "wx_winner", Status: model.UserActive}
	cases := []struct {
		name       string
		repo       *scriptedWechatRepo
		guard      *assigningGuard
		wantCode   string
		wantError  string
		wantWinner bool
	}{
		{
			name:      "create error",
			repo:      &scriptedWechatRepo{createErr: errors.New("insert unavailable")},
			wantError: "create wechat user",
		},
		{
			name:      "duplicate reload error",
			repo:      &scriptedWechatRepo{createErr: gorm.ErrDuplicatedKey, reloadErr: errors.New("reload unavailable")},
			wantError: "reload wechat user",
		},
		{
			name:       "duplicate active winner",
			repo:       &scriptedWechatRepo{createErr: gorm.ErrDuplicatedKey, winner: winner},
			wantWinner: true,
		},
		{
			name:     "duplicate disabled winner",
			repo:     &scriptedWechatRepo{createErr: gorm.ErrDuplicatedKey, winner: disabledWechatWinner(78)},
			wantCode: "user_disabled",
		},
		{
			name:      "guest assignment error",
			repo:      &scriptedWechatRepo{},
			guard:     &assigningGuard{err: errors.New("role unavailable")},
			wantError: "assign guest role",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewService(tc.repo, tc.guard)
			user, created, err := svc.FindOrCreateForWechat(ctx, "wxapp-scripted", "oScripted", "")
			switch {
			case tc.wantCode != "":
				appErr, ok := apperror.As(err)
				if !ok || appErr.Code != tc.wantCode {
					t.Fatalf("expected %s, got %v", tc.wantCode, err)
				}
			case tc.wantError != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q error, got %v", tc.wantError, err)
				}
			case tc.wantWinner:
				if err != nil || created || user.ID != winner.ID {
					t.Fatalf("winner=%+v created=%v err=%v", user, created, err)
				}
			}
		})
	}
}

func TestFindOrCreateForWechatDatabaseRaceRecovery(t *testing.T) {
	cases := []struct {
		name      string
		winner    *model.User
		reloadErr error
		guardErr  error
		wantCode  string
		wantError string
	}{
		{name: "active winner", winner: &model.User{Username: "db-winner", Status: model.UserActive}},
		{name: "disabled winner", winner: disabledWechatWinner(0), wantCode: "user_disabled"},
		{name: "reload error", reloadErr: errors.New("reload unavailable"), wantError: "reload wechat user"},
		{
			name: "role reconciliation error", winner: &model.User{Username: "role-winner", Status: model.UserActive},
			guardErr: errors.New("role unavailable"), wantError: "role unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := openWechatTestDB(t)
			winner := tc.winner
			if winner == nil {
				winner = &model.User{Username: "reload-winner", Status: model.UserActive}
			}
			appID, openID := "wxapp-race", "oRace"
			winner.AppID = &appID
			winner.OpenID = &openID
			winner.PasswordHash = "locked"
			winner.SessionVersion = 1
			if err := db.Create(winner).Error; err != nil {
				t.Fatal(err)
			}
			repo := &scriptedWechatRepo{winner: winner, reloadErr: tc.reloadErr}
			guard := &assigningGuard{err: tc.guardErr}
			svc := NewService(repo, guard).WithDatabase(db)
			user, created, err := svc.FindOrCreateForWechat(context.Background(), appID, openID, "")
			switch {
			case tc.wantCode != "":
				appErr, ok := apperror.As(err)
				if !ok || appErr.Code != tc.wantCode {
					t.Fatalf("expected %s, got %v", tc.wantCode, err)
				}
			case tc.wantError != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q error, got %v", tc.wantError, err)
				}
			default:
				if err != nil || created || user.ID != winner.ID {
					t.Fatalf("winner=%+v created=%v err=%v", user, created, err)
				}
			}
		})
	}
}

func TestFindOrCreateForWechatDatabaseCreateErrors(t *testing.T) {
	db := openWechatTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err = sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	svc := NewService(&scriptedWechatRepo{}, &txAssigningGuard{}).WithDatabase(db)
	if _, _, err = svc.FindOrCreateForWechat(context.Background(), "wxapp-closed", "oClosed", ""); err == nil {
		t.Fatal("expected closed database error")
	}

	db = openWechatTestDB(t)
	svc = NewService(&scriptedWechatRepo{}, &fakeGuard{allowed: true}).WithDatabase(db)
	if _, created, err := svc.FindOrCreateForWechat(context.Background(), "wxapp-no-assigner", "oNone", ""); err != nil || !created {
		t.Fatalf("missing optional assigner: created=%v err=%v", created, err)
	}
}

type explodingRepo struct {
	lookupErr error
}

func (explodingRepo) Create(context.Context, *model.User) error { return nil }
func (explodingRepo) GetByID(context.Context, uint64) (*model.User, error) {
	return nil, gorm.ErrRecordNotFound
}
func (explodingRepo) GetByUsername(context.Context, string) (*model.User, error) {
	return nil, gorm.ErrRecordNotFound
}
func (e explodingRepo) GetByAppOpenID(_ context.Context, _, _ string) (*model.User, error) {
	return nil, e.lookupErr
}
func (explodingRepo) List(context.Context, int, int) ([]model.User, int64, error) {
	return nil, 0, nil
}
func (explodingRepo) UpdateFields(context.Context, uint64, UpdateFields) error { return nil }

type backfillErrRepo struct {
	stored    *model.User
	updateErr error
}

func (backfillErrRepo) Create(context.Context, *model.User) error { return nil }
func (backfillErrRepo) GetByID(context.Context, uint64) (*model.User, error) {
	return nil, gorm.ErrRecordNotFound
}
func (backfillErrRepo) GetByUsername(context.Context, string) (*model.User, error) {
	return nil, gorm.ErrRecordNotFound
}
func (r backfillErrRepo) GetByAppOpenID(context.Context, string, string) (*model.User, error) {
	clone := *r.stored
	return &clone, nil
}
func (backfillErrRepo) List(context.Context, int, int) ([]model.User, int64, error) {
	return nil, 0, nil
}
func (r backfillErrRepo) UpdateFields(_ context.Context, id uint64, _ UpdateFields) error {
	if id == r.stored.ID {
		return r.updateErr
	}
	return nil
}

type scriptedWechatRepo struct {
	lookups   int
	winner    *model.User
	createErr error
	reloadErr error
}

func (r *scriptedWechatRepo) Create(_ context.Context, user *model.User) error {
	if r.createErr != nil {
		return r.createErr
	}
	user.ID = 1
	return nil
}
func (r *scriptedWechatRepo) GetByID(context.Context, uint64) (*model.User, error) {
	return nil, gorm.ErrRecordNotFound
}
func (r *scriptedWechatRepo) GetByUsername(context.Context, string) (*model.User, error) {
	return nil, gorm.ErrRecordNotFound
}
func (r *scriptedWechatRepo) GetByAppOpenID(context.Context, string, string) (*model.User, error) {
	r.lookups++
	if r.lookups == 1 {
		return nil, gorm.ErrRecordNotFound
	}
	if r.reloadErr != nil {
		return nil, r.reloadErr
	}
	if r.winner == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *r.winner
	return &clone, nil
}
func (*scriptedWechatRepo) List(context.Context, int, int) ([]model.User, int64, error) {
	return []model.User{}, 0, nil
}
func (*scriptedWechatRepo) UpdateFields(context.Context, uint64, UpdateFields) error { return nil }

func disabledWechatWinner(id uint64) *model.User {
	return &model.User{ID: id, Username: "disabled-winner", Status: model.UserDisabled}
}

func openWechatTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{TranslateError: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	return db
}

type dupKeyRepo struct {
	existing *model.User
	calls    int
}

// Create succeeds on the first call so the test can prime an existing user,
// then reports a duplicate on every subsequent call to drive the recovery
// path that re-loads the winner.
func (r *dupKeyRepo) Create(_ context.Context, u *model.User) error {
	r.calls++
	if r.calls > 1 {
		return gorm.ErrDuplicatedKey
	}
	u.ID = 1
	clone := *u
	r.existing = &clone
	return nil
}
func (r *dupKeyRepo) GetByID(context.Context, uint64) (*model.User, error) {
	return nil, gorm.ErrRecordNotFound
}
func (r *dupKeyRepo) GetByUsername(context.Context, string) (*model.User, error) {
	return nil, gorm.ErrRecordNotFound
}
func (r *dupKeyRepo) GetByAppOpenID(context.Context, string, string) (*model.User, error) {
	if r.existing == nil {
		return nil, gorm.ErrRecordNotFound
	}
	clone := *r.existing
	return &clone, nil
}
func (r *dupKeyRepo) List(context.Context, int, int) ([]model.User, int64, error) {
	return nil, 0, nil
}
func (r *dupKeyRepo) UpdateFields(context.Context, uint64, UpdateFields) error { return nil }

// TestFindOrCreateForWechatAtomicWithDB locks in the P1 fix that the user
// insert and the role assignment must succeed or fail together. The
// transaction-aware guard is exercised by wiring a real *gorm.DB and
// verifying that EnsureGuestForUserTx is invoked (not the non-transactional
// fallback) and that a guard error rolls back the user insert.
func TestFindOrCreateForWechatAtomicWithDB(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	repo := newFakeRepo()
	guard := &txAssigningGuard{}
	svc := NewService(repo, guard).WithDatabase(db)
	// fakeRepo holds the in-memory model, but the transactional path calls
	// s.db.Create instead. Bridge them by pre-seeding the fakeRepo so the
	// dup-key retry path can rehydrate on conflict.
	repo.users[1] = &model.User{ID: 1, Username: "seeded"}
	repo.next = 2

	// Force a guard error to prove the transaction rolls back the user row.
	guard.err = errors.New("casbin unavailable")
	if _, _, err = svc.FindOrCreateForWechat(context.Background(), "wxapp-1", "oX1", "uX1"); err == nil {
		t.Fatal("guard failure must propagate")
	}
	var count int64
	if err = db.Model(&model.User{}).Where("username LIKE ?", "wx_%").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("transaction did not roll back the user insert: count=%d", count)
	}
	if guard.assigned == 0 {
		t.Fatal("EnsureGuestForUserTx was not invoked")
	}
	if !guard.txSeen {
		t.Fatal("transaction-aware variant was not preferred")
	}

	// Happy path: when the guard succeeds the user row must be visible to
	// the same database that the transaction used.
	guard.err = nil
	guard.assigned = 0
	guard.txSeen = false
	created, isNew, err := svc.FindOrCreateForWechat(context.Background(), "wxapp-2", "oY1", "")
	if err != nil {
		t.Fatal(err)
	}
	if !isNew {
		t.Fatal("expected a new user to be created")
	}
	if !guard.txSeen || guard.assigned != created.ID {
		t.Fatalf("transaction-aware path not taken: assigned=%d txSeen=%v", guard.assigned, guard.txSeen)
	}
	// Mirror the committed row into the fakeRepo so a subsequent load
	// resolves as "existing" rather than racing on a fresh insert.
	repo.users[created.ID] = created
	if err = db.Model(&model.User{}).Where("app_id = ?", "wxapp-2").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one user row committed, got %d", count)
	}

	// Reconciling an existing account must also exercise the DB-backed
	// load path so a half-applied provisioning from a prior failure self-heals.
	guard.txSeen = false
	guard.assigned = 0
	if _, _, err = svc.FindOrCreateForWechat(context.Background(), "wxapp-2", "oY1", "uY1"); err != nil {
		t.Fatal(err)
	}
	if guard.assigned == 0 {
		t.Fatal("guest role was not reconciled for existing user via DB path")
	}

	// When an idempotent HTTP operation already owns the transaction, account
	// provisioning must join it instead of opening an independent transaction.
	executionContext, afterCommit := idempotency.WithAfterCommit(context.Background())
	err = db.Transaction(func(tx *gorm.DB) error {
		txContext := idempotency.WithTransaction(executionContext, tx)
		_, isNew, createErr := svc.FindOrCreateForWechat(txContext, "wxapp-3", "oZ1", "")
		if createErr != nil {
			return createErr
		}
		if !isNew {
			t.Fatal("expected outer transaction to create a user")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = afterCommit.Run(executionContext); err != nil {
		t.Fatal(err)
	}
}
