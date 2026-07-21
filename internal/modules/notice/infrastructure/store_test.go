package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/application"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/domain"
	"gorm.io/gorm"
)

func testStore(t *testing.T) (*NoticeStore, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &model.Role{}, &domain.Notice{}, &domain.NoticeAudience{}, &domain.NoticeRecipient{}, &domain.NoticeDelivery{}, &domain.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE casbin_rule (id INTEGER PRIMARY KEY, ptype TEXT, v0 TEXT, v1 TEXT, v2 TEXT, v3 TEXT, v4 TEXT, v5 TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	return NewNoticeStore(db), db
}

func TestStoreAudiencePublishInboxAndRead(t *testing.T) {
	store, db := testStore(t)
	ctx := context.Background()
	users := []model.User{{Username: "enabled", PasswordHash: "x", Status: model.UserActive}, {Username: "disabled", PasswordHash: "x", Status: model.UserDisabled}}
	if err := db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateAudience(ctx, domain.Audience{UserIDs: []uint64{users[1].ID}}); err == nil {
		t.Fatal("disabled explicit user must fail")
	}
	notice := &domain.Notice{Title: "title", Summary: "summary", Body: "body", Category: "campus", Priority: domain.PriorityNormal, Status: domain.StatusDraft, PushEnabled: true, Version: 1, CreatedBy: 1, UpdatedBy: 1}
	if err := store.Create(ctx, notice, []domain.NoticeAudience{{AudienceType: domain.AudienceAll, AudienceValue: "*"}}); err != nil {
		t.Fatal(err)
	}
	queued, err := store.QueuePublish(ctx, notice.ID, 1, 1, time.Now().Add(-time.Second))
	if err != nil || !queued {
		t.Fatalf("QueuePublish()=%v,%v", queued, err)
	}
	if err = store.Publish(ctx, notice.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = store.Publish(ctx, notice.ID, time.Now()); err != nil {
		t.Fatal("idempotent publish:", err)
	}
	rows, total, err := store.ListInbox(ctx, users[0].ID, application.InboxFilter{Page: 1, PageSize: 20})
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("ListInbox()=%+v,%d,%v", rows, total, err)
	}
	if count, err := store.UnreadCount(ctx, users[0].ID); err != nil || count != 1 {
		t.Fatalf("UnreadCount()=%d,%v", count, err)
	}
	when := time.Now()
	if err = store.MarkRead(ctx, users[0].ID, notice.ID, when); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkRead(ctx, users[0].ID, notice.ID, when); err != nil {
		t.Fatal("idempotent read:", err)
	}
	if count, err := store.UnreadCount(ctx, users[0].ID); err != nil || count != 0 {
		t.Fatalf("UnreadCount(after read)=%d,%v", count, err)
	}
	var deliveries int64
	if err = db.Model(&domain.NoticeDelivery{}).Count(&deliveries).Error; err != nil || deliveries != 1 {
		t.Fatalf("deliveries=%d err=%v", deliveries, err)
	}
}

func TestStoreOptimisticUpdateAndRevoke(t *testing.T) {
	store, _ := testStore(t)
	ctx := context.Background()
	notice := &domain.Notice{Title: "old", Summary: "", Body: "body", Category: "campus", Priority: domain.PriorityNormal, Status: domain.StatusDraft, Version: 1, CreatedBy: 1, UpdatedBy: 1}
	if err := store.Create(ctx, notice, []domain.NoticeAudience{{AudienceType: domain.AudienceAll, AudienceValue: "*"}}); err != nil {
		t.Fatal(err)
	}
	notice.Title = "new"
	if ok, err := store.UpdateDraft(ctx, notice, 2, nil); err != nil || ok {
		t.Fatalf("stale UpdateDraft()=%v,%v", ok, err)
	}
	if ok, err := store.UpdateDraft(ctx, notice, 1, []domain.NoticeAudience{{AudienceType: domain.AudienceAll, AudienceValue: "*"}}); err != nil || !ok {
		t.Fatalf("UpdateDraft()=%v,%v", ok, err)
	}
	if ok, err := store.QueuePublish(ctx, notice.ID, 1, 2, time.Now().Add(time.Hour)); err != nil || !ok {
		t.Fatalf("schedule=%v,%v", ok, err)
	}
	if ok, err := store.Revoke(ctx, notice.ID, 1, 3, time.Now()); err != nil || !ok {
		t.Fatalf("revoke=%v,%v", ok, err)
	}
	if _, err := store.GetInbox(ctx, 1, notice.ID); err == nil {
		t.Fatal("revoked notice must not be visible")
	}
}
