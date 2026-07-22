package domainevent

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	core "github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/domain"
	"gorm.io/gorm"
)

func TestNoticePublisherCreatesIdempotentMarketplaceNotice(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&domain.Notice{}, &domain.NoticeRecipient{}); err != nil {
		t.Fatal(err)
	}
	publisher := NewNoticePublisher(db)
	publisher.now = func() time.Time { return time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC) }
	event := core.Event{
		ID: 42, AggregateType: "order", AggregateID: 9, EventType: "order.created",
		Payload: []byte(`{"order_type":"marketplace","buyer_id":12,"seller_id":8,"contact":"must-not-leak"}`),
	}
	for range 2 {
		if err = publisher.Publish(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	var notices []domain.Notice
	if err = db.Find(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].SourceEventId == nil || *notices[0].SourceEventId != event.ID {
		t.Fatalf("notices = %#v", notices)
	}
	if notices[0].PushEnabled || notices[0].Status != domain.StatusPublished || notices[0].Body == "must-not-leak" {
		t.Fatalf("unexpected notice = %#v", notices[0])
	}
	var recipients []domain.NoticeRecipient
	if err = db.Order("user_id").Find(&recipients).Error; err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 2 || recipients[0].UserId != 8 || recipients[1].UserId != 12 {
		t.Fatalf("recipients = %#v", recipients)
	}
}

func TestNoticeSpecRecipientsAndPaths(t *testing.T) {
	tests := []struct {
		name       string
		event      core.Event
		wantUsers  int
		wantAction string
	}{
		{name: "review", event: core.Event{EventType: "listing.reviewed", Payload: []byte(`{"listing_id":3,"owner_id":7}`)}, wantUsers: 1, wantAction: "/api/v1/marketplace/listings/3"},
		{name: "removed", event: core.Event{EventType: "listing.removed", Payload: []byte(`{"listing_id":4,"owner_id":7}`)}, wantUsers: 1, wantAction: "/api/v1/marketplace/listings/4"},
		{name: "completed", event: core.Event{AggregateID: 11, EventType: "order.completed", Payload: []byte(`{"order_type":"marketplace","buyer_id":7,"seller_id":8}`)}, wantUsers: 2, wantAction: "/api/v1/orders/11"},
		{name: "deduplicates party", event: core.Event{AggregateID: 12, EventType: "order.expired", Payload: []byte(`{"order_type":"marketplace","buyer_id":7,"seller_id":7}`)}, wantUsers: 1, wantAction: "/api/v1/orders/12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ok, err := noticeSpecFor(tt.event)
			if err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			if len(spec.recipients) != tt.wantUsers || spec.actionPath != tt.wantAction {
				t.Fatalf("spec = %#v", spec)
			}
		})
	}
}

func TestNoticeSpecIgnoresNonMarketplaceOrders(t *testing.T) {
	event := core.Event{
		AggregateID: 13,
		EventType:   "order.completed",
		Payload:     []byte(`{"order_type":"errand","buyer_id":7,"seller_id":8}`),
	}
	if spec, ok, err := noticeSpecFor(event); err != nil || ok {
		t.Fatalf("spec=%#v ok=%v err=%v", spec, ok, err)
	}
}
