package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
	"gorm.io/gorm"
)

func TestApprovePublishesPendingActivity(t *testing.T) {
	db := newActivityStoreTestDB(t)
	store := NewStore(db)
	now := time.Now().UTC().Truncate(time.Second)
	activity := domain.Activity{
		Title:        "待审核活动",
		Status:       domain.ActivityStatusDraft,
		ReviewStatus: domain.ReviewStatusPendingReview,
		CreatedBy:    7,
		UpdatedBy:    7,
		EndAt:        now.Add(time.Hour),
		Version:      2,
	}
	if err := db.Create(&activity).Error; err != nil {
		t.Fatal(err)
	}

	approved, err := store.Approve(
		context.Background(),
		activity.ID,
		99,
		activity.Version,
		"内容合规",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != domain.ActivityStatusPublished {
		t.Fatalf("status=%q want=%q", approved.Status, domain.ActivityStatusPublished)
	}
	if approved.ReviewStatus != domain.ReviewStatusApproved {
		t.Fatalf("review_status=%q want=%q", approved.ReviewStatus, domain.ReviewStatusApproved)
	}
	if approved.Version != activity.Version+1 {
		t.Fatalf("version=%d want=%d", approved.Version, activity.Version+1)
	}

	public, total, err := store.ListPublic(context.Background(), domain.PublicSearch{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(public) != 1 || public[0].ID != activity.ID {
		t.Fatalf("public=%+v total=%d", public, total)
	}

	replayed, err := store.Publish(
		context.Background(),
		approved.ID,
		activity.CreatedBy,
		approved.Version,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Version != approved.Version {
		t.Fatalf("replayed version=%d want=%d", replayed.Version, approved.Version)
	}
	var eventCount int64
	if err = db.Model(&domainevent.Event{}).Count(&eventCount).Error; err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("events=%d want=1", eventCount)
	}
}

func TestApproveRejectsExpiredActivity(t *testing.T) {
	db := newActivityStoreTestDB(t)
	store := NewStore(db)
	now := time.Now().UTC().Truncate(time.Second)
	activity := domain.Activity{
		Title:        "已结束活动",
		Status:       domain.ActivityStatusDraft,
		ReviewStatus: domain.ReviewStatusPendingReview,
		CreatedBy:    7,
		UpdatedBy:    7,
		EndAt:        now,
		Version:      2,
	}
	if err := db.Create(&activity).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := store.Approve(
		context.Background(),
		activity.ID,
		99,
		activity.Version,
		"",
		now,
	); err == nil {
		t.Fatal("Approve() error=nil, want expired activity conflict")
	}

	var persisted domain.Activity
	if err := db.First(&persisted, activity.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != domain.ActivityStatusDraft || persisted.ReviewStatus != domain.ReviewStatusPendingReview {
		t.Fatalf("persisted=%+v", persisted)
	}
}

func newActivityStoreTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{TranslateError: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&domain.Activity{}, &domainevent.Event{}); err != nil {
		t.Fatal(err)
	}
	return db
}
