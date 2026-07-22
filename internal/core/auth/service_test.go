package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/auth/wechat"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	"gorm.io/gorm"
)

type authUsers struct{ u *model.User }

type nilAuthUsers struct{}

type failingAuthUsers struct{ err error }

func (f failingAuthUsers) GetByID(context.Context, uint64) (*model.User, error) {
	return nil, f.err
}
func (f failingAuthUsers) GetByUsername(context.Context, string) (*model.User, error) {
	return nil, f.err
}

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

// fakeWeChatUsers is a minimal WeChatUserFinder used by WeChat login tests.
type fakeWeChatUsers struct{ u *model.User }

func (f fakeWeChatUsers) FindOrCreateForWechat(_ context.Context, _, _, _ string) (*model.User, bool, error) {
	return f.u, false, nil
}

// fakeWeChatClient is a minimal WeChatClient used by WeChat login tests.
type fakeWeChatClient struct {
	session wechat.Session
	err     error
}

func (f fakeWeChatClient) Code2Session(_ context.Context, _, _ string) (wechat.Session, error) {
	if f.err != nil {
		return wechat.Session{}, f.err
	}
	return f.session, nil
}

// newTestService builds a Service with the legacy defaults (no WeChat wiring).
// WeChat-specific tests construct their own Service directly.
func newTestService(users UserRepository, sessions SessionStore) *Service {
	return NewService(users, sessions, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour, nil, nil)
}
func TestSessionLifecycle(t *testing.T) {
	hash, err := user.HashPassword("long-enough-password")
	if err != nil {
		t.Fatal(err)
	}
	repo := authUsers{u: &model.User{ID: 7, Username: "admin", PasswordHash: hash, Status: model.UserActive}}
	store := &memorySession{}
	svc := newTestService(repo, store)
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
	svc := newTestService(authUsers{u: &model.User{ID: 1, Username: "admin", PasswordHash: "bad", Status: model.UserActive}}, &memorySession{})
	if _, err := svc.Login(context.Background(), "admin", "wrong"); err == nil {
		t.Fatal("expected invalid credentials")
	}
}

func TestLoginPropagatesRepositoryFailure(t *testing.T) {
	repositoryErr := errors.New("database unavailable")
	service := newTestService(failingAuthUsers{err: repositoryErr}, &memorySession{})
	if _, err := service.Login(context.Background(), "admin", "password"); !errors.Is(err, repositoryErr) {
		t.Fatalf("Login error=%v", err)
	}
}

func TestDisabledAndInvalidTokens(t *testing.T) {
	hash, _ := user.HashPassword("long-enough-password")
	u := &model.User{ID: 1, Username: "admin", PasswordHash: hash, Status: model.UserDisabled}
	svc := newTestService(authUsers{u: u}, &memorySession{})
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
	service := newTestService(repository, failingSession{})
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
	service := newTestService(repository, store)
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
	service := newTestService(repository, store)
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
	service := newTestService(nilAuthUsers{}, store)
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
	service := newTestService(authUsers{}, &memorySession{})
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

func TestLoginByWeChatSuccess(t *testing.T) {
	u := &model.User{ID: 17, Username: "wx_member", Status: model.UserActive}
	users := fakeWeChatUsers{u: u}
	client := fakeWeChatClient{session: wechat.Session{OpenID: "oX1", UnionID: "uX1"}}
	store := &memorySession{}
	svc := NewService(nilAuthUsers{}, store, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour, users, client)
	pair, err := svc.LoginByWeChat(context.Background(), "wxapp-1", "code-1")
	if err != nil {
		t.Fatalf("LoginByWeChat: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatalf("expected non-empty token pair, got %+v", pair)
	}
	if store.uid != u.ID {
		t.Fatalf("session was not bound to the wechat user, got uid=%d", store.uid)
	}
}

func TestLoginByWeChatDisabled(t *testing.T) {
	users := fakeWeChatUsers{u: &model.User{ID: 1, Username: "wx_disabled", Status: model.UserDisabled}}
	client := fakeWeChatClient{session: wechat.Session{OpenID: "oX1"}}
	svc := NewService(nilAuthUsers{}, &memorySession{}, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour, users, client)
	_, err := svc.LoginByWeChat(context.Background(), "wxapp-1", "code-1")
	if err == nil {
		t.Fatal("disabled WeChat user must not log in")
	}
	if appErr, ok := apperror.As(err); !ok || appErr.Code != "user_disabled" {
		t.Fatalf("expected user_disabled error, got %v", err)
	}
}

func TestLoginByWeChatInvalidCode(t *testing.T) {
	client := fakeWeChatClient{err: apperror.New(401, "invalid_wechat_code", "微信登录失败")}
	svc := NewService(nilAuthUsers{}, &memorySession{}, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour, fakeWeChatUsers{}, client)
	_, err := svc.LoginByWeChat(context.Background(), "wxapp-1", "bad")
	if err == nil {
		t.Fatal("invalid code must surface an error")
	}
	if appErr, ok := apperror.As(err); !ok || appErr.Code != "invalid_wechat_code" {
		t.Fatalf("expected invalid_wechat_code error, got %v", err)
	}
}

func TestLoginByWeChatMissingWiring(t *testing.T) {
	svc := NewService(nilAuthUsers{}, &memorySession{}, "issuer", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour, nil, nil)
	_, err := svc.LoginByWeChat(context.Background(), "wxapp-1", "code")
	if err == nil {
		t.Fatal("LoginByWeChat without wiring must fail")
	}
	if appErr, ok := apperror.As(err); !ok || appErr.Code != "wechat_disabled" {
		t.Fatalf("expected wechat_disabled error, got %v", err)
	}
}
