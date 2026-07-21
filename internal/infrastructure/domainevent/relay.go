// Package domainevent relays durable domain events to integration subscribers.
package domainevent

import (
	"context"
	"errors"
	"fmt"
	"time"

	core "github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	batchSize   = 100
	leasePeriod = 30 * time.Second
	maxAttempts = 5
)

// Publisher delivers a leased domain event to an integration boundary.
type Publisher interface {
	Publish(context.Context, core.Event) error
}

// LogPublisher provides a durable, structured audit delivery target until a
// deployment adds external event subscribers.
type LogPublisher struct{ log *zap.Logger }

// NewLogPublisher creates the built-in domain-event audit publisher.
func NewLogPublisher(log *zap.Logger) *LogPublisher { return &LogPublisher{log: log} }

// Publish emits one event as a structured audit record.
func (p *LogPublisher) Publish(_ context.Context, event core.Event) error {
	p.log.Info(
		"domain event dispatched",
		zap.Uint64("event_id", event.ID),
		zap.String("aggregate_type", event.AggregateType),
		zap.Uint64("aggregate_id", event.AggregateID),
		zap.String("event_type", event.EventType),
		zap.Uint64("payload_version", event.PayloadVersion),
	)
	return nil
}

// Relay leases pending domain events and publishes each event at least once.
type Relay struct {
	db        *gorm.DB
	publisher Publisher
	interval  time.Duration
	log       *zap.Logger
	clock     func() time.Time
}

// NewRelay creates a durable domain-event relay.
func NewRelay(db *gorm.DB, publisher Publisher, interval time.Duration, log *zap.Logger) *Relay {
	return &Relay{db: db, publisher: publisher, interval: interval, log: log, clock: time.Now}
}

// Run polls until ctx is canceled. Per-event failures are retried and do not
// stop unrelated worker responsibilities.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if err := r.tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.log.Error("domain event relay failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Relay) tick(ctx context.Context) error {
	now := r.clock().UTC().Truncate(time.Millisecond)
	events, err := r.lease(ctx, now)
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := r.publisher.Publish(ctx, event); err != nil {
			if releaseErr := r.release(ctx, event, err, now); releaseErr != nil {
				return releaseErr
			}
			continue
		}
		if err := r.markDispatched(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (r *Relay) lease(ctx context.Context, now time.Time) ([]core.Event, error) {
	events := []core.Event{}
	err := idempotency.DB(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		// GORM Gen cannot express SKIP LOCKED or the timed-out lease predicate.
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where(
				"available_at <= ? AND (status = ? OR (status = ? AND locked_at < ?))",
				now,
				core.StatusPending,
				core.StatusLeased,
				now.Add(-leasePeriod),
			).
			Order("id").
			Limit(batchSize)
		if err := query.Find(&events).Error; err != nil {
			return fmt.Errorf("lease domain events: %w", err)
		}
		if len(events) == 0 {
			return nil
		}
		ids := make([]uint64, 0, len(events))
		for i := range events {
			ids = append(ids, events[i].ID)
			events[i].Status = core.StatusLeased
			events[i].LockedAt = &now
			events[i].Attempts++
		}
		return tx.Model(&core.Event{}).
			Where("id IN ?", ids).
			Updates(map[string]any{
				"status":     core.StatusLeased,
				"locked_at":  now,
				"attempts":   gorm.Expr("attempts + 1"),
				"last_error": nil,
			}).Error
	})
	return events, err
}

func (r *Relay) markDispatched(ctx context.Context, event core.Event) error {
	changed := idempotency.DB(ctx, r.db).Model(&core.Event{}).
		Where("id = ? AND status = ? AND locked_at = ?", event.ID, core.StatusLeased, event.LockedAt).
		Updates(map[string]any{"status": core.StatusDispatched, "locked_at": nil, "last_error": nil})
	if changed.Error != nil {
		return fmt.Errorf("mark domain event dispatched: %w", changed.Error)
	}
	if changed.RowsAffected != 1 {
		return fmt.Errorf("domain event %d lease was lost before dispatch", event.ID)
	}
	return nil
}

func (r *Relay) release(ctx context.Context, event core.Event, cause error, now time.Time) error {
	message := cause.Error()
	if len(message) > 1000 {
		message = message[:1000]
	}
	status := core.StatusPending
	availableAt := now.Add(time.Second * time.Duration(event.Attempts))
	if event.Attempts >= maxAttempts {
		status = core.StatusFailed
		availableAt = now
	}
	changed := idempotency.DB(ctx, r.db).Model(&core.Event{}).
		Where("id = ? AND status = ? AND locked_at = ?", event.ID, core.StatusLeased, event.LockedAt).
		Updates(map[string]any{
			"status":       status,
			"locked_at":    nil,
			"available_at": availableAt,
			"last_error":   message,
		})
	if changed.Error != nil {
		return fmt.Errorf("release domain event: %w", changed.Error)
	}
	if changed.RowsAffected != 1 {
		return fmt.Errorf("domain event %d lease was lost before release", event.ID)
	}
	return nil
}
