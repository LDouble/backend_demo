// Package auth implements JWT authentication backed by Redis sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	"gorm.io/gorm"
)

const accessType = "access"
const refreshType = "refresh"

var dummyPasswordHash = func() string {
	hash, err := user.HashPassword("authentication-timing-placeholder")
	if err != nil {
		panic("initialize dummy password hash: " + err.Error())
	}
	return hash
}()

// UserRepository supplies authentication users.
type UserRepository interface {
	GetByID(context.Context, uint64) (*model.User, error)
	GetByUsername(context.Context, string) (*model.User, error)
}

// SessionStore persists and atomically rotates sessions.
type SessionStore interface {
	Create(context.Context, string, uint64, string, time.Duration) error
	Exists(context.Context, string) (bool, error)
	Rotate(context.Context, string, string, string, time.Duration) (bool, error)
	Delete(context.Context, string) error
	DeleteUser(context.Context, uint64) error
}

// Claims are platform JWT claims.
type Claims struct {
	Type           string `json:"typ"`
	SessionID      string `json:"sid"`
	FamilyID       string `json:"fid"`
	SessionVersion uint64 `json:"sv"`
	jwt.RegisteredClaims
}

// TokenPair contains a short-lived access token and rotating refresh token.
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
}

// Service manages authentication sessions.
type Service struct {
	users                 UserRepository
	sessions              SessionStore
	issuer                string
	key                   []byte
	accessTTL, refreshTTL time.Duration
	now                   func() time.Time
}

// NewService creates an authentication service.
func NewService(users UserRepository, sessions SessionStore, issuer string, key []byte, accessTTL, refreshTTL time.Duration) *Service {
	return &Service{users: users, sessions: sessions, issuer: issuer, key: key, accessTTL: accessTTL, refreshTTL: refreshTTL, now: time.Now}
}

// Login authenticates a user and creates a new device session.
func (s *Service) Login(ctx context.Context, username, password string) (TokenPair, error) {
	u, err := s.users.GetByUsername(ctx, username)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return TokenPair{}, fmt.Errorf("get user by username: %w", err)
	}
	passwordHash := dummyPasswordHash
	if u != nil {
		passwordHash = u.PasswordHash
	}
	passwordMatches := user.CheckPassword(passwordHash, password)
	if u == nil || !passwordMatches {
		return TokenPair{}, apperror.New(http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
	}
	if u.Status != model.UserActive {
		return TokenPair{}, apperror.New(403, "user_disabled", "用户已禁用")
	}
	sid, err := randomID()
	if err != nil {
		return TokenPair{}, err
	}
	pair, refreshHash, err := s.issue(u.ID, sid, effectiveSessionVersion(u))
	if err != nil {
		return TokenPair{}, err
	}
	if err = s.sessions.Create(ctx, sid, u.ID, refreshHash, s.refreshTTL); err != nil {
		return TokenPair{}, fmt.Errorf("create session: %w", err)
	}
	return pair, nil
}

// Refresh rotates a refresh token and returns a new pair.
func (s *Service) Refresh(ctx context.Context, raw string) (TokenPair, error) {
	claims, err := s.parse(raw, refreshType)
	if err != nil {
		return TokenPair{}, apperror.New(401, "invalid_refresh_token", "刷新令牌无效")
	}
	uid, err := strconv.ParseUint(claims.Subject, 10, 64)
	if err != nil {
		return TokenPair{}, apperror.New(401, "invalid_refresh_token", "刷新令牌无效")
	}
	u, err := s.users.GetByID(ctx, uid)
	if err != nil || u == nil || u.Status != model.UserActive || claims.SessionVersion != effectiveSessionVersion(u) {
		return TokenPair{}, apperror.New(401, "invalid_refresh_token", "刷新令牌无效")
	}
	pair, newHash, err := s.issue(uid, claims.SessionID, effectiveSessionVersion(u))
	if err != nil {
		return TokenPair{}, err
	}
	ok, err := s.sessions.Rotate(ctx, claims.SessionID, tokenHash(raw), newHash, s.refreshTTL)
	if err != nil {
		return TokenPair{}, fmt.Errorf("rotate session: %w", err)
	}
	if !ok {
		if deleteErr := s.sessions.Delete(ctx, claims.SessionID); deleteErr != nil {
			return TokenPair{}, fmt.Errorf("revoke reused refresh family: %w", deleteErr)
		}
		return TokenPair{}, apperror.New(401, "refresh_reused", "刷新令牌已失效")
	}
	return pair, nil
}

// RefreshFamily returns a stable, non-secret limiter scope for a refresh family.
func (s *Service) RefreshFamily(raw string) string {
	claims, err := s.parse(raw, refreshType)
	if err != nil {
		return tokenHash(raw)
	}
	return claims.FamilyID
}

// RevokeUser removes every active token family for a user.
func (s *Service) RevokeUser(ctx context.Context, userID uint64) error {
	if err := s.sessions.DeleteUser(ctx, userID); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

// Authenticate validates an access token and active session.
func (s *Service) Authenticate(ctx context.Context, raw string) (uint64, string, error) {
	claims, err := s.parse(raw, accessType)
	if err != nil {
		return 0, "", apperror.New(401, "invalid_access_token", "访问令牌无效")
	}
	uid, err := strconv.ParseUint(claims.Subject, 10, 64)
	if err != nil {
		return 0, "", apperror.New(401, "invalid_access_token", "访问令牌无效")
	}
	ok, err := s.sessions.Exists(ctx, claims.SessionID)
	if err != nil {
		return 0, "", fmt.Errorf("check session: %w", err)
	}
	if !ok {
		return 0, "", apperror.New(401, "session_expired", "会话已失效")
	}
	u, err := s.users.GetByID(ctx, uid)
	if err != nil || u == nil || u.Status != model.UserActive || claims.SessionVersion != effectiveSessionVersion(u) {
		return 0, "", apperror.New(403, "user_disabled", "用户已禁用")
	}
	return uid, claims.SessionID, nil
}

// Logout removes one device session.
func (s *Service) Logout(ctx context.Context, sid string) error {
	if err := s.sessions.Delete(ctx, sid); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}
func (s *Service) issue(uid uint64, sid string, sessionVersion uint64) (TokenPair, string, error) {
	now := s.now()
	access, err := s.sign(uid, sid, sessionVersion, accessType, now.Add(s.accessTTL))
	if err != nil {
		return TokenPair{}, "", err
	}
	refresh, err := s.sign(uid, sid, sessionVersion, refreshType, now.Add(s.refreshTTL))
	if err != nil {
		return TokenPair{}, "", err
	}
	return TokenPair{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", ExpiresIn: int64(s.accessTTL.Seconds())}, tokenHash(refresh), nil
}
func (s *Service) sign(uid uint64, sid string, sessionVersion uint64, typ string, expires time.Time) (string, error) {
	jti, err := randomID()
	if err != nil {
		return "", err
	}
	now := s.now()
	claims := Claims{Type: typ, SessionID: sid, FamilyID: sid, SessionVersion: sessionVersion, RegisteredClaims: jwt.RegisteredClaims{Issuer: s.issuer, Subject: strconv.FormatUint(uid, 10), ID: jti, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(expires)}}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}
	return raw, nil
}
func (s *Service) parse(raw, typ string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return s.key, nil
	}, jwt.WithIssuer(s.issuer), jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(s.now))
	if err != nil || !token.Valid || claims.Type != typ || claims.SessionID == "" || claims.FamilyID != claims.SessionID || claims.SessionVersion == 0 {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
func randomID() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func tokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func effectiveSessionVersion(u *model.User) uint64 {
	if u.SessionVersion == 0 {
		return 1
	}
	return u.SessionVersion
}
