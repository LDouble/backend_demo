package infrastructure

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
	"gorm.io/gorm"
)

func newCarpoolTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&domain.Trip{}, &domain.Participant{}, &domainevent.Event{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestLeavePreservesParticipantQueryErrors(t *testing.T) {
	db := newCarpoolTestDB(t)
	now := time.Now().UTC()
	trip := domain.Trip{
		Title: "trip", Origin: "station", Destination: "campus", DepartureAt: now.Add(time.Hour),
		TotalSeats: 2, OccupiedSeats: 1, Status: domain.TripOpen, OrganizerId: 1,
		ContactType: "phone", ContactCiphertext: "ciphertext", Version: 1,
	}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}
	want := errors.New("participant query unavailable")
	if err := db.Callback().Query().Before("gorm:query").Register("test:fail-participant-query", func(tx *gorm.DB) {
		if tx.Statement.Table == "carpool_participants" {
			_ = tx.AddError(want)
		}
	}); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, nil)
	if _, err := store.Leave(context.Background(), trip.ID, 2, trip.Version, now); !errors.Is(err, want) {
		t.Fatalf("Leave error=%v want=%v", err, want)
	}
}

func TestLeaveMapsMissingParticipantToForbidden(t *testing.T) {
	db := newCarpoolTestDB(t)
	now := time.Now().UTC()
	trip := domain.Trip{
		Title: "trip", Origin: "station", Destination: "campus", DepartureAt: now.Add(time.Hour),
		TotalSeats: 2, Status: domain.TripOpen, OrganizerId: 1,
		ContactType: "phone", ContactCiphertext: "ciphertext", Version: 1,
	}
	if err := db.Create(&trip).Error; err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, nil)
	_, err := store.Leave(context.Background(), trip.ID, 2, trip.Version, now)
	appErr, ok := apperror.As(err)
	if !ok || appErr.Status != 403 || appErr.Code != "not_participant" {
		t.Fatalf("Leave error=%v", err)
	}
}

func TestCompleteDueProcessesBoundedBatch(t *testing.T) {
	db := newCarpoolTestDB(t)
	now := time.Now().UTC()
	trips := make([]domain.Trip, 101)
	for index := range trips {
		trips[index] = domain.Trip{
			Title: "trip", Origin: "station", Destination: "campus", DepartureAt: now.Add(-time.Minute),
			TotalSeats: 2, Status: domain.TripOpen, OrganizerId: 1,
			ContactType: "phone", ContactCiphertext: "ciphertext", Version: 1,
		}
	}
	if err := db.Create(&trips).Error; err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, nil)
	completed, err := store.CompleteDue(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if completed != 100 {
		t.Fatalf("completed=%d want=100", completed)
	}
	var remaining int64
	if err = db.Model(&domain.Trip{}).Where("status = ?", domain.TripOpen).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining=%d want=1", remaining)
	}
}
