// Package infrastructure persists notice aggregates and transactional outbox events.
package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/application"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrLeaseLost indicates that another worker or a state transition owns the row.
var ErrLeaseLost = errors.New("notice work lease lost")

// ErrDeliveryLeaseHeld keeps an overlapping task retryable until the owner
// completes the delivery or releases its lease.
var ErrDeliveryLeaseHeld = errors.New("notice delivery lease is held")

// Outbox states and event names shared with the relay.
const (
	OutboxPending    = "pending"
	OutboxLeased     = "leased"
	OutboxDispatched = "dispatched"
	EventPublish     = "notice.publish"
	EventDelivery    = "notice.delivery"
)

// NoticeStore implements transactional operations which cannot be expressed by
// the generated single-entity repository without breaking aggregate atomicity.
type NoticeStore struct{ db *gorm.DB }

// NewNoticeStore creates the cross-entity notice persistence adapter.
func NewNoticeStore(db *gorm.DB) *NoticeStore { return &NoticeStore{db: db} }

// ValidateAudience verifies declared roles and explicit active users.
func (s *NoticeStore) ValidateAudience(ctx context.Context, audience domain.Audience) error {
	for _, role := range uniqueStrings(audience.Roles) {
		var count int64
		if err := idempotency.DB(ctx, s.db).Model(&model.Role{}).Where("name = ?", role).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return apperror.New(http.StatusBadRequest, "invalid_audience", "受众角色不存在: "+role)
		}
	}
	ids := uniqueIDs(audience.UserIDs)
	if len(ids) > 0 {
		var count int64
		if err := idempotency.DB(ctx, s.db).Model(&model.User{}).Where("id IN ? AND status = ?", ids, model.UserActive).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(ids)) {
			return apperror.New(http.StatusBadRequest, "invalid_audience", "指定用户不存在或已禁用")
		}
	}
	return nil
}

// Create atomically inserts a draft and its audience declaration.
func (s *NoticeStore) Create(ctx context.Context, notice *domain.Notice, audience []domain.NoticeAudience) error {
	return idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(notice).Error; err != nil {
			return err
		}
		for i := range audience {
			audience[i].NoticeId = notice.ID
		}
		if len(audience) > 0 {
			return tx.Create(&audience).Error
		}
		return nil
	})
}

// Get returns a notice and its original audience declaration.
func (s *NoticeStore) Get(ctx context.Context, id uint64) (*domain.Notice, []domain.NoticeAudience, error) {
	var notice domain.Notice
	if err := idempotency.DB(ctx, s.db).First(&notice, id).Error; err != nil {
		return nil, nil, err
	}
	var audience []domain.NoticeAudience
	if err := idempotency.DB(ctx, s.db).Where("notice_id = ?", id).Order("audience_type, audience_value").Find(&audience).Error; err != nil {
		return nil, nil, err
	}
	return &notice, audience, nil
}

// ListAdmin returns all notice states for administrators.
func (s *NoticeStore) ListAdmin(ctx context.Context, page, size int) ([]domain.Notice, int64, error) {
	var total int64
	base := idempotency.DB(ctx, s.db).Model(&domain.Notice{})
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.Notice
	err := base.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error
	return rows, total, err
}

// UpdateDraft applies an optimistic draft-only update.
func (s *NoticeStore) UpdateDraft(ctx context.Context, notice *domain.Notice, expected uint64, audience []domain.NoticeAudience) (bool, error) {
	updated := false
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		values := map[string]any{"title": notice.Title, "summary": notice.Summary, "body": notice.Body, "category": notice.Category, "priority": notice.Priority, "action_path": notice.ActionPath, "push_enabled": notice.PushEnabled, "updated_by": notice.UpdatedBy, "version": gorm.Expr("version + 1")}
		result := tx.Model(&domain.Notice{}).Where("id = ? AND status = ? AND version = ?", notice.ID, domain.StatusDraft, expected).Updates(values)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		updated = true
		if err := tx.Where("notice_id = ?", notice.ID).Delete(&domain.NoticeAudience{}).Error; err != nil {
			return err
		}
		for i := range audience {
			audience[i].NoticeId = notice.ID
		}
		if len(audience) > 0 {
			return tx.Create(&audience).Error
		}
		return nil
	})
	return updated, err
}

// DeleteDraft deletes only a matching draft version.
func (s *NoticeStore) DeleteDraft(ctx context.Context, id, expected uint64) (bool, error) {
	result := idempotency.DB(ctx, s.db).Where("id = ? AND status = ? AND version = ?", id, domain.StatusDraft, expected).Delete(&domain.Notice{})
	return result.RowsAffected == 1, result.Error
}

// QueuePublish changes state and writes a publication outbox event atomically.
func (s *NoticeStore) QueuePublish(ctx context.Context, id, actor, expected uint64, when time.Time) (bool, error) {
	queued := false
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		status := domain.StatusPublishing
		if when.After(time.Now().UTC()) {
			status = domain.StatusScheduled
		}
		result := tx.Model(&domain.Notice{}).Where("id = ? AND status = ? AND version = ?", id, domain.StatusDraft, expected).Updates(map[string]any{"status": status, "publish_at": when, "updated_by": actor, "version": gorm.Expr("version + 1")})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		queued = true
		return createOutbox(tx, id, EventPublish, map[string]uint64{"notice_id": id}, when)
	})
	return queued, err
}

// Revoke hides a notice and cancels pending work atomically.
func (s *NoticeStore) Revoke(ctx context.Context, id, actor, expected uint64, now time.Time) (bool, error) {
	updated := false
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&domain.Notice{}).Where("id = ? AND version = ? AND status IN ?", id, expected, []string{domain.StatusScheduled, domain.StatusPublishing, domain.StatusPublished}).Updates(map[string]any{"status": domain.StatusRevoked, "revoked_at": now, "updated_by": actor, "version": gorm.Expr("version + 1")})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		updated = true
		if err := tx.Model(&domain.NoticeDelivery{}).Where("notice_id = ? AND status IN ?", id, []string{"pending", "failed"}).Updates(map[string]any{"status": "canceled", "last_error": "notice revoked"}).Error; err != nil {
			return err
		}
		return tx.Model(&domain.OutboxEvent{}).Where("aggregate_type = ? AND aggregate_id = ? AND status IN ?", "notice", id, []string{OutboxPending, OutboxLeased}).Update("status", "canceled").Error
	})
	return updated, err
}

// ListInbox returns published notices scoped to one recipient.
func (s *NoticeStore) ListInbox(ctx context.Context, userID uint64, filter application.InboxFilter) ([]domain.Notice, int64, error) {
	base := idempotency.DB(ctx, s.db).Model(&domain.Notice{}).Joins("JOIN notice_recipients nr ON nr.notice_id = notices.id AND nr.user_id = ?", userID).Where("notices.status = ?", domain.StatusPublished)
	if filter.Unread {
		base = base.Where("nr.read_at IS NULL")
	}
	if filter.Category != "" {
		base = base.Where("notices.category = ?", filter.Category)
	}
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.Notice
	err := base.Select("notices.*").Order("notices.published_at DESC, notices.id DESC").Offset((filter.Page - 1) * filter.PageSize).Limit(filter.PageSize).Find(&rows).Error
	return rows, total, err
}

// GetInbox gets a published notice scoped to one recipient.
func (s *NoticeStore) GetInbox(ctx context.Context, userID, noticeID uint64) (*domain.Notice, error) {
	var notice domain.Notice
	err := idempotency.DB(ctx, s.db).Model(&domain.Notice{}).Joins("JOIN notice_recipients nr ON nr.notice_id = notices.id AND nr.user_id = ?", userID).Where("notices.id = ? AND notices.status = ?", noticeID, domain.StatusPublished).Select("notices.*").First(&notice).Error
	return &notice, err
}

// UnreadCount counts unread published recipient rows.
func (s *NoticeStore) UnreadCount(ctx context.Context, userID uint64) (int64, error) {
	var count int64
	err := idempotency.DB(ctx, s.db).Table("notice_recipients nr").Joins("JOIN notices n ON n.id = nr.notice_id").Where("nr.user_id = ? AND nr.read_at IS NULL AND n.status = ?", userID, domain.StatusPublished).Count(&count).Error
	return count, err
}

// MarkRead idempotently updates one unread recipient row.
func (s *NoticeStore) MarkRead(ctx context.Context, userID, noticeID uint64, now time.Time) error {
	return idempotency.DB(ctx, s.db).Model(&domain.NoticeRecipient{}).Where("user_id = ? AND notice_id = ? AND read_at IS NULL", userID, noticeID).Update("read_at", now).Error
}

// MarkAllRead marks all currently published recipient rows read.
func (s *NoticeStore) MarkAllRead(ctx context.Context, userID uint64, now time.Time) (int64, error) {
	db := idempotency.DB(ctx, s.db)
	result := db.Model(&domain.NoticeRecipient{}).Where("user_id = ? AND read_at IS NULL AND notice_id IN (?)", userID, db.Model(&domain.Notice{}).Select("id").Where("status = ?", domain.StatusPublished)).Update("read_at", now)
	return result.RowsAffected, result.Error
}

// ListDeliveries returns external deliveries for a notice.
func (s *NoticeStore) ListDeliveries(ctx context.Context, noticeID uint64, page, size int) ([]domain.NoticeDelivery, int64, error) {
	base := idempotency.DB(ctx, s.db).Model(&domain.NoticeDelivery{}).Where("notice_id = ?", noticeID)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []domain.NoticeDelivery
	err := base.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error
	return rows, total, err
}

// RetryDeliveries requeues failed deliveries with new outbox events.
func (s *NoticeStore) RetryDeliveries(ctx context.Context, noticeID uint64, now time.Time) (int64, error) {
	var count int64
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		var deliveries []domain.NoticeDelivery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("notice_id = ? AND status = ?", noticeID, "failed").Find(&deliveries).Error; err != nil {
			return err
		}
		for i := range deliveries {
			result := tx.Model(&deliveries[i]).Where("status = ?", "failed").Updates(map[string]any{"status": "pending", "locked_at": nil, "last_error": ""})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				continue
			}
			if err := createOutbox(tx, noticeID, EventDelivery, map[string]uint64{"delivery_id": deliveries[i].ID}, now); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	return count, err
}

// ClaimDelivery obtains a conditional provider lease for one delivery.
func (s *NoticeStore) ClaimDelivery(ctx context.Context, id uint64, now time.Time, lease time.Duration) (time.Time, bool, error) {
	lockedAt := now.UTC().Truncate(time.Millisecond)
	result := idempotency.DB(ctx, s.db).Model(&domain.NoticeDelivery{}).
		Where("id = ? AND (status = ? OR (status = ? AND locked_at < ?))", id, "pending", "delivering", lockedAt.Add(-lease)).
		Updates(map[string]any{"status": "delivering", "locked_at": lockedAt})
	if result.Error != nil || result.RowsAffected == 1 {
		return lockedAt, result.RowsAffected == 1, result.Error
	}
	var delivery domain.NoticeDelivery
	err := idempotency.DB(ctx, s.db).Select("status").First(&delivery, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return lockedAt, false, nil
	}
	if err != nil {
		return lockedAt, false, err
	}
	if delivery.Status == "delivering" {
		return lockedAt, false, ErrDeliveryLeaseHeld
	}
	return lockedAt, false, nil
}

// LeaseOutbox atomically claims due work using a timeout lease.
func (s *NoticeStore) LeaseOutbox(ctx context.Context, limit int, now time.Time, lease time.Duration) ([]domain.OutboxEvent, error) {
	var events []domain.OutboxEvent
	lockedAt := now.UTC().Truncate(time.Millisecond)
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("available_at <= ? AND (status = ? OR (status = ? AND locked_at < ?))", lockedAt, OutboxPending, OutboxLeased, lockedAt.Add(-lease)).Order("id").Limit(limit)
		if err := query.Find(&events).Error; err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}
		ids := make([]uint64, len(events))
		for i := range events {
			ids[i] = events[i].ID
			events[i].LockedAt = &lockedAt
		}
		return tx.Model(&domain.OutboxEvent{}).Where("id IN ?", ids).Updates(map[string]any{"status": OutboxLeased, "locked_at": lockedAt}).Error
	})
	return events, err
}

// MarkOutboxDispatched releases a successfully enqueued outbox row.
func (s *NoticeStore) MarkOutboxDispatched(ctx context.Context, id uint64, lockedAt time.Time) error {
	result := idempotency.DB(ctx, s.db).Model(&domain.OutboxEvent{}).
		Where("id = ? AND status = ? AND locked_at = ?", id, OutboxLeased, lockedAt).
		Updates(map[string]any{"status": OutboxDispatched, "locked_at": nil})
	return requireLease(result)
}

// ReleaseOutbox returns a failed relay row to the pending pool.
func (s *NoticeStore) ReleaseOutbox(ctx context.Context, id uint64, lockedAt time.Time, cause error, now time.Time) error {
	message := cause.Error()
	if len(message) > 1000 {
		message = message[:1000]
	}
	result := idempotency.DB(ctx, s.db).Model(&domain.OutboxEvent{}).
		Where("id = ? AND status = ? AND locked_at = ?", id, OutboxLeased, lockedAt).
		Updates(map[string]any{"status": OutboxPending, "locked_at": nil, "available_at": now.Add(time.Second), "attempts": gorm.Expr("attempts + 1"), "last_error": message})
	return requireLease(result)
}

// Publish snapshots recipients and creates external delivery events atomically.
func (s *NoticeStore) Publish(ctx context.Context, noticeID uint64, now time.Time) error {
	return idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		var notice domain.Notice
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&notice, noticeID).Error; err != nil {
			return err
		}
		if notice.Status == domain.StatusPublished || notice.Status == domain.StatusRevoked {
			return nil
		}
		if notice.Status != domain.StatusPublishing && notice.Status != domain.StatusScheduled {
			return fmt.Errorf("notice %d has state %s", noticeID, notice.Status)
		}
		var audience []domain.NoticeAudience
		if err := tx.Where("notice_id = ?", noticeID).Find(&audience).Error; err != nil {
			return err
		}
		ids, err := resolveRecipients(tx, audience)
		if err != nil {
			return err
		}
		for _, userID := range ids {
			recipient := domain.NoticeRecipient{NoticeId: noticeID, UserId: userID}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&recipient).Error; err != nil {
				return err
			}
			if notice.PushEnabled {
				key := fmt.Sprintf("notice:%d:push:user:%d", noticeID, userID)
				delivery := domain.NoticeDelivery{NoticeId: noticeID, UserId: userID, Channel: domain.ChannelPush, Status: "pending", IdempotencyKey: key}
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&delivery).Error; err != nil {
					return err
				}
				if delivery.ID != 0 {
					if err := createOutbox(tx, noticeID, EventDelivery, map[string]uint64{"delivery_id": delivery.ID}, now); err != nil {
						return err
					}
				}
			}
		}
		return tx.Model(&domain.Notice{}).Where("id = ?", noticeID).Updates(map[string]any{"status": domain.StatusPublished, "published_at": now, "version": gorm.Expr("version + 1")}).Error
	})
}

// LoadDelivery loads the provider input aggregate.
func (s *NoticeStore) LoadDelivery(ctx context.Context, id uint64) (*domain.NoticeDelivery, *domain.Notice, *model.User, error) {
	var delivery domain.NoticeDelivery
	if err := idempotency.DB(ctx, s.db).First(&delivery, id).Error; err != nil {
		return nil, nil, nil, err
	}
	var notice domain.Notice
	if err := idempotency.DB(ctx, s.db).First(&notice, delivery.NoticeId).Error; err != nil {
		return nil, nil, nil, err
	}
	var user model.User
	if err := idempotency.DB(ctx, s.db).First(&user, delivery.UserId).Error; err != nil {
		return nil, nil, nil, err
	}
	return &delivery, &notice, &user, nil
}

// CompleteDelivery records a provider success idempotently.
func (s *NoticeStore) CompleteDelivery(ctx context.Context, id uint64, lockedAt time.Time, providerID string) error {
	result := idempotency.DB(ctx, s.db).Model(&domain.NoticeDelivery{}).
		Where("id = ? AND status = ? AND locked_at = ?", id, "delivering", lockedAt).
		Updates(map[string]any{"status": "sent", "locked_at": nil, "provider_message_id": providerID, "attempts": gorm.Expr("attempts + 1"), "last_error": ""})
	return requireLease(result)
}

// FailDelivery records a retryable or final provider failure.
func (s *NoticeStore) FailDelivery(ctx context.Context, id uint64, lockedAt time.Time, cause error, final bool) error {
	status := "pending"
	if final {
		status = "failed"
	}
	message := cause.Error()
	if len(message) > 1000 {
		message = message[:1000]
	}
	result := idempotency.DB(ctx, s.db).Model(&domain.NoticeDelivery{}).
		Where("id = ? AND status = ? AND locked_at = ?", id, "delivering", lockedAt).
		Updates(map[string]any{"status": status, "locked_at": nil, "attempts": gorm.Expr("attempts + 1"), "last_error": message})
	return requireLease(result)
}

func requireLease(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLeaseLost
	}
	return nil
}

func createOutbox(tx *gorm.DB, aggregateID uint64, eventType string, payload any, at time.Time) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	event := domain.OutboxEvent{AggregateType: "notice", AggregateId: aggregateID, EventType: eventType, Payload: data, Status: OutboxPending, AvailableAt: at}
	return tx.Create(&event).Error
}

func resolveRecipients(tx *gorm.DB, audience []domain.NoticeAudience) ([]uint64, error) {
	ids := map[uint64]struct{}{}
	for _, row := range audience {
		switch row.AudienceType {
		case domain.AudienceAll:
			var users []model.User
			if err := tx.Where("status = ?", model.UserActive).Find(&users).Error; err != nil {
				return nil, err
			}
			for _, user := range users {
				ids[user.ID] = struct{}{}
			}
		case domain.AudienceRole:
			var subjects []string
			if err := tx.Table("casbin_rule").Where("ptype = 'g' AND v1 = ?", row.AudienceValue).Pluck("v0", &subjects).Error; err != nil {
				return nil, err
			}
			userIDs := make([]uint64, 0, len(subjects))
			for _, subject := range subjects {
				if strings.HasPrefix(subject, "user:") {
					if id, err := strconv.ParseUint(strings.TrimPrefix(subject, "user:"), 10, 64); err == nil {
						userIDs = append(userIDs, id)
					}
				}
			}
			var users []model.User
			if len(userIDs) > 0 {
				if err := tx.Where("id IN ? AND status = ?", userIDs, model.UserActive).Find(&users).Error; err != nil {
					return nil, err
				}
			}
			for _, user := range users {
				ids[user.ID] = struct{}{}
			}
		case domain.AudienceUser:
			id, err := strconv.ParseUint(row.AudienceValue, 10, 64)
			if err != nil {
				return nil, err
			}
			var user model.User
			if err := tx.Where("id = ? AND status = ?", id, model.UserActive).Take(&user).Error; err != nil {
				return nil, fmt.Errorf("explicit notice recipient %d is missing or disabled: %w", id, err)
			}
			ids[id] = struct{}{}
		}
	}
	result := make([]uint64, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	return result, nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := seen[value]; value != "" && !ok {
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}
func uniqueIDs(values []uint64) []uint64 {
	seen := map[uint64]struct{}{}
	out := make([]uint64, 0, len(values))
	for _, value := range values {
		if value > 0 {
			if _, ok := seen[value]; !ok {
				seen[value] = struct{}{}
				out = append(out, value)
			}
		}
	}
	return out
}

var _ application.Store = (*NoticeStore)(nil)
