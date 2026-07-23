// Package infrastructure persists academic verification state with transactional role changes.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	platformquery "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"github.com/weouc-plus/campus-platform/internal/modules/academic_verification/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BaseRoleManager changes only the authentication-derived guest/member role.
type BaseRoleManager interface {
	EnsureGuestForUser(context.Context, uint64) error
	EnsureMemberForUser(context.Context, uint64) error
}

// Store is the transactional academic-verification repository.
type Store struct {
	db    *gorm.DB
	roles BaseRoleManager
}

// NewStore creates an academic verification store.
func NewStore(db *gorm.DB, roles BaseRoleManager) *Store { return &Store{db: db, roles: roles} }

// Status returns the effective identity and latest request.
func (s *Store) Status(ctx context.Context, userID uint64) (domain.Status, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db))
	status := domain.Status{}
	identity, err := q.AcademicIdentity.WithContext(ctx).
		Where(q.AcademicIdentity.UserId.Eq(userID)).First()
	if err == nil {
		status.Identity = identity
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Status{}, fmt.Errorf("get academic identity: %w", err)
	}
	request, err := q.AcademicVerificationRequest.WithContext(ctx).
		Where(q.AcademicVerificationRequest.UserId.Eq(userID)).
		Order(q.AcademicVerificationRequest.ID.Desc()).First()
	if err == nil {
		status.LatestRequest = request
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Status{}, fmt.Errorf("get latest academic request: %w", err)
	}
	return status, nil
}

// IsVerified reports whether a verified identity exists.
func (s *Store) IsVerified(ctx context.Context, userID uint64) (bool, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).AcademicIdentity
	count, err := q.WithContext(ctx).Where(
		q.UserId.Eq(userID),
		q.Status.Eq(domain.IdentityVerified),
	).Count()
	return count > 0, err
}

// FindAvailableMaterial returns a reusable upload token for identical content.
func (s *Store) FindAvailableMaterial(
	ctx context.Context,
	userID uint64,
	digest string,
	now time.Time,
) (*domain.AcademicVerificationMaterial, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).AcademicVerificationMaterial
	row, err := q.WithContext(ctx).Where(
		q.UserId.Eq(userID),
		q.Sha256.Eq(digest),
		q.Status.Eq(domain.MaterialAvailable),
		q.ExpiresAt.Gt(now),
	).Order(q.ID.Desc()).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return row, err
}

// CreateMaterial inserts encrypted-file metadata.
func (s *Store) CreateMaterial(ctx context.Context, material *domain.AcademicVerificationMaterial) error {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).AcademicVerificationMaterial
	if err := q.WithContext(ctx).Create(material); err != nil {
		return fmt.Errorf("create academic material: %w", err)
	}
	return nil
}

// SubmitStudentCard atomically consumes one material and creates a pending request.
func (s *Store) SubmitStudentCard(
	ctx context.Context,
	userID uint64,
	realName string,
	studentNo string,
	materialID uint64,
	now time.Time,
) (*domain.AcademicVerificationRequest, error) {
	var result *domain.AcademicVerificationRequest
	err := s.transaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		q := platformquery.Use(tx)
		material, err := q.AcademicVerificationMaterial.WithContext(txCtx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(q.AcademicVerificationMaterial.ID.Eq(materialID)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(http.StatusConflict, "material_unavailable", "认证材料不可用")
		}
		if err != nil {
			return err
		}
		available := material.UserId == userID && material.Status == domain.MaterialAvailable && material.ExpiresAt.After(now)
		if !available {
			return apperror.New(http.StatusConflict, "material_unavailable", "认证材料不可用")
		}
		request := &domain.AcademicVerificationRequest{
			UserId: userID, StudentNo: studentNo, RealName: realName,
			Method: domain.MethodStudentCard, MaterialId: &material.ID,
			Status: domain.RequestPending, Version: 1,
		}
		if err = q.AcademicVerificationRequest.WithContext(txCtx).Create(request); err != nil {
			return fmt.Errorf("create academic request: %w", err)
		}
		_, err = q.AcademicVerificationMaterial.WithContext(txCtx).
			Where(q.AcademicVerificationMaterial.ID.Eq(material.ID), q.AcademicVerificationMaterial.Version.Eq(material.Version)).
			UpdateSimple(
				q.AcademicVerificationMaterial.Status.Value(domain.MaterialBound),
				q.AcademicVerificationMaterial.BoundRequestId.Value(request.ID),
				q.AcademicVerificationMaterial.Version.Add(1),
			)
		if err != nil {
			return fmt.Errorf("bind academic material: %w", err)
		}
		result = request
		return nil
	})
	return result, err
}

// VerifyCredentials records only the successful result and atomically grants member.
func (s *Store) VerifyCredentials(
	ctx context.Context,
	userID uint64,
	realName string,
	studentNo string,
	now time.Time,
) (*domain.AcademicVerificationRequest, error) {
	var result *domain.AcademicVerificationRequest
	err := s.transaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		q := platformquery.Use(tx)
		request := &domain.AcademicVerificationRequest{
			UserId: userID, StudentNo: studentNo, RealName: realName,
			Method: domain.MethodCredentials, Status: domain.RequestApproved,
			ReviewedAt: &now, Version: 1,
		}
		if err := q.AcademicVerificationRequest.WithContext(txCtx).Create(request); err != nil {
			return fmt.Errorf("record credential verification: %w", err)
		}
		identity, err := s.replaceIdentity(txCtx, tx, userID, realName, studentNo, domain.MethodCredentials, now)
		if err != nil {
			return err
		}
		if err = s.supersedePending(txCtx, tx, userID, request.ID, now); err != nil {
			return err
		}
		if err = s.roles.EnsureMemberForUser(txCtx, userID); err != nil {
			return fmt.Errorf("grant member role: %w", err)
		}
		if err = writeVerificationEvent(tx, identity.ID, request.ID, userID, domain.RequestApproved, identity.Version); err != nil {
			return err
		}
		result = request
		return nil
	})
	return result, translateIdentityConflict(err)
}

// ListRequests returns administrator review history.
func (s *Store) ListRequests(
	ctx context.Context,
	status string,
	page int,
	pageSize int,
) ([]domain.AcademicVerificationRequest, int64, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).AcademicVerificationRequest
	dao := q.WithContext(ctx)
	if status != "" {
		dao = dao.Where(q.Status.Eq(status))
	}
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	rows, err := dao.Order(q.ID.Desc()).Offset((page - 1) * pageSize).Limit(pageSize).Find()
	if err != nil {
		return nil, 0, err
	}
	result := make([]domain.AcademicVerificationRequest, 0, len(rows))
	for _, row := range rows {
		result = append(result, *row)
	}
	return result, total, nil
}

// GetRequest returns one administrator-visible request.
func (s *Store) GetRequest(ctx context.Context, id uint64) (*domain.AcademicVerificationRequest, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).AcademicVerificationRequest
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.New(http.StatusNotFound, "academic_request_not_found", "认证申请不存在")
	}
	return row, err
}

// GetIdentity returns the current identity for one user without exposing storage metadata.
func (s *Store) GetIdentity(ctx context.Context, userID uint64) (*domain.AcademicIdentity, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).AcademicIdentity
	identity, err := q.WithContext(ctx).Where(q.UserId.Eq(userID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.New(http.StatusNotFound, "academic_identity_not_found", "教务身份不存在")
	}
	return identity, err
}

// GetMaterial returns private metadata; storage keys must never be mapped into API JSON.
func (s *Store) GetMaterial(ctx context.Context, id uint64) (*domain.AcademicVerificationMaterial, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).AcademicVerificationMaterial
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.New(http.StatusNotFound, "academic_material_not_found", "认证材料不存在")
	}
	return row, err
}

// Approve accepts a pending request and atomically replaces the identity.
func (s *Store) Approve(
	ctx context.Context,
	id uint64,
	adminID uint64,
	version uint64,
	now time.Time,
) (*domain.AcademicVerificationRequest, error) {
	var result *domain.AcademicVerificationRequest
	err := s.transaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		q := platformquery.Use(tx)
		request, err := q.AcademicVerificationRequest.WithContext(txCtx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(q.AcademicVerificationRequest.ID.Eq(id)).First()
		if err := reviewableRequest(request, err, version); err != nil {
			return err
		}
		identity, err := s.replaceIdentity(
			txCtx,
			tx,
			request.UserId,
			request.RealName,
			request.StudentNo,
			domain.MethodStudentCard,
			now,
		)
		if err != nil {
			return err
		}
		newVersion := request.Version + 1
		_, err = q.AcademicVerificationRequest.WithContext(txCtx).
			Where(q.AcademicVerificationRequest.ID.Eq(id), q.AcademicVerificationRequest.Version.Eq(version)).
			UpdateSimple(
				q.AcademicVerificationRequest.Status.Value(domain.RequestApproved),
				q.AcademicVerificationRequest.ReviewedBy.Value(adminID),
				q.AcademicVerificationRequest.ReviewedAt.Value(now),
				q.AcademicVerificationRequest.Version.Value(newVersion),
			)
		if err != nil {
			return err
		}
		if err = s.supersedePending(txCtx, tx, request.UserId, request.ID, now); err != nil {
			return err
		}
		if request.MaterialId != nil {
			deleteAfter := now.Add(domain.ReviewedMaterialRetention)
			_, err = q.AcademicVerificationMaterial.WithContext(txCtx).
				Where(q.AcademicVerificationMaterial.ID.Eq(*request.MaterialId)).
				UpdateSimple(q.AcademicVerificationMaterial.DeleteAfter.Value(deleteAfter))
			if err != nil {
				return err
			}
		}
		if err = s.roles.EnsureMemberForUser(txCtx, request.UserId); err != nil {
			return fmt.Errorf("grant member role: %w", err)
		}
		if err = writeVerificationEvent(tx, identity.ID, request.ID, request.UserId, domain.RequestApproved, identity.Version); err != nil {
			return err
		}
		request.Status = domain.RequestApproved
		request.ReviewedBy = &adminID
		request.ReviewedAt = &now
		request.Version = newVersion
		result = request
		return nil
	})
	return result, translateIdentityConflict(err)
}

// Reject records a reason and schedules the bound material for retention cleanup.
func (s *Store) Reject(
	ctx context.Context,
	id uint64,
	adminID uint64,
	version uint64,
	reason string,
	now time.Time,
) (*domain.AcademicVerificationRequest, error) {
	var result *domain.AcademicVerificationRequest
	err := s.transaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		q := platformquery.Use(tx)
		request, err := q.AcademicVerificationRequest.WithContext(txCtx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(q.AcademicVerificationRequest.ID.Eq(id)).First()
		if err := reviewableRequest(request, err, version); err != nil {
			return err
		}
		newVersion := request.Version + 1
		_, err = q.AcademicVerificationRequest.WithContext(txCtx).
			Where(q.AcademicVerificationRequest.ID.Eq(id), q.AcademicVerificationRequest.Version.Eq(version)).
			UpdateSimple(
				q.AcademicVerificationRequest.Status.Value(domain.RequestRejected),
				q.AcademicVerificationRequest.ReviewReason.Value(reason),
				q.AcademicVerificationRequest.ReviewedBy.Value(adminID),
				q.AcademicVerificationRequest.ReviewedAt.Value(now),
				q.AcademicVerificationRequest.Version.Value(newVersion),
			)
		if err != nil {
			return err
		}
		if request.MaterialId != nil {
			deleteAfter := now.Add(domain.ReviewedMaterialRetention)
			_, err = q.AcademicVerificationMaterial.WithContext(txCtx).
				Where(q.AcademicVerificationMaterial.ID.Eq(*request.MaterialId)).
				UpdateSimple(q.AcademicVerificationMaterial.DeleteAfter.Value(deleteAfter))
			if err != nil {
				return err
			}
		}
		if err = writeVerificationEvent(tx, request.ID, request.ID, request.UserId, domain.RequestRejected, newVersion); err != nil {
			return err
		}
		request.Status = domain.RequestRejected
		request.ReviewReason = &reason
		request.ReviewedBy = &adminID
		request.ReviewedAt = &now
		request.Version = newVersion
		result = request
		return nil
	})
	return result, err
}

// Revoke removes the effective identity and switches its owner back to guest.
func (s *Store) Revoke(
	ctx context.Context,
	id uint64,
	adminID uint64,
	version uint64,
	reason string,
	now time.Time,
) (*domain.AcademicIdentity, error) {
	_ = adminID
	var result *domain.AcademicIdentity
	err := s.transaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		q := platformquery.Use(tx).AcademicIdentity
		identity, err := q.WithContext(txCtx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.ID.Eq(id)).First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(http.StatusNotFound, "academic_identity_not_found", "教务身份不存在")
		}
		if err != nil {
			return err
		}
		if identity.Status != domain.IdentityVerified || identity.Version != version {
			return apperror.New(http.StatusConflict, "academic_identity_conflict", "教务身份状态或版本已变化")
		}
		newVersion := identity.Version + 1
		_, err = q.WithContext(txCtx).Where(q.ID.Eq(id), q.Version.Eq(version)).UpdateSimple(
			q.Status.Value(domain.IdentityRevoked),
			q.RevokedAt.Value(now),
			q.RevokeReason.Value(reason),
			q.Version.Value(newVersion),
		)
		if err != nil {
			return err
		}
		requests := platformquery.Use(tx).AcademicVerificationRequest
		request, err := requests.WithContext(txCtx).Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(
				requests.UserId.Eq(identity.UserId),
				requests.Status.Eq(domain.RequestApproved),
			).
			Order(requests.ID.Desc()).
			First()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(http.StatusConflict, "academic_request_conflict", "认证申请状态已变化")
		}
		if err != nil {
			return err
		}
		_, err = requests.WithContext(txCtx).Where(
			requests.ID.Eq(request.ID),
			requests.Version.Eq(request.Version),
		).UpdateSimple(
			requests.Status.Value(domain.RequestRevoked),
			requests.ReviewReason.Value(reason),
			requests.ReviewedBy.Value(adminID),
			requests.ReviewedAt.Value(now),
			requests.Version.Add(1),
		)
		if err != nil {
			return err
		}
		if err = s.roles.EnsureGuestForUser(txCtx, identity.UserId); err != nil {
			return fmt.Errorf("restore guest role: %w", err)
		}
		if err = writeVerificationEvent(tx, identity.ID, 0, identity.UserId, domain.IdentityRevoked, newVersion); err != nil {
			return err
		}
		identity.Status = domain.IdentityRevoked
		identity.RevokedAt = &now
		identity.RevokeReason = &reason
		identity.Version = newVersion
		result = identity
		return nil
	})
	return result, err
}

// ClaimCleanup gives one worker exclusive ownership of due materials.
func (s *Store) ClaimCleanup(ctx context.Context, now time.Time, limit int) ([]domain.AcademicVerificationMaterial, error) {
	claimed := []domain.AcademicVerificationMaterial{}
	err := s.transaction(ctx, func(txCtx context.Context, tx *gorm.DB) error {
		q := platformquery.Use(tx).AcademicVerificationMaterial
		// Generated predicates cannot group the retention clocks and stale-lease condition,
		// so this repository-scoped clause is parameterized.
		rows := []domain.AcademicVerificationMaterial{}
		err := q.WithContext(txCtx).UnderlyingDB().
			Where(
				"(status = ? AND expires_at <= ?) OR "+
					"(status = ? AND delete_after IS NOT NULL AND delete_after <= ?) OR "+
					"(status = ? AND updated_at <= ?)",
				domain.MaterialAvailable,
				now,
				domain.MaterialBound,
				now,
				domain.MaterialDeleting,
				now.Add(-domain.CleanupClaimLease),
			).
			Clauses(clause.OrderBy{Columns: []clause.OrderByColumn{{Column: clause.Column{Name: "id"}}}}).
			Limit(limit).
			Find(&rows).Error
		if err != nil {
			return err
		}
		for i := range rows {
			row := &rows[i]
			result, updateErr := q.WithContext(txCtx).
				Where(q.ID.Eq(row.ID), q.Status.Eq(row.Status), q.Version.Eq(row.Version)).
				UpdateSimple(
					q.Status.Value(domain.MaterialDeleting),
					q.UpdatedAt.Value(now),
					q.Version.Add(1),
				)
			if updateErr != nil {
				return updateErr
			}
			if result.RowsAffected == 1 {
				row.Status = domain.MaterialDeleting
				row.UpdatedAt = now
				row.Version++
				claimed = append(claimed, *row)
			}
		}
		return nil
	})
	return claimed, err
}

// CompleteCleanup retains metadata after encrypted content deletion.
func (s *Store) CompleteCleanup(ctx context.Context, id uint64, now time.Time) error {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).AcademicVerificationMaterial
	result, err := q.WithContext(ctx).Where(q.ID.Eq(id), q.Status.Eq(domain.MaterialDeleting)).UpdateSimple(
		q.Status.Value(domain.MaterialDeleted), q.DeletedAt.Value(now), q.Version.Add(1),
	)
	if err != nil {
		return err
	}
	if result.RowsAffected != 1 {
		return apperror.New(http.StatusConflict, "material_cleanup_conflict", "认证材料清理状态已变化")
	}
	return nil
}

// ReleaseCleanup restores a failed deletion claim for retry.
func (s *Store) ReleaseCleanup(ctx context.Context, id uint64) error {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).AcademicVerificationMaterial
	row, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if err != nil {
		return err
	}
	status := domain.MaterialAvailable
	if row.BoundRequestId != nil {
		status = domain.MaterialBound
	}
	_, err = q.WithContext(ctx).Where(q.ID.Eq(id), q.Status.Eq(domain.MaterialDeleting)).UpdateSimple(
		q.Status.Value(status), q.Version.Add(1),
	)
	return err
}

func (s *Store) replaceIdentity(
	ctx context.Context,
	tx *gorm.DB,
	userID uint64,
	realName string,
	studentNo string,
	method string,
	now time.Time,
) (*domain.AcademicIdentity, error) {
	q := platformquery.Use(tx).AcademicIdentity
	identity, err := q.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(q.UserId.Eq(userID)).First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		identity = &domain.AcademicIdentity{
			UserId: userID, StudentNo: studentNo, RealName: realName, Method: method,
			Status: domain.IdentityVerified, VerifiedAt: now, Version: 1,
		}
		if err = q.WithContext(ctx).Create(identity); err != nil {
			return nil, err
		}
		return identity, nil
	}
	if err != nil {
		return nil, err
	}
	newVersion := identity.Version + 1
	_, err = q.WithContext(ctx).Where(q.ID.Eq(identity.ID), q.Version.Eq(identity.Version)).UpdateSimple(
		q.StudentNo.Value(studentNo), q.RealName.Value(realName), q.Method.Value(method),
		q.Status.Value(domain.IdentityVerified), q.VerifiedAt.Value(now),
		q.RevokedAt.Null(), q.RevokeReason.Null(), q.Version.Value(newVersion),
	)
	if err != nil {
		return nil, err
	}
	identity.StudentNo = studentNo
	identity.RealName = realName
	identity.Method = method
	identity.Status = domain.IdentityVerified
	identity.VerifiedAt = now
	identity.RevokedAt = nil
	identity.RevokeReason = nil
	identity.Version = newVersion
	return identity, nil
}

func (s *Store) supersedePending(
	ctx context.Context,
	tx *gorm.DB,
	userID uint64,
	successfulRequestID uint64,
	now time.Time,
) error {
	queries := platformquery.Use(tx)
	requests := queries.AcademicVerificationRequest
	pending, err := requests.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		requests.UserId.Eq(userID),
		requests.Status.Eq(domain.RequestPending),
		requests.ID.Neq(successfulRequestID),
	).Find()
	if err != nil || len(pending) == 0 {
		return err
	}
	if _, err = requests.WithContext(ctx).Where(
		requests.UserId.Eq(userID),
		requests.Status.Eq(domain.RequestPending),
		requests.ID.Neq(successfulRequestID),
	).UpdateSimple(requests.Status.Value(domain.RequestSuperseded), requests.Version.Add(1)); err != nil {
		return err
	}

	materialIDs := make([]uint64, 0, len(pending))
	for _, request := range pending {
		if request.MaterialId != nil {
			materialIDs = append(materialIDs, *request.MaterialId)
		}
	}
	if len(materialIDs) == 0 {
		return nil
	}
	materials := queries.AcademicVerificationMaterial
	_, err = materials.WithContext(ctx).Where(
		materials.ID.In(materialIDs...),
		materials.Status.Eq(domain.MaterialBound),
	).UpdateSimple(materials.DeleteAfter.Value(now.Add(domain.ReviewedMaterialRetention)))
	return err
}

func (s *Store) transaction(
	ctx context.Context,
	work func(context.Context, *gorm.DB) error,
) error {
	if idempotency.InTransaction(ctx) {
		tx := idempotency.DB(ctx, s.db)
		return work(ctx, tx)
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return work(idempotency.WithTransaction(ctx, tx), tx)
	})
}

func reviewableRequest(request *domain.AcademicVerificationRequest, err error, version uint64) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(http.StatusNotFound, "academic_request_not_found", "认证申请不存在")
	}
	if err != nil {
		return err
	}
	if request.Status != domain.RequestPending || request.Version != version {
		return apperror.New(http.StatusConflict, "academic_request_conflict", "认证申请状态或版本已变化")
	}
	return nil
}

func translateIdentityConflict(err error) error {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return apperror.New(http.StatusConflict, "student_number_in_use", "该学号已绑定其他账号")
	}
	return err
}

func writeVerificationEvent(
	tx *gorm.DB,
	aggregateID uint64,
	requestID uint64,
	userID uint64,
	status string,
	version uint64,
) error {
	payload := domain.VerificationEvent{
		UserID: userID, RequestID: requestID, Status: status,
		ActionPath: "/api/v1/academic-verification", Version: version,
	}
	return domainevent.Write(tx, "academic_identity", aggregateID, "academic_verification."+status, payload)
}
