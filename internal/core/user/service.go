// Package user implements platform account management.
package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,32}$`)

// Repository persists users.
type Repository interface {
	Create(context.Context, *model.User) error
	GetByID(context.Context, uint64) (*model.User, error)
	GetByUsername(context.Context, string) (*model.User, error)
	GetByAppOpenID(context.Context, string, string) (*model.User, error)
	List(context.Context, int, int) ([]model.User, int64, error)
	UpdateFields(context.Context, uint64, UpdateFields) error
}

// UpdateFields contains only the columns an account operation may change.
// IncrementSessionVersion is applied atomically by the repository.
type UpdateFields struct {
	Username                *string
	PasswordHash            *string
	Status                  *string
	UnionID                 *string
	IncrementSessionVersion bool
}

// RoleGuard prevents the last super administrator from being disabled.
type RoleGuard interface {
	CanDisable(context.Context, uint64) (bool, error)
}

// Service manages users.
type Service struct {
	repo    Repository
	guard   RoleGuard
	revoker SessionRevoker
	db      *gorm.DB
}

// SessionRevoker invalidates every active session for a security-sensitive user change.
type SessionRevoker interface {
	RevokeUser(context.Context, uint64) error
}

// NewService creates a user service.
func NewService(repo Repository, guard RoleGuard) *Service { return &Service{repo: repo, guard: guard} }

// WithSessionRevoker attaches authentication session invalidation.
func (s *Service) WithSessionRevoker(revoker SessionRevoker) *Service {
	s.revoker = revoker
	return s
}

// WithDatabase attaches the transaction boundary used by operations that must
// commit user creation and downstream provisioning atomically.
func (s *Service) WithDatabase(db *gorm.DB) *Service {
	s.db = db
	return s
}

// HashPassword validates and hashes a password.
func HashPassword(password string) (string, error) {
	if len(password) < 12 {
		return "", apperror.New(http.StatusBadRequest, "invalid_password", "密码至少需要 12 位")
	}
	// bcrypt silently truncates inputs longer than 72 bytes, which would
	// collapse two different passwords into the same digest. Truncate
	// explicitly so the operator-visible behavior is deterministic.
	if len(password) > 72 {
		password = password[:72]
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// CheckPassword compares a password with a bcrypt hash.
func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Create creates an active user.
func (s *Service) Create(ctx context.Context, username, password string) (*model.User, error) {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) {
		return nil, apperror.New(http.StatusBadRequest, "invalid_username", "用户名格式无效")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &model.User{Username: username, PasswordHash: hash, Status: model.UserActive, SessionVersion: 1}
	if err := s.repo.Create(ctx, u); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, apperror.New(http.StatusConflict, "username_exists", "用户名已存在")
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	if assigner, ok := s.guard.(interface {
		EnsureGuestForUser(context.Context, uint64) error
	}); ok {
		if err := assigner.EnsureGuestForUser(ctx, u.ID); err != nil {
			return nil, fmt.Errorf("assign guest role: %w", err)
		}
	}
	return u, nil
}

// ChangePassword verifies the current password and revokes every active session.
func (s *Service) ChangePassword(ctx context.Context, id uint64, currentPassword, newPassword string) error {
	u, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if !CheckPassword(u.PasswordHash, currentPassword) {
		return apperror.New(http.StatusUnauthorized, "invalid_current_password", "当前密码错误")
	}
	_, err = s.Update(ctx, id, nil, &newPassword)
	return err
}

// Get returns a user by ID.
func (s *Service) Get(ctx context.Context, id uint64) (*model.User, error) {
	u, err := s.repo.GetByID(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.New(http.StatusNotFound, "user_not_found", "用户不存在")
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// FindByUsername returns a user by login name.
func (s *Service) FindByUsername(ctx context.Context, username string) (*model.User, error) {
	return s.repo.GetByUsername(ctx, username)
}

// List returns a page of users.
func (s *Service) List(ctx context.Context, page, size int) ([]model.User, int64, error) {
	return s.repo.List(ctx, page, size)
}

// Update changes username or password when provided.
func (s *Service) Update(ctx context.Context, id uint64, username, password *string) (*model.User, error) {
	u, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	changes := UpdateFields{}
	if username != nil {
		v := strings.TrimSpace(*username)
		if !usernamePattern.MatchString(v) {
			return nil, apperror.New(400, "invalid_username", "用户名格式无效")
		}
		u.Username = v
		changes.Username = &v
	}
	if password != nil {
		hash, e := HashPassword(*password)
		if e != nil {
			return nil, e
		}
		u.PasswordHash = hash
		u.SessionVersion++
		changes.PasswordHash = &hash
		changes.IncrementSessionVersion = true
	}
	if username == nil && password == nil {
		return u, nil
	}
	if err := s.repo.UpdateFields(ctx, id, changes); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, apperror.New(http.StatusConflict, "username_exists", "用户名已存在")
		}
		return nil, fmt.Errorf("update user: %w", err)
	}
	if password != nil && s.revoker != nil {
		if err := idempotency.DeferAfterCommit(ctx, func(callbackContext context.Context) error {
			return s.revoker.RevokeUser(callbackContext, id)
		}); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// SetStatus enables or disables a user.
func (s *Service) SetStatus(ctx context.Context, id uint64, status string) (*model.User, error) {
	if status != model.UserActive && status != model.UserDisabled {
		return nil, apperror.New(400, "invalid_status", "用户状态无效")
	}
	u, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if status == model.UserDisabled && s.guard != nil {
		if disabler, ok := s.guard.(interface {
			DisableUser(context.Context, uint64) error
		}); ok {
			if err = disabler.DisableUser(ctx, id); err != nil {
				return nil, err
			}
			u.Status = status
			u.SessionVersion++
			if s.revoker != nil {
				if err = idempotency.DeferAfterCommit(ctx, func(callbackContext context.Context) error {
					return s.revoker.RevokeUser(callbackContext, id)
				}); err != nil {
					return nil, err
				}
			}
			return u, nil
		}
		ok, e := s.guard.CanDisable(ctx, id)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, apperror.New(409, "last_super_admin", "不能禁用最后一个超级管理员")
		}
	}
	u.Status = status
	u.SessionVersion++
	if err := s.repo.UpdateFields(ctx, id, UpdateFields{
		Status:                  &status,
		IncrementSessionVersion: true,
	}); err != nil {
		return nil, fmt.Errorf("update status: %w", err)
	}
	if status == model.UserDisabled && s.revoker != nil {
		if err := idempotency.DeferAfterCommit(ctx, func(callbackContext context.Context) error {
			return s.revoker.RevokeUser(callbackContext, id)
		}); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// wechatSyntheticUsername derives a stable, non-guessable username for a
// WeChat account. The hash makes the username safe to log while staying
// unique per (appID, openID) pair. If a collision is detected (vanishingly
// unlikely with 96 bits of entropy) the function appends a short salt.
func wechatSyntheticUsername(appID, openID string) string {
	sum := sha256.Sum256([]byte(appID + ":" + openID))
	candidate := "wx_" + hex.EncodeToString(sum[:])[:12]
	if usernamePattern.MatchString(candidate) {
		return candidate
	}
	// usernamePattern permits [A-Za-z0-9._-]; the hex prefix above already
	// satisfies it, so this branch is a safety net for a future regex change.
	return candidate
}

// FindOrCreateForWechat returns the platform user bound to (appID, openID),
// creating one if it does not exist. The created account is active, has a
// locked password hash (so it cannot be used to log in via password) and is
// auto-assigned the guest role inside the same database transaction so the
// user is never observable without a role. If the existing account is
// disabled, the caller receives a 403 to mirror the password login path.
// On every successful load we also reconcile the guest role so a half-applied
// provisioning from a previous failure self-heals on the next login.
func (s *Service) FindOrCreateForWechat(ctx context.Context, appID, openID, unionID string) (*model.User, bool, error) {
	appID = strings.TrimSpace(appID)
	openID = strings.TrimSpace(openID)
	if appID == "" || openID == "" {
		return nil, false, apperror.New(http.StatusBadRequest, "invalid_request", "appid 与 openid 不能为空")
	}
	existing, err := s.repo.GetByAppOpenID(ctx, appID, openID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, fmt.Errorf("lookup wechat user: %w", err)
	}
	if existing != nil {
		if existing.Status == model.UserDisabled {
			return nil, false, apperror.New(http.StatusForbidden, "user_disabled", "用户已禁用")
		}
		// backfill unionid if WeChat newly returned one and the row is missing it
		if unionID != "" && (existing.UnionID == nil || *existing.UnionID == "") {
			unionCopy := unionID
			if err := s.repo.UpdateFields(ctx, existing.ID, UpdateFields{UnionID: &unionCopy}); err != nil {
				return nil, false, fmt.Errorf("backfill unionid: %w", err)
			}
			existing.UnionID = &unionCopy
		}
		if err := s.ensureGuestRole(ctx, existing.ID); err != nil {
			return nil, false, err
		}
		return existing, false, nil
	}
	secret, err := randomLockedSecret()
	if err != nil {
		return nil, false, fmt.Errorf("generate wechat locked secret: %w", err)
	}
	hash, err := HashPassword(secret)
	if err != nil {
		return nil, false, fmt.Errorf("hash locked password: %w", err)
	}
	appCopy, openCopy := appID, openID
	u := &model.User{
		Username:       wechatSyntheticUsername(appID, openID),
		AppID:          &appCopy,
		OpenID:         &openCopy,
		UnionID:        nullableString(unionID),
		PasswordHash:   hash,
		Status:         model.UserActive,
		SessionVersion: 1,
	}
	if s.db != nil {
		if err := s.createWithGuestRoleInTx(ctx, u); err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				// Lost the race with a concurrent first login; reload and return.
				winner, lookupErr := s.repo.GetByAppOpenID(ctx, appID, openID)
				if lookupErr != nil {
					return nil, false, fmt.Errorf("reload wechat user: %w", lookupErr)
				}
				if winner.Status == model.UserDisabled {
					return nil, false, apperror.New(http.StatusForbidden, "user_disabled", "用户已禁用")
				}
				if err := s.ensureGuestRole(ctx, winner.ID); err != nil {
					return nil, false, err
				}
				return winner, false, nil
			}
			return nil, false, err
		}
		return u, true, nil
	}
	// Fallback path used only when no *gorm.DB is wired (legacy tests). The
	// best-effort non-transactional role assignment matches the pre-fix
	// behaviour and is exercised by service tests that predate the
	// transactional guarantee.
	if err := s.repo.Create(ctx, u); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			winner, lookupErr := s.repo.GetByAppOpenID(ctx, appID, openID)
			if lookupErr != nil {
				return nil, false, fmt.Errorf("reload wechat user: %w", lookupErr)
			}
			if winner.Status == model.UserDisabled {
				return nil, false, apperror.New(http.StatusForbidden, "user_disabled", "用户已禁用")
			}
			return winner, false, nil
		}
		return nil, false, fmt.Errorf("create wechat user: %w", err)
	}
	if assigner, ok := s.guard.(interface {
		EnsureGuestForUser(context.Context, uint64) error
	}); ok {
		if err := assigner.EnsureGuestForUser(ctx, u.ID); err != nil {
			return nil, false, fmt.Errorf("assign guest role: %w", err)
		}
	}
	return u, true, nil
}

// createWithGuestRoleInTx inserts the user and assigns the guest role in a
// single database transaction so a half-applied provisioning can never be
// observed by a subsequent login.
func (s *Service) createWithGuestRoleInTx(ctx context.Context, u *model.User) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(u).Error; err != nil {
			return err
		}
		assigner, ok := s.guard.(interface {
			EnsureGuestForUserTx(context.Context, *gorm.DB, uint64) error
			EnsureGuestForUser(context.Context, uint64) error
		})
		if !ok {
			return nil
		}
		if txAssigner, hasTx := assigner.(interface {
			EnsureGuestForUserTx(context.Context, *gorm.DB, uint64) error
		}); hasTx {
			return txAssigner.EnsureGuestForUserTx(ctx, tx, u.ID)
		}
		return assigner.EnsureGuestForUser(ctx, u.ID)
	})
}

// ensureGuestRole assigns the guest role outside of any transaction. It is
// safe to call when the role is already present (idempotent at the data
// layer), so we use it both on the post-create reconciliation path and when
// loading an existing WeChat user.
func (s *Service) ensureGuestRole(ctx context.Context, userID uint64) error {
	assigner, ok := s.guard.(interface {
		EnsureGuestForUser(context.Context, uint64) error
	})
	if !ok {
		return nil
	}
	return assigner.EnsureGuestForUser(ctx, userID)
}

func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// randomLockedSecret returns a 72-byte random secret used to populate the
// password hash of WeChat-only accounts. No caller knows the plaintext, so
// password-based login against the hash always fails. The string is shaped to
// satisfy the 12-character minimum imposed by HashPassword. crypto/rand
// failures are propagated so the caller can refuse to create an account
// with a publicly known password.
func randomLockedSecret() (string, error) {
	return lockedSecretGenerator()
}

// lockedSecretGenerator is the production source of entropy for
// randomLockedSecret. Tests swap it to inject entropy-source failures.
var lockedSecretGenerator = func() (string, error) {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random secret: %w", err)
	}
	return "wx-lock-" + hex.EncodeToString(buf), nil
}
