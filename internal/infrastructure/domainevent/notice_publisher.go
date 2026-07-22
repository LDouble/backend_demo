package domainevent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	core "github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/domain"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// NoticePublisher converts marketplace lifecycle events into in-app notices.
type NoticePublisher struct {
	db  *gorm.DB
	now func() time.Time
}

// NewNoticePublisher creates the in-app domain-event subscriber.
func NewNoticePublisher(db *gorm.DB) *NoticePublisher {
	return &NoticePublisher{db: db, now: time.Now}
}

type noticeEventPayload struct {
	ListingID uint64 `json:"listing_id"`
	OwnerID   uint64 `json:"owner_id"`
	BuyerID   uint64 `json:"buyer_id"`
	SellerID  uint64 `json:"seller_id"`
	OrderType string `json:"order_type"`
}

// Publish idempotently persists a published in-app notice for supported events.
func (p *NoticePublisher) Publish(ctx context.Context, event core.Event) error {
	spec, ok, err := noticeSpecFor(event)
	if err != nil || !ok {
		return err
	}
	now := p.now().UTC()
	return p.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		notice := domain.Notice{
			Title: spec.title, Summary: spec.summary, Body: spec.summary,
			Category: "marketplace", Priority: domain.PriorityNormal,
			Status: domain.StatusPublished, ActionPath: &spec.actionPath,
			PushEnabled: false, PublishAt: &now, PublishedAt: &now,
			Version: 1, CreatedBy: 0, UpdatedBy: 0, SourceEventId: &event.ID,
		}
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_event_id"}}, DoNothing: true}).Create(&notice)
		if created.Error != nil {
			return fmt.Errorf("create event notice: %w", created.Error)
		}
		if created.RowsAffected == 0 {
			return nil
		}
		recipients := make([]domain.NoticeRecipient, 0, len(spec.recipients))
		for _, userID := range spec.recipients {
			recipients = append(recipients, domain.NoticeRecipient{NoticeId: notice.ID, UserId: userID})
		}
		if err := tx.Create(&recipients).Error; err != nil {
			return fmt.Errorf("create event notice recipients: %w", err)
		}
		return nil
	})
}

type eventNoticeSpec struct {
	title      string
	summary    string
	actionPath string
	recipients []uint64
}

func noticeSpecFor(event core.Event) (eventNoticeSpec, bool, error) {
	supported := map[string]struct{}{
		"listing.reviewed": {}, "listing.removed": {}, "order.created": {},
		"order.cancelled": {}, "order.completed": {}, "order.expired": {},
	}
	if _, ok := supported[event.EventType]; !ok {
		return eventNoticeSpec{}, false, nil
	}
	var payload noticeEventPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return eventNoticeSpec{}, false, fmt.Errorf("decode %s payload: %w", event.EventType, err)
	}
	if strings.HasPrefix(event.EventType, "order.") && payload.OrderType != tradedomain.OrderTypeMarketplace {
		return eventNoticeSpec{}, false, nil
	}
	spec := eventNoticeSpec{}
	switch event.EventType {
	case "listing.reviewed":
		spec.title, spec.summary = "商品审核结果", "你的二手商品审核状态已更新"
		spec.actionPath = fmt.Sprintf("/api/v1/marketplace/listings/%d", payload.ListingID)
		spec.recipients = []uint64{payload.OwnerID}
	case "listing.removed":
		spec.title, spec.summary = "商品已下架", "你的二手商品已被管理员下架"
		spec.actionPath = fmt.Sprintf("/api/v1/marketplace/listings/%d", payload.ListingID)
		spec.recipients = []uint64{payload.OwnerID}
	default:
		spec.title = orderEventTitle(event.EventType)
		spec.summary = "二手交易订单状态已更新"
		spec.actionPath = fmt.Sprintf("/api/v1/orders/%d", event.AggregateID)
		spec.recipients = []uint64{payload.BuyerID, payload.SellerID}
	}
	spec.recipients = uniqueRecipients(spec.recipients)
	if len(spec.recipients) == 0 {
		return eventNoticeSpec{}, false, fmt.Errorf("%s payload has no recipients", event.EventType)
	}
	return spec, true, nil
}

func orderEventTitle(eventType string) string {
	switch eventType {
	case "order.created":
		return "二手交易订单已创建"
	case "order.cancelled":
		return "二手交易订单已取消"
	case "order.completed":
		return "二手交易已完成"
	default:
		return "二手交易订单已过期"
	}
}

func uniqueRecipients(values []uint64) []uint64 {
	seen := map[uint64]struct{}{}
	result := make([]uint64, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
