package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/hibiken/asynq"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/domain"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/infrastructure"
	"gorm.io/gorm"
)

type recordingProvider struct{ calls int }

func (p *recordingProvider) Send(context.Context, *model.User, *domain.Notice, string, string) (string, error) {
	p.calls++
	return "provider-1", nil
}

func TestProcessorDeliveryIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &domain.Notice{}, &domain.NoticeDelivery{}, &domain.NoticeAudience{}, &domain.NoticeRecipient{}, &domain.OutboxEvent{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	user := model.User{Username: "member", PasswordHash: "hash", Status: model.UserActive}
	if err = db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	notice := domain.Notice{Title: "title", Summary: "summary", Body: "secret body", Category: "campus", Priority: domain.PriorityNormal, Status: domain.StatusPublished, Version: 1, CreatedBy: 1, UpdatedBy: 1}
	if err = db.Create(&notice).Error; err != nil {
		t.Fatal(err)
	}
	delivery := domain.NoticeDelivery{NoticeId: notice.ID, UserId: user.ID, Channel: domain.ChannelPush, Status: "pending", IdempotencyKey: "stable-key"}
	if err = db.Create(&delivery).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]uint64{"delivery_id": delivery.ID})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(taskPayload{EventID: 1, EventType: infrastructure.EventDelivery, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	task := asynq.NewTask(taskType, envelope)
	provider := &recordingProvider{}
	processor := NewProcessor(infrastructure.NewNoticeStore(db), provider)
	if err = processor.Handle(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err = processor.Handle(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls=%d, want 1", provider.calls)
	}
	if err = db.First(&delivery, delivery.ID).Error; err != nil {
		t.Fatal(err)
	}
	if delivery.Status != "sent" || delivery.ProviderMessageId == nil || *delivery.ProviderMessageId != "provider-1" {
		t.Fatalf("delivery=%+v", delivery)
	}
}

func TestProcessorKeepsFailedAndLeasedDeliveriesRetryable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &domain.Notice{}, &domain.NoticeDelivery{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	notice := domain.Notice{
		Title: "title", Summary: "summary", Body: "body", Category: "campus",
		Priority: domain.PriorityNormal, Status: domain.StatusPublished, Version: 1,
		CreatedBy: 1, UpdatedBy: 1,
	}
	if err = db.Create(&notice).Error; err != nil {
		t.Fatal(err)
	}
	processor := NewProcessor(infrastructure.NewNoticeStore(db), &recordingProvider{})

	missingUser := domain.NoticeDelivery{
		NoticeId: notice.ID, UserId: 999, Channel: domain.ChannelPush,
		Status: "pending", IdempotencyKey: "missing-user",
	}
	if err = db.Create(&missingUser).Error; err != nil {
		t.Fatal(err)
	}
	if err = processor.Handle(context.Background(), deliveryTask(t, missingUser.ID)); err == nil {
		t.Fatal("load failure was acknowledged")
	}
	if err = db.First(&missingUser, missingUser.ID).Error; err != nil {
		t.Fatal(err)
	}
	if missingUser.Status != "pending" || missingUser.LockedAt != nil {
		t.Fatalf("failed delivery retained lease: %+v", missingUser)
	}

	lockedAt := time.Now().UTC()
	leasing := domain.NoticeDelivery{
		NoticeId: notice.ID, UserId: 999, Channel: domain.ChannelPush,
		Status: "delivering", LockedAt: &lockedAt, IdempotencyKey: "leased",
	}
	if err = db.Create(&leasing).Error; err != nil {
		t.Fatal(err)
	}
	err = processor.Handle(context.Background(), deliveryTask(t, leasing.ID))
	if !errors.Is(err, infrastructure.ErrDeliveryLeaseHeld) {
		t.Fatalf("leased delivery error=%v", err)
	}
}

func deliveryTask(t *testing.T, deliveryID uint64) *asynq.Task {
	t.Helper()
	payload, err := json.Marshal(map[string]uint64{"delivery_id": deliveryID})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(taskPayload{
		EventID: 1, EventType: infrastructure.EventDelivery, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return asynq.NewTask(taskType, envelope)
}
