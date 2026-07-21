package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	"gorm.io/gorm"
)

type authUsers struct{ u *model.User }

type nilAuthUsers struct{}

func (nilAuthUsers) GetByID(context.Context, uint64) (*model.User, error) { return nil, nil }
func (nilAuthUsers) GetByUsername(context.Context, string) (*model.User, error) {
	return nil, nil
}

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
func (failingSession) DeleteUser(context.Context, uint64) error {
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
func (m *memorySession) DeleteUser(_ context.Context, uid uint64) error {
	if m.uid == uid {
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
	if _, _, err = svc.Authenticate(context.Background(), rotated.AccessToken); err == nil {
		t.Fatal("refresh reuse must revoke the token family")
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
	if err = service.RevokeUser(context.Background(), 1); err == nil {
		t.Fatal("session user-revoke error must propagate")
	}
}

func TestRefreshFamilyAndUserRevocation(t *testing.T) {
	hash, err := user.HashPassword("long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	repository := authUsers{u: &model.User{ID: 8, Username: "member", PasswordHash: hash, Status: model.UserActive}}
	store := &memorySession{}
	service := NewService(
		repository,
		store,
		"issuer",
		[]byte("0123456789abcdef0123456789abcdef"),
		time.Minute,
		time.Hour,
	)
	pair, err := service.Login(context.Background(), "member", "long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	family := service.RefreshFamily(pair.RefreshToken)
	if family == "" || family == tokenHash(pair.RefreshToken) {
		t.Fatalf("family=%q", family)
	}
	if invalid := service.RefreshFamily("not-a-token"); invalid != tokenHash("not-a-token") {
		t.Fatalf("invalid family=%q", invalid)
	}
	if err = service.RevokeUser(context.Background(), repository.u.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.Authenticate(context.Background(), pair.AccessToken); err == nil {
		t.Fatal("revoked user session remained active")
	}
}

func TestSessionVersionInvalidatesTokensWithoutRedisDeletion(t *testing.T) {
	hash, err := user.HashPassword("long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	repository := authUsers{u: &model.User{ID: 9, Username: "member", PasswordHash: hash, Status: model.UserActive, SessionVersion: 1}}
	store := &memorySession{}
	service := NewService(repository, store, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour)
	pair, err := service.Login(context.Background(), "member", "long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	repository.u.SessionVersion++
	if _, _, err = service.Authenticate(context.Background(), pair.AccessToken); err == nil {
		t.Fatal("access token survived a durable session version change")
	}
	if _, err = service.Refresh(context.Background(), pair.RefreshToken); err == nil {
		t.Fatal("refresh token survived a durable session version change")
	}
	if !store.exists {
		t.Fatal("test requires the Redis session to remain present")
	}
}

func TestAuthenticateAndRefreshRejectNilRepositoryResults(t *testing.T) {
	store := &memorySession{sid: "session", exists: true}
	service := NewService(nilAuthUsers{}, store, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour)
	pair, refreshHash, err := service.issue(1, store.sid, 1)
	if err != nil {
		t.Fatal(err)
	}
	store.hash = refreshHash
	if _, _, err = service.Authenticate(context.Background(), pair.AccessToken); err == nil {
		t.Fatal("nil user authenticated")
	}
	if _, err = service.Refresh(context.Background(), pair.RefreshToken); err == nil {
		t.Fatal("nil user refreshed")
	}
}

func TestParseRequiresExpiration(t *testing.T) {
	service := NewService(authUsers{}, &memorySession{}, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour)
	now := time.Now().UTC()
	claims := Claims{
		Type:           accessType,
		SessionID:      "session",
		FamilyID:       "session",
		SessionVersion: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   "issuer",
			Subject:  "1",
			IssuedAt: jwt.NewNumericDate(now),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(service.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.parse(raw, accessType); err == nil {
		t.Fatal("token without expiration was accepted")
	}
}
