package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
	"gorm.io/gorm"
)

func TestViewerRegistrationQueries(t *testing.T) {
	db := newActivityStoreTestDB(t)
	if err := db.AutoMigrate(&domain.ActivityRegistration{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	rows := []domain.ActivityRegistration{
		{ActivityId: 1, UserId: 9, Status: domain.RegistrationStatusActive, RegisteredAt: now, Version: 1},
		{ActivityId: 2, UserId: 9, Status: domain.RegistrationStatusCancelled, RegisteredAt: now, Version: 2},
		{ActivityId: 2, UserId: 10, Status: domain.RegistrationStatusActive, RegisteredAt: now, Version: 1},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	registered, err := store.IsViewerRegistered(context.Background(), 9, 1)
	if err != nil || !registered {
		t.Fatalf("registered=%t err=%v", registered, err)
	}
	notRegistered, err := store.IsViewerRegistered(context.Background(), 9, 2)
	if err != nil || notRegistered {
		t.Fatalf("notRegistered=%t err=%v", notRegistered, err)
	}
	anonymous, err := store.IsViewerRegistered(context.Background(), 0, 1)
	if err != nil || anonymous {
		t.Fatalf("anonymous=%t err=%v", anonymous, err)
	}
	batch, err := store.IsViewerRegisteredBatch(context.Background(), 9, []uint64{1, 2})
	if err != nil || !batch[1] || batch[2] {
		t.Fatalf("batch=%v err=%v", batch, err)
	}
	empty, err := store.IsViewerRegisteredBatch(context.Background(), 9, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty=%v err=%v", empty, err)
	}
	anonymousBatch, err := store.IsViewerRegisteredBatch(context.Background(), 0, []uint64{1})
	if err != nil || len(anonymousBatch) != 0 {
		t.Fatalf("anonymous batch=%v err=%v", anonymousBatch, err)
	}
}

func TestViewerRegistrationQueriesPreserveErrors(t *testing.T) {
	db := newActivityStoreTestDB(t)
	if err := db.AutoMigrate(&domain.ActivityRegistration{}); err != nil {
		t.Fatal(err)
	}
	want := errors.New("registrations unavailable")
	if err := db.Callback().Query().Before("gorm:query").Register("test:fail-registration-query", func(tx *gorm.DB) {
		if tx.Statement.Table == "activity_registrations" {
			_ = tx.AddError(want)
		}
	}); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	if registered, err := store.IsViewerRegistered(context.Background(), 9, 1); !errors.Is(err, want) || registered {
		t.Fatalf("registered=%t err=%v", registered, err)
	}
	batch, err := store.IsViewerRegisteredBatch(context.Background(), 9, []uint64{1})
	if !errors.Is(err, want) || batch != nil {
		t.Fatalf("batch=%v err=%v", batch, err)
	}
}

func TestPublisherCannotRegisterOwnActivity(t *testing.T) {
	db := newActivityStoreTestDB(t)
	if err := db.AutoMigrate(&domain.ActivityRegistration{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	activity := domain.Activity{
		Title: "owner activity", CreatedBy: 9, UpdatedBy: 9,
		Status: domain.ActivityStatusPublished, ReviewStatus: domain.ReviewStatusApproved,
		SignupStartAt: now.Add(-time.Hour), SignupEndAt: now.Add(time.Hour),
		StartAt: now.Add(2 * time.Hour), EndAt: now.Add(3 * time.Hour),
		Capacity: 2, Version: 1,
	}
	if err := db.Create(&activity).Error; err != nil {
		t.Fatal(err)
	}
	registration, _, err := NewStore(db).Register(
		context.Background(),
		activity.ID,
		activity.CreatedBy,
		"self-registration",
		now,
	)
	appErr, ok := apperror.As(err)
	if registration == nil || registration.ID != 0 || !ok || appErr.Status != 403 || appErr.Code != "self_registration" {
		t.Fatalf("registration=%v err=%v", registration, err)
	}
}
