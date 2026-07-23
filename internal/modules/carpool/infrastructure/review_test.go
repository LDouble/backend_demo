package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
	"gorm.io/gorm"
)

func TestCarpoolReviewLifecycleControlsVisibility(t *testing.T) {
	db := newCarpoolTestDB(t)
	store := NewStore(db, nil)
	trip := createReviewTrip(t, db, domain.ReviewPending)
	now := time.Now().UTC().Truncate(time.Second)

	if rows, total, err := store.SearchTrips(context.Background(), domain.Search{}, 1, 20, now); err != nil ||
		total != 0 || len(rows) != 0 {
		t.Fatalf("pending rows=%+v total=%d err=%v", rows, total, err)
	}
	if _, _, err := store.GetTrip(context.Background(), trip.ID, trip.OrganizerId+1); err == nil {
		t.Fatal("GetTrip() stranger pending error=nil")
	}
	if owned, _, err := store.GetTrip(context.Background(), trip.ID, trip.OrganizerId); err != nil ||
		owned.ID != trip.ID {
		t.Fatalf("owner trip=%+v err=%v", owned, err)
	}

	approved, err := store.Review(
		context.Background(),
		trip.ID,
		99,
		trip.Version,
		true,
		"",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if approved.ReviewStatus != domain.ReviewApproved ||
		approved.ReviewReason != nil ||
		approved.Version != trip.Version+1 {
		t.Fatalf("approved=%+v", approved)
	}
	if rows, total, listErr := store.SearchTrips(context.Background(), domain.Search{}, 1, 20, now); listErr != nil ||
		total != 1 || len(rows) != 1 {
		t.Fatalf("approved rows=%+v total=%d err=%v", rows, total, listErr)
	}

	revoked, err := store.RevokeReview(
		context.Background(),
		trip.ID,
		100,
		approved.Version,
		"  收到举报，重新核验  ",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.ReviewStatus != domain.ReviewPending ||
		revoked.ReviewReason == nil ||
		*revoked.ReviewReason != "收到举报，重新核验" {
		t.Fatalf("revoked=%+v", revoked)
	}
}

func TestCarpoolRejectEditAndResubmit(t *testing.T) {
	db := newCarpoolTestDB(t)
	store := NewStore(db, nil)
	trip := createReviewTrip(t, db, domain.ReviewPending)
	now := time.Now().UTC()

	rejected, err := store.Review(
		context.Background(),
		trip.ID,
		99,
		trip.Version,
		false,
		"  出发地描述不清  ",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.ReviewStatus != domain.ReviewRejected ||
		rejected.ReviewReason == nil ||
		*rejected.ReviewReason != "出发地描述不清" {
		t.Fatalf("rejected=%+v", rejected)
	}
	updated, err := store.UpdateTrip(
		context.Background(),
		trip.ID,
		trip.OrganizerId,
		rejected.Version,
		domain.TripInput{
			Title:       "补充后的行程",
			Origin:      "东门公交站",
			Destination: trip.Destination,
			DepartureAt: trip.DepartureAt,
			TotalSeats:  trip.TotalSeats,
		},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ReviewStatus != domain.ReviewDraft ||
		updated.ReviewReason != nil ||
		updated.ReviewedBy != nil {
		t.Fatalf("updated=%+v", updated)
	}
	submitted, err := store.SubmitReview(
		context.Background(),
		trip.ID,
		trip.OrganizerId,
		updated.Version,
	)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.ReviewStatus != domain.ReviewPending ||
		submitted.Version != updated.Version+1 {
		t.Fatalf("submitted=%+v", submitted)
	}
}

func TestCarpoolModerationInvalidBranchesRollBack(t *testing.T) {
	tests := []struct {
		name     string
		version  uint64
		approved bool
		reason   string
	}{
		{name: "rejection reason required", version: 1},
		{name: "stale version", version: 99, approved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newCarpoolTestDB(t)
			store := NewStore(db, nil)
			trip := createReviewTrip(t, db, domain.ReviewPending)
			if _, err := store.Review(
				context.Background(),
				trip.ID,
				99,
				tt.version,
				tt.approved,
				tt.reason,
				time.Now().UTC(),
			); err == nil {
				t.Fatal("Review() error=nil")
			}
			var persisted domain.Trip
			if err := db.First(&persisted, trip.ID).Error; err != nil {
				t.Fatal(err)
			}
			if persisted.ReviewStatus != domain.ReviewPending ||
				persisted.Version != trip.Version ||
				persisted.ReviewedBy != nil {
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

func TestCarpoolAdminFiltersAndJoinRequireApproval(t *testing.T) {
	db := newCarpoolTestDB(t)
	store := NewStore(db, nil)
	target := createReviewTrip(t, db, domain.ReviewRejected)
	target.Title = "图书馆到高铁站"
	if err := db.Save(target).Error; err != nil {
		t.Fatal(err)
	}
	createReviewTrip(t, db, domain.ReviewPending)

	rows, total, err := store.ListAdmin(context.Background(), domain.AdminSearch{
		Status:       domain.TripOpen,
		ReviewStatus: domain.ReviewRejected,
		Keyword:      "高铁站",
	}, 1, 20)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != target.ID {
		t.Fatalf("rows=%+v total=%d err=%v", rows, total, err)
	}
	if _, err = store.Join(
		context.Background(),
		target.ID,
		target.OrganizerId+1,
		target.Version,
		time.Now().UTC(),
	); err == nil {
		t.Fatal("Join() rejected trip error=nil")
	}
}

func TestCarpoolListMineScopesFiltersAndPaginates(t *testing.T) {
	db := newCarpoolTestDB(t)
	store := NewStore(db, nil)
	target := createReviewTrip(t, db, domain.ReviewRejected)
	target.Title = "我的高铁拼车"
	if err := db.Save(target).Error; err != nil {
		t.Fatal(err)
	}
	createReviewTrip(t, db, domain.ReviewPending)
	foreign := createReviewTrip(t, db, domain.ReviewRejected)
	foreign.OrganizerId = 8
	foreign.Title = "他人的高铁拼车"
	if err := db.Save(foreign).Error; err != nil {
		t.Fatal(err)
	}

	rows, total, err := store.ListMine(context.Background(), 7, domain.AdminSearch{}, 1, 1)
	if err != nil || total != 2 || len(rows) != 1 {
		t.Fatalf("page rows=%+v total=%d err=%v", rows, total, err)
	}
	rows, total, err = store.ListMine(context.Background(), 7, domain.AdminSearch{
		Status:       domain.TripOpen,
		ReviewStatus: domain.ReviewRejected,
		Keyword:      " 高铁 ",
	}, 1, 20)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != target.ID {
		t.Fatalf("filtered rows=%+v total=%d err=%v", rows, total, err)
	}
}

func createReviewTrip(t *testing.T, db *gorm.DB, reviewStatus string) *domain.Trip {
	t.Helper()
	trip := &domain.Trip{
		Title:             "校园拼车",
		Origin:            "东门",
		Destination:       "高铁站",
		DepartureAt:       time.Now().UTC().Add(2 * time.Hour),
		TotalSeats:        3,
		Status:            domain.TripOpen,
		ReviewStatus:      reviewStatus,
		OrganizerId:       7,
		ContactType:       "wechat",
		ContactCiphertext: "ciphertext",
		Version:           1,
	}
	if err := db.Create(trip).Error; err != nil {
		t.Fatal(err)
	}
	return trip
}
