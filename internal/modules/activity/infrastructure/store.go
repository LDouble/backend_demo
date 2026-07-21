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
	"github.com/weouc-plus/campus-platform/internal/core/privacy"
	platformquery "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store persists activity aggregates and registration workflows.
type Store struct {
	db     *gorm.DB
	query  *platformquery.Query
	cipher *configcenter.Cipher
}

// NewStore creates an activity persistence adapter.
func NewStore(db *gorm.DB, ciphers ...*configcenter.Cipher) *Store {
	store := &Store{db: db, query: platformquery.Use(db)}
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
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(activity).Error; err != nil {
			return err
		}
		ciphertext, err := s.encryptContact(input.Contact.Value, activityContactAAD(activity.ID))
		if err != nil {
			return err
		}
		activity.ContactCiphertext = ciphertext
		return tx.Save(activity).Error
	})
	return activity, err
}

// Update changes an editable activity draft.
func (s *Store) Update(ctx context.Context, id, actorID, version uint64, input domain.ActivityInput, _ time.Time) (*domain.Activity, error) {
	return s.mutateActivity(ctx, id, version, func(tx *gorm.DB, activity *domain.Activity) error {
		if activity.CreatedBy != actorID {
			return apperror.New(http.StatusForbidden, "not_activity_owner", "仅活动发布者可以执行此操作")
		}
		if !domain.CanEdit(activity.Status, activity.ReviewStatus) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许编辑")
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
	activity, err := s.query.Activity.WithContext(ctx).Where(s.query.Activity.ID.Eq(id)).First()
	return activity, activityNotFound(err)
}

// GetPublic returns one published and approved activity.
func (s *Store) GetPublic(ctx context.Context, id uint64) (*domain.Activity, error) {
	q := s.query.Activity
	activity, err := q.WithContext(ctx).Where(
		q.ID.Eq(id),
		q.Status.Eq(domain.ActivityStatusPublished),
		q.ReviewStatus.Eq(domain.ReviewStatusApproved),
	).First()
	return activity, activityNotFound(err)
}

// ListAdmin returns activities visible to administrators.
func (s *Store) ListAdmin(ctx context.Context, search domain.AdminSearch, page, pageSize int) ([]domain.Activity, int64, error) {
	dao := s.db.WithContext(ctx).Model(&domain.Activity{})
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
	dao := s.db.WithContext(ctx).Model(&domain.Activity{}).
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
	return s.mutateActivity(ctx, id, version, func(tx *gorm.DB, activity *domain.Activity) error {
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

// Approve marks a pending activity as approved.
func (s *Store) Approve(ctx context.Context, id, actorID, version uint64, comment string) (*domain.Activity, error) {
	return s.review(ctx, id, actorID, version, true, comment)
}

// Reject marks a pending activity as rejected.
func (s *Store) Reject(ctx context.Context, id, actorID, version uint64, comment string) (*domain.Activity, error) {
	return s.review(ctx, id, actorID, version, false, comment)
}

func (s *Store) review(ctx context.Context, id, actorID, version uint64, approved bool, comment string) (*domain.Activity, error) {
	return s.mutateActivity(ctx, id, version, func(tx *gorm.DB, activity *domain.Activity) error {
		if approved && !domain.CanApprove(activity.Status, activity.ReviewStatus) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许审核通过")
		}
		if !approved && !domain.CanReject(activity.Status, activity.ReviewStatus) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许驳回")
		}
		trimmed := strings.TrimSpace(comment)
		if approved {
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
	return s.mutateActivity(ctx, id, version, func(tx *gorm.DB, activity *domain.Activity) error {
		if activity.CreatedBy != actorID {
			return apperror.New(http.StatusForbidden, "not_activity_owner", "仅活动发布者可以执行此操作")
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
		return tx.Save(activity).Error
	})
}

// Cancel marks a published activity cancelled.
func (s *Store) Cancel(ctx context.Context, id, actorID, version uint64, _ time.Time) (*domain.Activity, error) {
	return s.mutateActivity(ctx, id, version, func(tx *gorm.DB, activity *domain.Activity) error {
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
func (s *Store) Finish(ctx context.Context, id, actorID, version uint64, _ time.Time) (*domain.Activity, error) {
	return s.mutateActivity(ctx, id, version, func(tx *gorm.DB, activity *domain.Activity) error {
		if activity.CreatedBy != actorID {
			return apperror.New(http.StatusForbidden, "not_activity_owner", "仅活动发布者可以执行此操作")
		}
		if !domain.CanFinish(activity.Status) {
			return apperror.New(http.StatusConflict, "invalid_activity_state", "当前活动状态不允许结束")
		}
		activity.Status = domain.ActivityStatusFinished
		activity.UpdatedBy = actorID
		activity.Version++
		return tx.Save(activity).Error
	})
}

// Register creates or reactivates a user registration and reserves capacity.
func (s *Store) Register(ctx context.Context, activityID, userID uint64, _ string, now time.Time) (*domain.ActivityRegistration, *domain.Activity, error) {
	var registration domain.ActivityRegistration
	var activity domain.Activity
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&activity, activityID).Error; err != nil {
			return activityNotFound(err)
		}
		if err := domain.RegistrationAllowed(&activity, now); err != nil {
			return apperror.Wrap(http.StatusConflict, "activity_not_registrable", err.Error(), err)
		}
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("activity_id = ? AND user_id = ?", activityID, userID).First(&registration).Error
		switch {
		case err == nil:
			if registration.Status == domain.RegistrationStatusActive {
				return apperror.New(http.StatusConflict, "already_registered", "请勿重复报名")
			}
			registration.Status = domain.RegistrationStatusActive
			registration.RegisteredAt = now
			registration.CancelledAt = nil
			registration.Version++
			if err := tx.Save(&registration).Error; err != nil {
				return err
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			registration = domain.ActivityRegistration{
				ActivityId:   activityID,
				UserId:       userID,
				Status:       domain.RegistrationStatusActive,
				RegisteredAt: now,
				Version:      1,
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
		return nil
	})
	return &registration, &activity, err
}

// CancelRegistration cancels an active registration and releases capacity.
func (s *Store) CancelRegistration(ctx context.Context, activityID, userID, version uint64, now time.Time) (*domain.ActivityRegistration, *domain.Activity, error) {
	var registration domain.ActivityRegistration
	var activity domain.Activity
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		return nil
	})
	return &registration, &activity, err
}

// ListMyRegistrations returns registrations for the current user joined with activities.
func (s *Store) ListMyRegistrations(ctx context.Context, userID uint64, page, pageSize int) ([]domain.MyRegistration, int64, error) {
	regQuery := s.query.ActivityRegistration
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
	actQuery := s.query.Activity
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
func (s *Store) Contact(ctx context.Context, activity *domain.Activity, viewerID uint64) (domain.ContactDetails, error) {
	value, err := s.decryptContact(activity.ContactCiphertext, activityContactAAD(activity.ID))
	if err != nil {
		return domain.ContactDetails{}, err
	}
	if value == "" {
		return domain.ContactDetails{Type: activity.ContactType}, nil
	}
	if activity.Status != domain.ActivityStatusCancelled && activity.Status != domain.ActivityStatusFinished {
		if viewerID == activity.CreatedBy {
			return domain.ContactDetails{Type: activity.ContactType, Value: value}, nil
		}
		regQuery := s.query.ActivityRegistration
		count, err := regQuery.WithContext(ctx).Where(
			regQuery.ActivityId.Eq(activity.ID),
			regQuery.UserId.Eq(viewerID),
			regQuery.Status.Eq(domain.RegistrationStatusActive),
		).Count()
		if err != nil {
			return domain.ContactDetails{}, err
		}
		if count > 0 {
			return domain.ContactDetails{Type: activity.ContactType, Value: value}, nil
		}
	}
	return domain.ContactDetails{Type: activity.ContactType, Value: privacy.MaskContact(value)}, nil
}

func (s *Store) mutateActivity(ctx context.Context, id, version uint64, fn func(*gorm.DB, *domain.Activity) error) (*domain.Activity, error) {
	var activity domain.Activity
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&activity, id).Error; err != nil {
			return activityNotFound(err)
		}
		if activity.Version != version {
			return apperror.New(http.StatusConflict, "version_conflict", "活动已被其他请求更新")
		}
		return fn(tx, &activity)
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

func applyKeywordFilter(dao *gorm.DB, keyword string) *gorm.DB {
	if keyword == "" {
		return dao
	}
	pattern := "%" + keyword + "%"
	return dao.Where("(title LIKE ? OR summary LIKE ? OR location LIKE ?)", pattern, pattern, pattern)
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
