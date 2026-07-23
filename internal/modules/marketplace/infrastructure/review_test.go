package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
	"gorm.io/gorm"
)

func TestReviewMarketplaceListingDecisions(t *testing.T) {
	tests := []struct {
		name          string
		approved      bool
		reason        string
		wantStatus    string
		wantRejection string
	}{
		{
			name:       "approve publishes listing",
			approved:   true,
			reason:     "ignored",
			wantStatus: domain.ListingPublished,
		},
		{
			name:          "reject records trimmed reason",
			reason:        "  图片不清晰  ",
			wantStatus:    domain.ListingRejected,
			wantRejection: "图片不清晰",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newMarketplaceReviewTestDB(t)
			store := NewStore(db)
			listing := createPendingReviewListing(t, db)
			now := time.Now().UTC().Truncate(time.Second)

			reviewed, err := store.Review(
				context.Background(),
				listing.ID,
				99,
				listing.Version,
				tt.approved,
				tt.reason,
				now,
			)
			if err != nil {
				t.Fatal(err)
			}
			if reviewed.Status != tt.wantStatus || reviewed.Version != listing.Version+1 {
				t.Fatalf("reviewed=%+v", reviewed)
			}
			if reviewed.ReviewedBy == nil || *reviewed.ReviewedBy != 99 ||
				reviewed.ReviewedAt == nil || !reviewed.ReviewedAt.Equal(now) {
				t.Fatalf("review metadata=%+v", reviewed)
			}
			if tt.wantRejection == "" {
				if reviewed.RejectionReason != nil {
					t.Fatalf("rejection_reason=%q want nil", *reviewed.RejectionReason)
				}
			} else if reviewed.RejectionReason == nil || *reviewed.RejectionReason != tt.wantRejection {
				t.Fatalf("rejection_reason=%v want=%q", reviewed.RejectionReason, tt.wantRejection)
			}
			var event domainevent.Event
			if err = db.Where("aggregate_type = ? AND aggregate_id = ?", "listing", listing.ID).
				First(&event).Error; err != nil {
				t.Fatal(err)
			}
			if event.EventType != "listing.reviewed" {
				t.Fatalf("event_type=%q", event.EventType)
			}
		})
	}
}

func TestReviewMarketplaceListingRollsBackInvalidDecision(t *testing.T) {
	tests := []struct {
		name     string
		version  uint64
		approved bool
		reason   string
	}{
		{name: "empty rejection reason", version: 2},
		{name: "stale version", version: 1, approved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newMarketplaceReviewTestDB(t)
			store := NewStore(db)
			listing := createPendingReviewListing(t, db)

			if _, err := store.Review(
				context.Background(),
				listing.ID,
				99,
				tt.version,
				tt.approved,
				tt.reason,
				time.Now().UTC(),
			); err == nil {
				t.Fatal("Review() error=nil")
			}
			var persisted domain.Listing
			if err := db.First(&persisted, listing.ID).Error; err != nil {
				t.Fatal(err)
			}
			if persisted.Status != domain.ListingPendingReview ||
				persisted.Version != listing.Version ||
				persisted.ReviewedBy != nil ||
				persisted.ReviewedAt != nil {
				t.Fatalf("persisted=%+v", persisted)
			}
			var count int64
			if err := db.Model(&domainevent.Event{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("events=%d want=0", count)
			}
		})
	}
}

func TestRemoveMarketplaceListing(t *testing.T) {
	db := newMarketplaceReviewTestDB(t)
	store := NewStore(db)
	listing := createPendingReviewListing(t, db)
	now := time.Now().UTC().Truncate(time.Second)

	removed, err := store.Remove(context.Background(), listing.ID, 99, listing.Version, now)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Status != domain.ListingRemoved || removed.Version != listing.Version+1 {
		t.Fatalf("removed=%+v", removed)
	}
	if _, err = store.Remove(context.Background(), listing.ID, 99, removed.Version, now); err == nil {
		t.Fatal("second Remove() error=nil")
	}
	var count int64
	if err = db.Model(&domainevent.Event{}).
		Where("event_type = ?", "listing.removed").
		Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("removed events=%d want=1", count)
	}
}

func newMarketplaceReviewTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{TranslateError: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(
		&domain.Listing{},
		&domain.MarketplaceReservation{},
		&domainevent.Event{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func createPendingReviewListing(t *testing.T, db *gorm.DB) *domain.Listing {
	t.Helper()
	listing := &domain.Listing{
		Title:       "待审核教材",
		Description: "九成新",
		PriceCents:  1200,
		Currency:    domain.CurrencyCNY,
		Status:      domain.ListingPendingReview,
		OwnerId:     7,
		Version:     2,
	}
	if err := db.Create(listing).Error; err != nil {
		t.Fatal(err)
	}
	return listing
}
