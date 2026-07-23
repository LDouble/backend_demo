// Package infrastructure persists activity aggregates with transactional rules.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/privacy"
	platformquery "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store persists activity aggregates and registration workflows.
type Store struct {
	db     *gorm.DB
	cipher *configcenter.Cipher
}

// NewStore creates an activity persistence adapter.
func NewStore(db *gorm.DB, ciphers ...*configcenter.Cipher) *Store {
	store := &Store{db: db}
	if len(ciphers) > 0 {
		store.cipher = ciphers[0]
	}
	return store
}

// Create inserts a draft activity and encrypts its contact details.
func (s *Store) Create(ctx context.Context, actorID uint64, input domain.ActivityInput) (*domain.Activity, error) {
	activity := &domain.Activity{
		Title:           strings.TrimSpace(input.Title),
		Summary:         strings.TrimSpace(input.Summary),
		Body:            strings.TrimSpace(input.Body),
		Location:        strings.TrimSpace(input.Location),
		SignupStartAt:   input.SignupStartAt.UTC(),
		SignupEndAt:     input.SignupEndAt.UTC(),
		StartAt:         input.StartAt.UTC(),
		EndAt:           input.EndAt.UTC(),
		Capacity:        input.Capacity,
		RegisteredCount: 0,
		Status:          domain.ActivityStatusDraft,
		ReviewStatus:    domain.ReviewStatusDraft,
		ContactType:     strings.TrimSpace(input.Contact.Type),
		CreatedBy:       actorID,
		UpdatedBy:       actorID,
		Version:         1,
	}
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(activity).Error; err != nil {
			return err
		}
		ciphertext, err := s.encryptContact(input.Contact.Value, activityContactAAD(activity.ID))
		if err != nil {
			return err
		}
		activity.ContactCiphertext = ciphertext
		if err := tx.Save(activity).Error; err != nil {
			return err
		}
		return activityEvent(tx, activity, "activity.created")
	})
	return activity, err
}

// Update changes an editable activity draft.
func (s *Store) Update(ctx context.Context, id, actorID, version uint64, input domain.ActivityInput, _ time.Time) (*domain.Activity, error) {
	return s.mutateActivityWithEvent(ctx, id, version, "activity.updated", func(tx *gorm.DB, activity *domain.Activity) error {
		if activity.CreatedBy != actorID {
			return apperror.New(http.StatusForbidden, "not_activity_owner", "仅活动发布者可以执行此操作")
		}
		if !domain.CanEdit(activity.Status, activity.ReviewStatus) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许编辑")
		}
		// A.3: capacity must not drop below the number of people already
		// registered; otherwise registered_count > capacity creates an
		// invariant no future code path can repair.
		if err := domain.ValidateCapacityUpdate(input.Capacity, activity.RegisteredCount); err != nil {
			return apperror.Wrap(http.StatusBadRequest, "capacity_below_registered", err.Error(), err)
		}
		activity.Title = strings.TrimSpace(input.Title)
		activity.Summary = strings.TrimSpace(input.Summary)
		activity.Body = strings.TrimSpace(input.Body)
		activity.Location = strings.TrimSpace(input.Location)
		activity.SignupStartAt = input.SignupStartAt.UTC()
		activity.SignupEndAt = input.SignupEndAt.UTC()
		activity.StartAt = input.StartAt.UTC()
		activity.EndAt = input.EndAt.UTC()
		activity.Capacity = input.Capacity
		activity.ReviewStatus = domain.ReviewStatusDraft
		activity.ReviewComment = nil
		activity.UpdatedBy = actorID
		activity.Version++
		if input.Contact.Provided {
			ciphertext, err := s.encryptContact(input.Contact.Value, activityContactAAD(activity.ID))
			if err != nil {
				return err
			}
			activity.ContactType = strings.TrimSpace(input.Contact.Type)
			activity.ContactCiphertext = ciphertext
		}
		return tx.Save(activity).Error
	})
}

// GetAdmin returns an activity without public visibility constraints.
func (s *Store) GetAdmin(ctx context.Context, id uint64) (*domain.Activity, error) {
	query := platformquery.Use(idempotency.DB(ctx, s.db)).Activity
	activity, err := query.WithContext(ctx).Where(query.ID.Eq(id)).First()
	return activity, activityNotFound(err)
}

// GetPublic returns one activity, applying VisibleToViewer so that the owner
// and active-registration viewers can still resolve cancelled/finished
// activities that they have a relationship to. Unrelated viewers see 404.
func (s *Store) GetPublic(ctx context.Context, id, viewerID uint64) (*domain.Activity, error) {
	query := platformquery.Use(idempotency.DB(ctx, s.db)).Activity
	activity, err := query.WithContext(ctx).Where(query.ID.Eq(id)).First()
	if err != nil {
		return nil, activityNotFound(err)
	}
	registered, err := s.IsViewerRegistered(ctx, viewerID, activity.ID)
	if err != nil {
		return nil, err
	}
	if !domain.VisibleToViewer(activity.Status, activity.ReviewStatus, activity.CreatedBy, viewerID, registered) {
		return nil, activityNotFound(gorm.ErrRecordNotFound)
	}
	return activity, nil
}

// IsViewerRegistered reports whether the supplied viewer holds an *active*
// registration row for the activity. Used by GetPublic to honour
// VisibleToViewer without the manager layer needing to issue its own query.
func (s *Store) IsViewerRegistered(ctx context.Context, viewerID, activityID uint64) (bool, error) {
	if viewerID == 0 {
		return false, nil
	}
	q := platformquery.Use(idempotency.DB(ctx, s.db)).ActivityRegistration
	count, err := q.WithContext(ctx).Where(
		q.ActivityId.Eq(activityID),
		q.UserId.Eq(viewerID),
		q.Status.Eq(domain.RegistrationStatusActive),
	).Count()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// IsViewerRegisteredBatch is the list-path companion of IsViewerRegistered: it
// returns the subset of activityIDs the viewer has active registrations for,
// collapsing N+1 queries into a single SELECT ... WHERE activity_id IN (...).
func (s *Store) IsViewerRegisteredBatch(ctx context.Context, viewerID uint64, activityIDs []uint64) (map[uint64]bool, error) {
	out := make(map[uint64]bool, len(activityIDs))
	if viewerID == 0 || len(activityIDs) == 0 {
		return out, nil
	}
	q := platformquery.Use(idempotency.DB(ctx, s.db)).ActivityRegistration
	rows, err := q.WithContext(ctx).Where(
		q.UserId.Eq(viewerID),
		q.Status.Eq(domain.RegistrationStatusActive),
		q.ActivityId.In(activityIDs...),
	).Find()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ActivityId] = true
	}
	return out, nil
}

// ListAdmin returns activities visible to administrators.
func (s *Store) ListAdmin(ctx context.Context, search domain.AdminSearch, page, pageSize int) ([]domain.Activity, int64, error) {
	dao := idempotency.DB(ctx, s.db).Model(&domain.Activity{})
	if status := strings.TrimSpace(search.Status); status != "" {
		dao = dao.Where("status = ?", status)
	}
	if review := strings.TrimSpace(search.ReviewStatus); review != "" {
		dao = dao.Where("review_status = ?", review)
	}
	dao = applyKeywordFilter(dao, strings.TrimSpace(search.Keyword))
	dao = applyStartDateFilter(dao, search.StartDate)
	var total int64
	if err := dao.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.Activity
	if err := dao.Order("start_at ASC, id DESC").Offset(offset(page, pageSize)).Limit(limit(pageSize)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ListPublic returns published and approved activities for user endpoints.
func (s *Store) ListPublic(ctx context.Context, search domain.PublicSearch, page, pageSize int) ([]domain.Activity, int64, error) {
	dao := idempotency.DB(ctx, s.db).Model(&domain.Activity{}).
		Where("status = ? AND review_status = ?", domain.ActivityStatusPublished, domain.ReviewStatusApproved)
	dao = applyKeywordFilter(dao, strings.TrimSpace(search.Keyword))
	dao = applyStartDateFilter(dao, search.StartDate)
	var total int64
	if err := dao.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.Activity
	if err := dao.Order("start_at ASC, id DESC").Offset(offset(page, pageSize)).Limit(limit(pageSize)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// SubmitReview moves an owned draft activity into pending review.
func (s *Store) SubmitReview(ctx context.Context, id, actorID, version uint64) (*domain.Activity, error) {
	return s.mutateActivityWithEvent(ctx, id, version, "activity.submitted_for_review", func(tx *gorm.DB, activity *domain.Activity) error {
		if activity.CreatedBy != actorID {
			return apperror.New(http.StatusForbidden, "not_activity_owner", "仅活动发布者可以执行此操作")
		}
		if !domain.CanSubmitReview(activity.Status, activity.ReviewStatus) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许送审")
		}
		activity.ReviewStatus = domain.ReviewStatusPendingReview
		activity.ReviewComment = nil
		activity.UpdatedBy = actorID
		activity.Version++
		return tx.Save(activity).Error
	})
}

// Approve marks a pending activity as approved and publishes it atomically.
func (s *Store) Approve(
	ctx context.Context,
	id uint64,
	actorID uint64,
	version uint64,
	comment string,
	now time.Time,
) (*domain.Activity, error) {
	return s.review(ctx, id, actorID, version, true, comment, now)
}

// Reject marks a pending activity as rejected.
func (s *Store) Reject(ctx context.Context, id, actorID, version uint64, comment string) (*domain.Activity, error) {
	return s.review(ctx, id, actorID, version, false, comment, time.Time{})
}

func (s *Store) review(
	ctx context.Context,
	id uint64,
	actorID uint64,
	version uint64,
	approved bool,
	comment string,
	now time.Time,
) (*domain.Activity, error) {
	kind := "activity.rejected"
	if approved {
		kind = "activity.approved"
	}
	return s.mutateActivityWithEvent(ctx, id, version, kind, func(tx *gorm.DB, activity *domain.Activity) error {
		if approved && !domain.CanApprove(activity.Status, activity.ReviewStatus) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许审核通过")
		}
		if !approved && !domain.CanReject(activity.Status, activity.ReviewStatus) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许驳回")
		}
		// A.5: review_comment must be non-empty on reject and ≤ 500 chars in
		// both approve and reject paths; the column is VARCHAR(500) and any
		// overflow raises Data too long for column without a graceful envelope.
		trimmed := strings.TrimSpace(comment)
		if !approved && trimmed == "" {
			return apperror.New(http.StatusBadRequest, "review_comment_required", "驳回意见不能为空")
		}
		if err := domain.ValidateReviewComment(trimmed); err != nil {
			return apperror.Wrap(http.StatusBadRequest, "review_comment_too_long", err.Error(), err)
		}
		if approved {
			if !activity.EndAt.After(now) {
				return apperror.New(http.StatusConflict, "activity_expired", "活动已结束，无法审核通过")
			}
			activity.Status = domain.ActivityStatusPublished
			activity.ReviewStatus = domain.ReviewStatusApproved
			if trimmed == "" {
				activity.ReviewComment = nil
			} else {
				activity.ReviewComment = &trimmed
			}
		} else {
			activity.ReviewStatus = domain.ReviewStatusRejected
			activity.ReviewComment = &trimmed
		}
		activity.UpdatedBy = actorID
		activity.Version++
		return tx.Save(activity).Error
	})
}

// Publish exposes an approved draft activity to users.
func (s *Store) Publish(ctx context.Context, id, actorID, version uint64, now time.Time) (*domain.Activity, error) {
	var activity domain.Activity
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&activity, id).Error; err != nil {
			return activityNotFound(err)
		}
		if activity.Version != version {
			return apperror.New(http.StatusConflict, "version_conflict", "活动已被其他请求更新")
		}
		if activity.CreatedBy != actorID {
			return apperror.New(http.StatusForbidden, "not_activity_owner", "仅活动发布者可以执行此操作")
		}
		alreadyPublished := activity.Status == domain.ActivityStatusPublished &&
			activity.ReviewStatus == domain.ReviewStatusApproved
		if alreadyPublished {
			return nil
		}
		if !domain.CanPublish(activity.Status, activity.ReviewStatus) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许发布")
		}
		if !activity.EndAt.After(now) {
			return apperror.New(http.StatusConflict, "activity_expired", "活动已结束，无法发布")
		}
		activity.Status = domain.ActivityStatusPublished
		activity.UpdatedBy = actorID
		activity.Version++
		if err := tx.Save(&activity).Error; err != nil {
			return err
		}
		return activityEvent(tx, &activity, "activity.published")
	})
	return &activity, err
}

// Cancel marks a published activity cancelled.
func (s *Store) Cancel(ctx context.Context, id, actorID, version uint64, _ time.Time) (*domain.Activity, error) {
	return s.mutateActivityWithEvent(ctx, id, version, "activity.cancelled", func(tx *gorm.DB, activity *domain.Activity) error {
		if activity.CreatedBy != actorID {
			return apperror.New(http.StatusForbidden, "not_activity_owner", "仅活动发布者可以执行此操作")
		}
		if !domain.CanCancel(activity.Status) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许取消")
		}
		activity.Status = domain.ActivityStatusCancelled
		activity.UpdatedBy = actorID
		activity.Version++
		return tx.Save(activity).Error
	})
}

// Finish marks a published activity finished.
func (s *Store) Finish(ctx context.Context, id, actorID, version uint64, now time.Time) (*domain.Activity, error) {
	return s.mutateActivityWithEvent(ctx, id, version, "activity.finished", func(tx *gorm.DB, activity *domain.Activity) error {
		if activity.CreatedBy != actorID {
			return apperror.New(http.StatusForbidden, "not_activity_owner", "仅活动发布者可以执行此操作")
		}
		if !domain.CanFinish(activity.Status) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许结束")
		}
		if now.Before(activity.EndAt) {
			return apperror.New(http.StatusConflict, "activity_not_ended", "活动结束时间前不能结束，请使用取消操作")
		}
		activity.Status = domain.ActivityStatusFinished
		activity.UpdatedBy = actorID
		activity.Version++
		return tx.Save(activity).Error
	})
}

// Register creates or reactivates a user registration and reserves capacity.
func (s *Store) Register(ctx context.Context, activityID, userID uint64, key string, now time.Time) (*domain.ActivityRegistration, *domain.Activity, error) {
	if key = strings.TrimSpace(key); key == "" || len(key) > 128 {
		return nil, nil, apperror.New(http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key 缺失或超过 128 字符")
	}
	var registration domain.ActivityRegistration
	var activity domain.Activity
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&activity, activityID).Error; err != nil {
			return activityNotFound(err)
		}
		// Replay-first dedupe: a row with the same (activity_id, user_id, key) is the
		// canonical idempotent representation; if found, return that row unchanged.
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("activity_id = ? AND user_id = ? AND idempotency_key = ?", activityID, userID, key).
			First(&registration).Error
		switch {
		case err == nil:
			// idempotent replay: no state change, return existing row
			return nil
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		}
		if err := domain.RegistrationAllowed(&activity, now); err != nil {
			return apperror.Wrap(http.StatusConflict, "activity_not_registrable", err.Error(), err)
		}
		// No prior registration with this key. Check for an *active* row that
		// belongs to a different key — that signals the caller reused a key
		// after cancellation, or is reusing the key for a fresh insert that
		// would create a parallel registration. Surface 409 with a stable code.
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("activity_id = ? AND user_id = ?", activityID, userID).
			First(&registration).Error
		switch {
		case err == nil:
			if registration.Status == domain.RegistrationStatusActive {
				return apperror.New(http.StatusConflict, "already_registered", "请勿重复报名")
			}
			// Previous row was cancelled; reuse it with the new idempotency key
			// so the unique (activity_id, user_id, key) constraint is satisfied
			// and audit trail survives.
			registration.Status = domain.RegistrationStatusActive
			registration.RegisteredAt = now
			registration.CancelledAt = nil
			registration.IdempotencyKey = key
			registration.Version++
			if err := tx.Save(&registration).Error; err != nil {
				return err
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			registration = domain.ActivityRegistration{
				ActivityId:     activityID,
				UserId:         userID,
				Status:         domain.RegistrationStatusActive,
				RegisteredAt:   now,
				IdempotencyKey: key,
				Version:        1,
			}
			if err := tx.Create(&registration).Error; err != nil {
				return err
			}
		default:
			return err
		}
		activity.RegisteredCount++
		if err := tx.Save(&activity).Error; err != nil {
			return err
		}
		return registrationEvent(tx, &activity, "activity.registration_created", &registration)
	})
	return &registration, &activity, err
}

// CancelRegistration cancels an active registration and releases capacity.
func (s *Store) CancelRegistration(ctx context.Context, activityID, userID, version uint64, now time.Time) (*domain.ActivityRegistration, *domain.Activity, error) {
	var registration domain.ActivityRegistration
	var activity domain.Activity
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&activity, activityID).Error; err != nil {
			return activityNotFound(err)
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("activity_id = ? AND user_id = ?", activityID, userID).First(&registration).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(http.StatusNotFound, "registration_not_found", "报名记录不存在")
			}
			return err
		}
		if registration.Version != version {
			return apperror.New(http.StatusConflict, "version_conflict", "报名记录已被其他请求更新")
		}
		if err := domain.CancellationAllowed(&registration, &activity, now); err != nil {
			return apperror.Wrap(http.StatusConflict, "registration_not_cancellable", err.Error(), err)
		}
		registration.Status = domain.RegistrationStatusCancelled
		registration.CancelledAt = &now
		registration.Version++
		if err := tx.Save(&registration).Error; err != nil {
			return err
		}
		if activity.RegisteredCount > 0 {
			activity.RegisteredCount--
		}
		if err := tx.Save(&activity).Error; err != nil {
			return err
		}
		if err := activityEvent(tx, &activity, "activity.registration_cancelled"); err != nil {
			return err
		}
		return registrationEvent(tx, &activity, "activity.registration_cancelled", &registration)
	})
	return &registration, &activity, err
}

// ListMyRegistrations returns registrations for the current user joined with activities.
func (s *Store) ListMyRegistrations(ctx context.Context, userID uint64, page, pageSize int) ([]domain.MyRegistration, int64, error) {
	query := platformquery.Use(idempotency.DB(ctx, s.db))
	regQuery := query.ActivityRegistration
	rows, total, err := regQuery.WithContext(ctx).
		Where(regQuery.UserId.Eq(userID)).
		Order(regQuery.RegisteredAt.Desc(), regQuery.ID.Desc()).
		FindByPage(offset(page, pageSize), limit(pageSize))
	if err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		return nil, total, nil
	}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ActivityId)
	}
	actQuery := query.Activity
	activities, err := actQuery.WithContext(ctx).Where(actQuery.ID.In(ids...)).Find()
	if err != nil {
		return nil, 0, err
	}
	activityByID := make(map[uint64]domain.Activity, len(activities))
	for _, activity := range activities {
		activityByID[activity.ID] = *activity
	}
	result := make([]domain.MyRegistration, 0, len(rows))
	for _, row := range rows {
		activity, ok := activityByID[row.ActivityId]
		if !ok {
			continue
		}
		result = append(result, domain.MyRegistration{Activity: activity, Registration: *row})
	}
	return result, total, nil
}

// Contact returns plaintext or masked contact details based on viewer access.
// Single-activity callers should use this directly; list callers should
// pre-batch via IsViewerRegisteredBatch and use ContactWithAccess per row to
// avoid issuing a SELECT COUNT(*) per list entry.
func (s *Store) Contact(ctx context.Context, activity *domain.Activity, viewerID uint64) (domain.ContactDetails, error) {
	if activity == nil {
		return domain.ContactDetails{}, nil
	}
	// Owner and terminal-state viewers can be answered without a DB query;
	// only the active-non-owner branch needs registration status.
	if activity.Status == domain.ActivityStatusCancelled || activity.Status == domain.ActivityStatusFinished {
		return s.maskedContact(activity)
	}
	if viewerID != 0 && viewerID == activity.CreatedBy {
		value, err := s.decryptContact(activity.ContactCiphertext, activityContactAAD(activity.ID))
		if err != nil {
			return domain.ContactDetails{}, err
		}
		return domain.ContactDetails{Type: activity.ContactType, Value: value}, nil
	}
	registered, err := s.IsViewerRegistered(ctx, viewerID, activity.ID)
	if err != nil {
		return domain.ContactDetails{}, err
	}
	return s.ContactWithAccess(ctx, activity, viewerID, registered)
}

// ContactWithAccess is the list-path variant that reuses a precomputed
// `hasActiveRegistration` flag in lieu of re-querying. When the masked route
// is taken, the ciphertext is *not* decrypted, eliminating one AES-GCM call
// per row in the common case where the viewer is neither owner nor registered.
func (s *Store) ContactWithAccess(_ context.Context, activity *domain.Activity, viewerID uint64, hasActiveRegistration bool) (domain.ContactDetails, error) {
	if activity == nil {
		return domain.ContactDetails{}, nil
	}
	if activity.Status == domain.ActivityStatusCancelled || activity.Status == domain.ActivityStatusFinished {
		// Terminal state: short-circuit to MaskContact on the ciphertext
		// directly so we don't pay decrypt→mask for non-authorised viewers.
		return s.maskedContact(activity)
	}
	if viewerID != 0 && viewerID == activity.CreatedBy {
		value, err := s.decryptContact(activity.ContactCiphertext, activityContactAAD(activity.ID))
		if err != nil {
			return domain.ContactDetails{}, err
		}
		return domain.ContactDetails{Type: activity.ContactType, Value: value}, nil
	}
	if hasActiveRegistration {
		value, err := s.decryptContact(activity.ContactCiphertext, activityContactAAD(activity.ID))
		if err != nil {
			return domain.ContactDetails{}, err
		}
		return domain.ContactDetails{Type: activity.ContactType, Value: value}, nil
	}
	return s.maskedContact(activity)
}

func (s *Store) maskedContact(activity *domain.Activity) (domain.ContactDetails, error) {
	if activity.ContactCiphertext == "" {
		return domain.ContactDetails{Type: activity.ContactType}, nil
	}
	value, err := s.decryptContact(activity.ContactCiphertext, activityContactAAD(activity.ID))
	if err != nil {
		return domain.ContactDetails{}, err
	}
	if value == "" {
		return domain.ContactDetails{Type: activity.ContactType}, nil
	}
	return domain.ContactDetails{Type: activity.ContactType, Value: privacy.MaskContact(value)}, nil
}

// mutateActivityWithEvent runs an optimistic-lock transaction and, on commit,
// writes a domain_event row whose event_kind is the kind supplied. Pass an
// empty kind to skip event emission (used by tests / paths that intentionally
// avoid event writes).
func (s *Store) mutateActivityWithEvent(ctx context.Context, id, version uint64, eventKind string, fn func(*gorm.DB, *domain.Activity) error) (*domain.Activity, error) {
	var activity domain.Activity
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&activity, id).Error; err != nil {
			return activityNotFound(err)
		}
		if activity.Version != version {
			return apperror.New(http.StatusConflict, "version_conflict", "活动已被其他请求更新")
		}
		if err := fn(tx, &activity); err != nil {
			return err
		}
		if eventKind == "" {
			return nil
		}
		return activityEvent(tx, &activity, eventKind)
	})
	return &activity, err
}

func (s *Store) encryptContact(value, aad string) (string, error) {
	if s.cipher == nil {
		return "", fmt.Errorf("contact cipher is not configured")
	}
	return s.cipher.Encrypt(strings.TrimSpace(value), aad)
}

func (s *Store) decryptContact(ciphertext, aad string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	if s.cipher == nil {
		return "", fmt.Errorf("contact cipher is not configured")
	}
	return s.cipher.Decrypt(ciphertext, aad)
}

func activityContactAAD(id uint64) string { return fmt.Sprintf("activity:%d", id) }

func activityNotFound(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(http.StatusNotFound, "activity_not_found", "活动不存在")
	}
	return err
}

func offset(page, pageSize int) int {
	if page < 1 {
		page = 1
	}
	return (page - 1) * limit(pageSize)
}

func limit(pageSize int) int {
	if pageSize < 1 {
		return 20
	}
	if pageSize > 100 {
		return 100
	}
	return pageSize
}

// FulltextMinLength is the smallest keyword length considered "long enough"
// to benefit from a FULLTEXT scan. Shorter keywords fall through to the
// single-column prefix LIKE so we never issue a MATCH AGAINST for a token
// shorter than the ngram parser's lower bound.
const FulltextMinLength = 3

func applyKeywordFilter(dao *gorm.DB, keyword string) *gorm.DB {
	if keyword == "" {
		return dao
	}
	trimmed := strings.TrimSpace(keyword)
	if len([]rune(trimmed)) < FulltextMinLength {
		// Prefix LIKE keeps the original indexes in play for short keywords.
		pattern := trimmed + "%"
		return dao.Where("(title LIKE ? OR summary LIKE ? OR location LIKE ?)", pattern, pattern, pattern)
	}
	// MySQL FULLTEXT (added by migration 000010). On dialect=sqlite (tests)
	// MATCH AGAINST is not supported, so fall back to the prefix LIKE pattern
	// instead of returning 500. Production runs against MySQL only.
	if isMySQL(dao) {
		return dao.Where("MATCH(title, summary, location) AGAINST (? IN BOOLEAN MODE)", trimmed)
	}
	pattern := "%" + trimmed + "%"
	return dao.Where("(title LIKE ? OR summary LIKE ? OR location LIKE ?)", pattern, pattern, pattern)
}

func isMySQL(dao *gorm.DB) bool {
	if dao == nil || dao.Dialector == nil {
		return false
	}
	return dao.Name() == "mysql"
}

func applyStartDateFilter(dao *gorm.DB, startDate *time.Time) *gorm.DB {
	if startDate == nil {
		return dao
	}
	start := startDate.UTC()
	end := start.Add(24 * time.Hour)
	return dao.Where("start_at >= ? AND start_at < ?", start, end)
}

var _ interface {
	Create(context.Context, uint64, domain.ActivityInput) (*domain.Activity, error)
} = (*Store)(nil)

// activityEvent writes a domain event for an aggregate mutation; the
// idempotency key encodes the activity version so re-emits collapse on the
// unique index in domain_events.
func activityEvent(tx *gorm.DB, activity *domain.Activity, kind string) error {
	if tx == nil || activity == nil {
		return nil
	}
	payload := map[string]any{
		"activity_id": activity.ID,
		"status":      activity.Status,
		"version":     activity.Version,
	}
	return domainevent.Write(tx, "activity", activity.ID, kind, payload)
}

// registrationEvent records a registration lifecycle mutation alongside the
// owning activity's domain event so subscribers see both the registration and
// the activity snapshot in a single transaction.
func registrationEvent(tx *gorm.DB, activity *domain.Activity, kind string, registration *domain.ActivityRegistration) error {
	if tx == nil || activity == nil || registration == nil {
		return nil
	}
	payload := map[string]any{
		"activity_id":     activity.ID,
		"registration_id": registration.ID,
		"user_id":         registration.UserId,
		"status":          registration.Status,
		"version":         registration.Version,
	}
	return domainevent.Write(tx, "activity_registration", registration.ID, kind, payload)
}
