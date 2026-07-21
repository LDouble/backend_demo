// Package worker relays transactional outbox events to Asynq and handles them.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/domain"
	"github.com/weouc-plus/campus-platform/internal/modules/notice/infrastructure"
	"go.uber.org/zap"
)

const taskType = "notice:outbox"

type taskPayload struct {
	EventID   uint64          `json:"event_id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
}

// Provider sends one external notification with an application idempotency key.
type Provider interface {
	Send(context.Context, *model.User, *domain.Notice, string, string) (string, error)
}

// LogProvider is the first-release provider. It intentionally omits body text.
type LogProvider struct{ log *zap.Logger }

// NewLogProvider creates the body-safe logging provider.
func NewLogProvider(log *zap.Logger) *LogProvider { return &LogProvider{log: log} }

// Send records delivery identifiers without logging notification content.
func (p *LogProvider) Send(_ context.Context, user *model.User, notice *domain.Notice, channel, key string) (string, error) {
	providerID := fmt.Sprintf("log-%s", key)
	p.log.Info("notification delivered", zap.Uint64("notice_id", notice.ID), zap.Uint64("user_id", user.ID), zap.String("channel", channel), zap.String("provider_id", providerID))
	return providerID, nil
}

// Relay leases outbox rows and enqueues deterministic Asynq tasks.
type Relay struct {
	store    *infrastructure.NoticeStore
	client   *asynq.Client
	interval time.Duration
	log      *zap.Logger
}

// NewRelay creates an outbox relay.
func NewRelay(store *infrastructure.NoticeStore, client *asynq.Client, interval time.Duration, log *zap.Logger) *Relay {
	return &Relay{store: store, client: client, interval: interval, log: log}
}

// Run polls until the context is canceled.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		if err := r.tick(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.log.Error("outbox relay failed", zap.Error(err))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Relay) tick(ctx context.Context) error {
	events, err := r.store.LeaseOutbox(ctx, 100, time.Now().UTC(), 30*time.Second)
	if err != nil {
		return err
	}
	for _, event := range events {
		payload, marshalErr := json.Marshal(taskPayload{EventID: event.ID, EventType: event.EventType, Payload: event.Payload})
		if marshalErr != nil {
			return marshalErr
		}
		task := asynq.NewTask(taskType, payload, asynq.TaskID(fmt.Sprintf("notice-outbox-%d", event.ID)), asynq.MaxRetry(5), asynq.Timeout(30*time.Second), asynq.Queue("notifications"))
		_, enqueueErr := r.client.EnqueueContext(ctx, task)
		if enqueueErr != nil && !errors.Is(enqueueErr, asynq.ErrTaskIDConflict) {
			_ = r.store.ReleaseOutbox(ctx, event.ID, enqueueErr, time.Now().UTC())
			continue
		}
		if err := r.store.MarkOutboxDispatched(ctx, event.ID); err != nil {
			return err
		}
	}
	return nil
}

// Processor handles publication snapshots and individual external deliveries.
type Processor struct {
	store    *infrastructure.NoticeStore
	provider Provider
}

// NewProcessor creates a notice task processor.
func NewProcessor(store *infrastructure.NoticeStore, provider Provider) *Processor {
	return &Processor{store: store, provider: provider}
}

// Register attaches notice handlers to an Asynq mux.
func (p *Processor) Register(mux *asynq.ServeMux) { mux.HandleFunc(taskType, p.Handle) }

// Handle processes a publication or external delivery event.
func (p *Processor) Handle(ctx context.Context, task *asynq.Task) error {
	var envelope taskPayload
	if err := json.Unmarshal(task.Payload(), &envelope); err != nil {
		return fmt.Errorf("decode notice task: %w", err)
	}
	switch envelope.EventType {
	case infrastructure.EventPublish:
		var payload struct {
			NoticeID uint64 `json:"notice_id"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return err
		}
		return p.store.Publish(ctx, payload.NoticeID, time.Now().UTC())
	case infrastructure.EventDelivery:
		var payload struct {
			DeliveryID uint64 `json:"delivery_id"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return err
		}
		return p.deliver(ctx, payload.DeliveryID)
	default:
		return fmt.Errorf("unsupported notice event %q", envelope.EventType)
	}
}

func (p *Processor) deliver(ctx context.Context, id uint64) error {
	delivery, notice, user, err := p.store.LoadDelivery(ctx, id)
	if err != nil {
		return err
	}
	if delivery.Status == "sent" || delivery.Status == "canceled" {
		return nil
	}
	if notice.Status == domain.StatusRevoked {
		return p.store.FailDelivery(ctx, id, errors.New("notice revoked"), true)
	}
	providerID, err := p.provider.Send(ctx, user, notice, delivery.Channel, delivery.IdempotencyKey)
	if err == nil {
		return p.store.CompleteDelivery(ctx, id, providerID)
	}
	retry, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	if updateErr := p.store.FailDelivery(ctx, id, err, retry >= maxRetry); updateErr != nil {
		return updateErr
	}
	return err
}
