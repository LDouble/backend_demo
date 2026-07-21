package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	"gorm.io/gorm"
)

type authUsers struct{ u *model.User }

func (f authUsers) GetByID(_ context.Context, id uint64) (*model.User, error) {
	if f.u.ID != id {
		return nil, gorm.ErrRecordNotFound
	}
	return f.u, nil
}
func (f authUsers) GetByUsername(_ context.Context, name string) (*model.User, error) {
	if f.u.Username != name {
		return nil, gorm.ErrRecordNotFound
	}
	return f.u, nil
}

type memorySession struct {
	sid    string
	uid    uint64
	hash   string
	exists bool
}

type failingSession struct{}

func (failingSession) Create(context.Context, string, uint64, string, time.Duration) error {
	return errors.New("session unavailable")
}
func (failingSession) Exists(context.Context, string) (bool, error) {
	return false, errors.New("session unavailable")
}
func (failingSession) Rotate(context.Context, string, string, string, time.Duration) (bool, error) {
	return false, errors.New("session unavailable")
}
func (failingSession) Delete(context.Context, string) error {
	return errors.New("session unavailable")
}

func (m *memorySession) Create(_ context.Context, sid string, uid uint64, hash string, _ time.Duration) error {
	m.sid = sid
	m.uid = uid
	m.hash = hash
	m.exists = true
	return nil
}
func (m *memorySession) Exists(_ context.Context, sid string) (bool, error) {
	return m.exists && m.sid == sid, nil
}
func (m *memorySession) Rotate(_ context.Context, sid, oldHash, newHash string, _ time.Duration) (bool, error) {
	if !m.exists || m.sid != sid || m.hash != oldHash {
		return false, nil
	}
	m.hash = newHash
	return true, nil
}
func (m *memorySession) Delete(_ context.Context, sid string) error {
	if sid == m.sid {
		m.exists = false
	}
	return nil
}
func TestSessionLifecycle(t *testing.T) {
	hash, err := user.HashPassword("long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	repo := authUsers{u: &model.User{ID: 7, Username: "admin", PasswordHash: hash, Status: model.UserActive}}
	store := &memorySession{}
	svc := NewService(repo, store, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour)
	pair, err := svc.Login(context.Background(), "admin", "long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	uid, sid, err := svc.Authenticate(context.Background(), pair.AccessToken)
	if err != nil || uid != 7 || sid == "" {
		t.Fatalf("authenticate: uid=%d sid=%q err=%v", uid, sid, err)
	}
	rotated, err := svc.Refresh(context.Background(), pair.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.RefreshToken == pair.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if _, err = svc.Refresh(context.Background(), pair.RefreshToken); err == nil {
		t.Fatal("old refresh token must be rejected")
	}
	if err = svc.Logout(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.Authenticate(context.Background(), rotated.AccessToken); err == nil {
		t.Fatal("logged out access token must fail")
	}
}
func TestLoginRejectsInvalidUser(t *testing.T) {
	svc := NewService(authUsers{u: &model.User{ID: 1, Username: "admin", PasswordHash: "bad", Status: model.UserActive}}, &memorySession{}, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour)
	if _, err := svc.Login(context.Background(), "admin", "wrong"); err == nil {
		t.Fatal("expected invalid credentials")
	}
}

func TestDisabledAndInvalidTokens(t *testing.T) {
	hash, _ := user.HashPassword("long-enough-password")
	u := &model.User{ID: 1, Username: "admin", PasswordHash: hash, Status: model.UserDisabled}
	svc := NewService(authUsers{u: u}, &memorySession{}, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour)
	if _, err := svc.Login(context.Background(), "admin", "long-enough-password"); err == nil {
		t.Fatal("disabled login must fail")
	}
	if _, _, err := svc.Authenticate(context.Background(), "not-a-token"); err == nil {
		t.Fatal("invalid access must fail")
	}
	if _, err := svc.Refresh(context.Background(), "not-a-token"); err == nil {
		t.Fatal("invalid refresh must fail")
	}
}

func TestSessionStoreErrors(t *testing.T) {
	hash, err := user.HashPassword("long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	repository := authUsers{u: &model.User{ID: 1, Username: "admin", PasswordHash: hash, Status: model.UserActive}}
	service := NewService(repository, failingSession{}, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour)
	if _, err = service.Login(context.Background(), "admin", "long-enough-password"); err == nil {
		t.Fatal("session create error must propagate")
	}
	if err = service.Logout(context.Background(), "sid"); err == nil {
		t.Fatal("session delete error must propagate")
	}
}
