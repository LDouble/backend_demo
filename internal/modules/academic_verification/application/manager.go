// Package application coordinates academic verification without exposing credentials to persistence.
package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/academic_verification/domain"
)

// ErrInvalidCredentials is deliberately indistinguishable for unknown accounts and bad passwords.
var ErrInvalidCredentials = errors.New("invalid academic credentials")

// ErrProviderUnavailable indicates a retryable provider failure.
var ErrProviderUnavailable = errors.New("academic provider unavailable")

// Provider verifies credentials and returns the authoritative real name.
type Provider interface {
	Verify(context.Context, string, string) (string, error)
}

// FailureLimiter bounds credential guessing by user, student-number digest and client IP.
type FailureLimiter interface {
	Allow(context.Context, uint64, string, string) (bool, error)
	RecordFailure(context.Context, uint64, string, string) error
	Clear(context.Context, uint64, string, string) error
}

// MaterialStore encrypts private material outside the database.
type MaterialStore interface {
	Save(context.Context, []byte, string) (string, error)
	Open(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

// Store owns persistence and role changes that must share one transaction.
type Store interface {
	Status(context.Context, uint64) (domain.Status, error)
	IsVerified(context.Context, uint64) (bool, error)
	FindAvailableMaterial(context.Context, uint64, string, time.Time) (*domain.AcademicVerificationMaterial, error)
	CreateMaterial(context.Context, *domain.AcademicVerificationMaterial) error
	SubmitStudentCard(context.Context, uint64, string, string, uint64, time.Time) (*domain.AcademicVerificationRequest, error)
	VerifyCredentials(context.Context, uint64, string, string, time.Time) (*domain.AcademicVerificationRequest, error)
	ListRequests(context.Context, string, int, int) ([]domain.AcademicVerificationRequest, int64, error)
	GetRequest(context.Context, uint64) (*domain.AcademicVerificationRequest, error)
	GetIdentity(context.Context, uint64) (*domain.AcademicIdentity, error)
	GetMaterial(context.Context, uint64) (*domain.AcademicVerificationMaterial, error)
	Approve(context.Context, uint64, uint64, uint64, time.Time) (*domain.AcademicVerificationRequest, error)
	Reject(context.Context, uint64, uint64, uint64, string, time.Time) (*domain.AcademicVerificationRequest, error)
	Revoke(context.Context, uint64, uint64, uint64, string, time.Time) (*domain.AcademicIdentity, error)
	ClaimCleanup(context.Context, time.Time, int) ([]domain.AcademicVerificationMaterial, error)
	CompleteCleanup(context.Context, uint64, time.Time) error
	ReleaseCleanup(context.Context, uint64) error
}

// Manager validates inputs and coordinates providers, encrypted files and transactional persistence.
type Manager struct {
	store     Store
	materials MaterialStore
	provider  Provider
	limiter   FailureLimiter
	now       func() time.Time
}

// NewManager creates an academic verification manager.
func NewManager(store Store, materials MaterialStore, provider Provider, limiter FailureLimiter) *Manager {
	return &Manager{store: store, materials: materials, provider: provider, limiter: limiter, now: time.Now}
}

// Status returns the current identity and latest request for one user.
func (m *Manager) Status(ctx context.Context, userID uint64) (domain.Status, error) {
	return m.store.Status(ctx, userID)
}

// IsVerified reports whether the user has an effective academic identity.
func (m *Manager) IsVerified(ctx context.Context, userID uint64) (bool, error) {
	return m.store.IsVerified(ctx, userID)
}

// Upload validates a bounded image and returns a naturally idempotent material row.
func (m *Manager) Upload(ctx context.Context, userID uint64, source io.Reader) (*domain.AcademicVerificationMaterial, error) {
	if source == nil {
		return nil, apperror.New(http.StatusBadRequest, "missing_material", "缺少学生证图片")
	}
	data, err := io.ReadAll(io.LimitReader(source, domain.MaxMaterialBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read academic material: %w", err)
	}
	if int64(len(data)) > domain.MaxMaterialBytes {
		return nil, apperror.New(http.StatusRequestEntityTooLarge, "material_too_large", "学生证图片不能超过 5 MiB")
	}
	mimeType, err := verifiedImageType(data)
	if err != nil {
		return nil, err
	}
	digestBytes := sha256.Sum256(data)
	digest := hex.EncodeToString(digestBytes[:])
	now := m.now().UTC()
	existing, err := m.store.FindAvailableMaterial(ctx, userID, digest, now)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	storageKey, err := m.materials.Save(ctx, data, mimeType)
	if err != nil {
		return nil, fmt.Errorf("store academic material: %w", err)
	}
	material := &domain.AcademicVerificationMaterial{
		UserId: userID, StorageKey: storageKey, MimeType: mimeType,
		SizeBytes: int64(len(data)), Sha256: digest, Status: domain.MaterialAvailable,
		ExpiresAt: now.Add(domain.UnboundMaterialTTL), Version: 1,
	}
	if err = m.store.CreateMaterial(ctx, material); err != nil {
		_ = m.materials.Delete(ctx, storageKey)
		return nil, err
	}
	return material, nil
}

// SubmitStudentCard creates a pending manual-review request.
func (m *Manager) SubmitStudentCard(
	ctx context.Context,
	userID uint64,
	realName string,
	studentNo string,
	materialID uint64,
) (*domain.AcademicVerificationRequest, error) {
	realName, studentNo, err := validateIdentityInput(realName, studentNo)
	if err != nil {
		return nil, err
	}
	return m.store.SubmitStudentCard(ctx, userID, realName, studentNo, materialID, m.now().UTC())
}

// VerifyCredentials performs a bounded provider call and never passes the password to persistence.
func (m *Manager) VerifyCredentials(
	ctx context.Context,
	userID uint64,
	studentNo string,
	password string,
	clientIP string,
) (*domain.AcademicVerificationRequest, error) {
	studentNo = strings.TrimSpace(studentNo)
	if studentNo == "" || len(studentNo) > 64 || password == "" || len(password) > 256 {
		return nil, apperror.New(http.StatusUnauthorized, "invalid_academic_credentials", "教务凭据无效")
	}
	allowed, err := m.limiter.Allow(ctx, userID, studentNo, clientIP)
	if err != nil {
		return nil, fmt.Errorf("check academic credential limit: %w", err)
	}
	if !allowed {
		return nil, apperror.New(http.StatusTooManyRequests, "academic_credentials_limited", "尝试次数过多，请稍后再试")
	}
	providerContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	realName, err := m.provider.Verify(providerContext, studentNo, password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			if recordErr := m.limiter.RecordFailure(ctx, userID, studentNo, clientIP); recordErr != nil {
				return nil, fmt.Errorf("record academic credential failure: %w", recordErr)
			}
			return nil, apperror.New(http.StatusUnauthorized, "invalid_academic_credentials", "教务凭据无效")
		}
		return nil, apperror.New(http.StatusServiceUnavailable, "academic_provider_unavailable", "教务认证服务暂不可用")
	}
	if err = m.limiter.Clear(ctx, userID, studentNo, clientIP); err != nil {
		return nil, fmt.Errorf("clear academic credential failures: %w", err)
	}
	request, err := m.store.VerifyCredentials(ctx, userID, strings.TrimSpace(realName), studentNo, m.now().UTC())
	if err != nil {
		return nil, err
	}
	return request, nil
}

// ListRequests returns a normalized administrator page.
func (m *Manager) ListRequests(ctx context.Context, status string, page, pageSize int) ([]domain.AcademicVerificationRequest, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return m.store.ListRequests(ctx, strings.TrimSpace(status), page, pageSize)
}

// GetRequest returns one review request.
func (m *Manager) GetRequest(ctx context.Context, id uint64) (*domain.AcademicVerificationRequest, error) {
	return m.store.GetRequest(ctx, id)
}

// GetIdentity returns the current identity for administrator revocation.
func (m *Manager) GetIdentity(ctx context.Context, userID uint64) (*domain.AcademicIdentity, error) {
	return m.store.GetIdentity(ctx, userID)
}

// OpenMaterial decrypts a material after authorization has already succeeded.
func (m *Manager) OpenMaterial(ctx context.Context, id uint64) (domain.MaterialContent, error) {
	material, err := m.store.GetMaterial(ctx, id)
	if err != nil {
		return domain.MaterialContent{}, err
	}
	if material.Status == domain.MaterialDeleted || material.DeletedAt != nil {
		return domain.MaterialContent{}, apperror.New(http.StatusGone, "material_deleted", "认证材料已按保留策略删除")
	}
	data, err := m.materials.Open(ctx, material.StorageKey)
	if err != nil {
		return domain.MaterialContent{}, fmt.Errorf("open academic material: %w", err)
	}
	return domain.MaterialContent{MIMEType: material.MimeType, Data: data}, nil
}

// Approve accepts one pending request with optimistic locking.
func (m *Manager) Approve(ctx context.Context, id, adminID, version uint64) (*domain.AcademicVerificationRequest, error) {
	return m.store.Approve(ctx, id, adminID, version, m.now().UTC())
}

// Reject rejects one pending request and requires a reason.
func (m *Manager) Reject(ctx context.Context, id, adminID, version uint64, reason string) (*domain.AcademicVerificationRequest, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, apperror.New(http.StatusBadRequest, "review_reason_required", "驳回原因不能为空")
	}
	return m.store.Reject(ctx, id, adminID, version, reason, m.now().UTC())
}

// Revoke removes an effective identity and returns the user to guest.
func (m *Manager) Revoke(ctx context.Context, id, adminID, version uint64, reason string) (*domain.AcademicIdentity, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, apperror.New(http.StatusBadRequest, "revoke_reason_required", "撤销原因不能为空")
	}
	return m.store.Revoke(ctx, id, adminID, version, reason, m.now().UTC())
}

// CleanupExpired deletes exclusively claimed encrypted files and retains metadata.
func (m *Manager) CleanupExpired(ctx context.Context) (int64, error) {
	now := m.now().UTC()
	materials, err := m.store.ClaimCleanup(ctx, now, 100)
	if err != nil {
		return 0, err
	}
	var cleaned int64
	for _, material := range materials {
		if err = m.materials.Delete(ctx, material.StorageKey); err != nil {
			_ = m.store.ReleaseCleanup(ctx, material.ID)
			return cleaned, fmt.Errorf("delete academic material: %w", err)
		}
		if err = m.store.CompleteCleanup(ctx, material.ID, now); err != nil {
			return cleaned, err
		}
		cleaned++
	}
	return cleaned, nil
}

func verifiedImageType(data []byte) (string, error) {
	if len(data) == 0 {
		return "", apperror.New(http.StatusBadRequest, "empty_material", "学生证图片不能为空")
	}
	mimeType := http.DetectContentType(data)
	switch mimeType {
	case "image/jpeg", "image/png", "image/webp":
		return mimeType, nil
	default:
		return "", apperror.New(http.StatusBadRequest, "unsupported_material_type", "仅支持 JPEG、PNG 或 WebP 图片")
	}
}

func validateIdentityInput(realName, studentNo string) (string, string, error) {
	realName = strings.TrimSpace(realName)
	studentNo = strings.TrimSpace(studentNo)
	invalid := realName == "" || len(realName) > 100 || studentNo == "" || len(studentNo) > 64
	if invalid || strings.IndexFunc(realName+studentNo, unicode.IsControl) >= 0 {
		return "", "", apperror.New(http.StatusBadRequest, "invalid_academic_identity", "姓名或学号格式无效")
	}
	return realName, studentNo, nil
}
