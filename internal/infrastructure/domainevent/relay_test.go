package domainevent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	core "github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type testPublisher struct {
	err    error
	events []core.Event
}

func (p *testPublisher) Publish(_ context.Context, event core.Event) error {
	p.events = append(p.events, event)
	return p.err
}

func TestRelayDispatchesAndRetriesEvents(t *testing.T) {
	tests := []struct {
		name       string
		publisher  *testPublisher
		iterations int
		wantStatus string
		wantCalls  int
		wantTry    int64
	}{
		{
			name:       "dispatches successfully",
			publisher:  &testPublisher{},
			iterations: 1,
			wantStatus: core.StatusDispatched,
			wantCalls:  1,
			wantTry:    1,
		},
		{
			name:       "marks permanently failing event",
			publisher:  &testPublisher{err: errors.New("publisher unavailable")},
			iterations: maxAttempts,
			wantStatus: core.StatusFailed,
			wantCalls:  maxAttempts,
			wantTry:    maxAttempts,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err = db.AutoMigrate(&core.Event{}); err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
			event := core.Event{
				AggregateType:  "activity",
				AggregateID:    7,
				EventType:      "activity.created",
				PayloadVersion: 1,
				Payload:        []byte(`{"id":7}`),
				IdempotencyKey: "event-" + tt.name,
				Status:         core.StatusPending,
				AvailableAt:    now,
			}
			if err = db.Create(&event).Error; err != nil {
				t.Fatal(err)
			}
			relay := NewRelay(db, tt.publisher, time.Hour, zap.NewNop())
			relay.clock = func() time.Time { return now }
			for i := 0; i < tt.iterations; i++ {
				if err = relay.tick(context.Background()); err != nil {
					t.Fatal(err)
				}
				now = now.Add(time.Minute)
			}
			var got core.Event
			if err = db.First(&got, event.ID).Error; err != nil {
				t.Fatal(err)
			}
			if got.Status != tt.wantStatus || got.Attempts != tt.wantTry {
				t.Fatalf("event status=%q attempts=%d", got.Status, got.Attempts)
			}
			if len(tt.publisher.events) != tt.wantCalls {
				t.Fatalf("publisher calls=%d", len(tt.publisher.events))
			}
		})
	}
}
