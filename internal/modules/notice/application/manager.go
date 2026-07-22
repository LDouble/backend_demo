// Package application coordinates notice-center use cases.
package application

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/domain"
	"gorm.io/gorm"
)

// Store is the handwritten persistence contract for cross-entity transactions.
type Store interface {
	ValidateAudience(context.Context, domain.Audience) error
	Create(context.Context, *domain.Notice, []domain.NoticeAudience) error
	Get(context.Context, uint64) (*domain.Notice, []domain.NoticeAudience, error)
	ListAdmin(context.Context, int, int) ([]domain.Notice, int64, error)
	UpdateDraft(context.Context, *domain.Notice, uint64, []domain.NoticeAudience) (bool, error)
	DeleteDraft(context.Context, uint64, uint64) (bool, error)
	QueuePublish(context.Context, uint64, uint64, uint64, time.Time) (bool, error)
	Revoke(context.Context, uint64, uint64, uint64, time.Time) (bool, error)
	ListInbox(context.Context, uint64, InboxFilter) ([]domain.Notice, int64, error)
	GetInbox(context.Context, uint64, uint64) (*domain.Notice, error)
	UnreadCount(context.Context, uint64) (int64, error)
	MarkRead(context.Context, uint64, uint64, time.Time) error
	MarkAllRead(context.Context, uint64, time.Time) (int64, error)
	ListDeliveries(context.Context, uint64, int, int) ([]domain.NoticeDelivery, int64, error)
	RetryDeliveries(context.Context, uint64, time.Time) (int64, error)
}

// AdminFilter narrows the administrator notice list.
type AdminFilter struct{ Keyword, Status, Category string }
type filteredStore interface {
	ListAdminFiltered(context.Context, int, int, AdminFilter) ([]domain.Notice, int64, error)
	ListDeliveriesFiltered(context.Context, uint64, int, int, string) ([]domain.NoticeDelivery, int64, error)
}

// InboxFilter controls a recipient's inbox page.
type InboxFilter struct {
	Unread   bool
	Category string
	Page     int
	PageSize int
}

// Manager enforces notice state and validation rules.
type Manager struct {
	store Store
	now   func() time.Time
}

// NewManager creates the notice application service.
func NewManager(store Store) *Manager { return &Manager{store: store, now: time.Now} }

// Create creates a draft and retains the original audience declaration.
func (m *Manager) Create(ctx context.Context, actor uint64, input domain.DraftInput) (*domain.Notice, error) {
	if err := domain.ValidateDraft(input); err != nil {
		return nil, apperror.Wrap(http.StatusBadRequest, "invalid_notice", err.Error(), err)
	}
	if err := m.store.ValidateAudience(ctx, input.Audience); err != nil {
		return nil, err
	}
	notice := fromInput(input, actor)
	if err := m.store.Create(ctx, notice, audienceRows(input.Audience)); err != nil {
		return nil, fmt.Errorf("create notice: %w", err)
	}
	return notice, nil
}

// Update updates draft content using optimistic locking.
func (m *Manager) Update(ctx context.Context, id, actor, expected uint64, input domain.DraftInput) (*domain.Notice, error) {
	if expected == 0 {
		return nil, apperror.New(400, "invalid_version", "expected_version 必须大于 0")
	}
	if err := domain.ValidateDraft(input); err != nil {
		return nil, apperror.Wrap(400, "invalid_notice", err.Error(), err)
	}
	if err := m.store.ValidateAudience(ctx, input.Audience); err != nil {
		return nil, err
	}
	notice := fromInput(input, actor)
	notice.ID = id
	ok, err := m.store.UpdateDraft(ctx, notice, expected, audienceRows(input.Audience))
	if err != nil {
		return nil, fmt.Errorf("update notice: %w", err)
	}
	if !ok {
		return nil, m.versionOrStateError(ctx, id, expected)
	}
	return m.getNotice(ctx, id)
}

// GetAdmin gets a notice including its original audience.
func (m *Manager) GetAdmin(ctx context.Context, id uint64) (*domain.Notice, []domain.NoticeAudience, error) {
	notice, audience, err := m.store.Get(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, apperror.New(404, "notice_not_found", "通知不存在")
	}
	return notice, audience, err
}

// ListAdmin returns all states to authorized administrators.
func (m *Manager) ListAdmin(ctx context.Context, page, size int, filters ...AdminFilter) ([]domain.Notice, int64, error) {
	page, size = normalizePage(page), normalizeSize(size)
	filter := AdminFilter{}
	if len(filters) > 0 {
		filter = filters[0]
	}
	if filtered, ok := m.store.(filteredStore); ok {
		return filtered.ListAdminFiltered(ctx, page, size, filter)
	}
	return m.store.ListAdmin(ctx, page, size)
}

// Delete deletes drafts only.
func (m *Manager) Delete(ctx context.Context, id, expected uint64) error {
	ok, err := m.store.DeleteDraft(ctx, id, expected)
	if err != nil {
		return err
	}
	if !ok {
		return m.versionOrStateError(ctx, id, expected)
	}
	return nil
}

// Publish schedules or immediately queues a publication.
func (m *Manager) Publish(ctx context.Context, id, actor, expected uint64, at *time.Time) (*domain.Notice, error) {
	when := m.now().UTC()
	if at != nil && at.After(when) {
		when = at.UTC()
	}
	ok, err := m.store.QueuePublish(ctx, id, actor, expected, when)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, m.versionOrStateError(ctx, id, expected)
	}
	return m.getNotice(ctx, id)
}

// Revoke hides a notice and cancels unsent external deliveries.
func (m *Manager) Revoke(ctx context.Context, id, actor, expected uint64) (*domain.Notice, error) {
	ok, err := m.store.Revoke(ctx, id, actor, expected, m.now().UTC())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, m.versionOrStateError(ctx, id, expected)
	}
	return m.getNotice(ctx, id)
}

// ListInbox returns one recipient's published notices.
func (m *Manager) ListInbox(ctx context.Context, userID uint64, filter InboxFilter) ([]domain.Notice, int64, error) {
	filter.Page, filter.PageSize = normalizePage(filter.Page), normalizeSize(filter.PageSize)
	filter.Category = strings.TrimSpace(filter.Category)
	return m.store.ListInbox(ctx, userID, filter)
}

// GetInbox returns a recipient-scoped published notice.
func (m *Manager) GetInbox(ctx context.Context, userID, noticeID uint64) (*domain.Notice, error) {
	notice, err := m.store.GetInbox(ctx, userID, noticeID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.New(404, "notice_not_found", "通知不存在或不属于当前用户")
	}
	return notice, err
}

// UnreadCount returns the current unread count.
func (m *Manager) UnreadCount(ctx context.Context, userID uint64) (int64, error) {
	return m.store.UnreadCount(ctx, userID)
}

// MarkRead idempotently marks one notice read.
func (m *Manager) MarkRead(ctx context.Context, userID, noticeID uint64) error {
	if _, err := m.GetInbox(ctx, userID, noticeID); err != nil {
		return err
	}
	return m.store.MarkRead(ctx, userID, noticeID, m.now().UTC())
}

// MarkAllRead marks every currently published notice read.
func (m *Manager) MarkAllRead(ctx context.Context, userID uint64) (int64, error) {
	return m.store.MarkAllRead(ctx, userID, m.now().UTC())
}

// ListDeliveries returns external delivery attempts for a notice.
func (m *Manager) ListDeliveries(ctx context.Context, noticeID uint64, page, size int, status ...string) ([]domain.NoticeDelivery, int64, error) {
	if _, _, err := m.GetAdmin(ctx, noticeID); err != nil {
		return nil, 0, err
	}
	filter := ""
	if len(status) > 0 {
		filter = status[0]
	}
	if filtered, ok := m.store.(filteredStore); ok {
		return filtered.ListDeliveriesFiltered(ctx, noticeID, normalizePage(page), normalizeSize(size), filter)
	}
	return m.store.ListDeliveries(ctx, noticeID, normalizePage(page), normalizeSize(size))
}

// RetryDeliveries requeues failed external deliveries.
func (m *Manager) RetryDeliveries(ctx context.Context, noticeID uint64) (int64, error) {
	if _, _, err := m.GetAdmin(ctx, noticeID); err != nil {
		return 0, err
	}
	return m.store.RetryDeliveries(ctx, noticeID, m.now().UTC())
}

func fromInput(input domain.DraftInput, actor uint64) *domain.Notice {
	return &domain.Notice{Title: strings.TrimSpace(input.Title), Summary: input.Summary, Body: input.Body, Category: input.Category, Priority: input.Priority, ActionPath: optionalString(input.ActionPath), PushEnabled: contains(input.Channels, domain.ChannelPush), Status: domain.StatusDraft, Version: 1, CreatedBy: actor, UpdatedBy: actor}
}

func audienceRows(audience domain.Audience) []domain.NoticeAudience {
	rows := make([]domain.NoticeAudience, 0, 1+len(audience.Roles)+len(audience.UserIDs))
	if audience.All {
		rows = append(rows, domain.NoticeAudience{AudienceType: domain.AudienceAll, AudienceValue: "*"})
	}
	roles := append([]string(nil), audience.Roles...)
	sort.Strings(roles)
	for _, role := range roles {
		rows = append(rows, domain.NoticeAudience{AudienceType: domain.AudienceRole, AudienceValue: role})
	}
	ids := append([]uint64(nil), audience.UserIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		rows = append(rows, domain.NoticeAudience{AudienceType: domain.AudienceUser, AudienceValue: strconv.FormatUint(id, 10)})
	}
	return rows
}

func (m *Manager) versionOrStateError(ctx context.Context, id, expected uint64) error {
	notice, _, err := m.store.Get(ctx, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(404, "notice_not_found", "通知不存在")
	}
	if err != nil {
		return err
	}
	if notice.Version != expected {
		return apperror.New(409, "version_conflict", "通知已被其他请求更新")
	}
	return apperror.New(409, "invalid_notice_state", "当前通知状态不允许此操作")
}

func (m *Manager) getNotice(ctx context.Context, id uint64) (*domain.Notice, error) {
	notice, _, err := m.GetAdmin(ctx, id)
	return notice, err
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func normalizePage(page int) int {
	if page < 1 {
		return 1
	}
	return page
}
func normalizeSize(size int) int {
	if size < 1 {
		return 20
	}
	if size > 100 {
		return 100
	}
	return size
}
