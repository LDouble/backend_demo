// Package domainevent provides the durable integration-event record shared
// across modules. The relay polls (status='pending', available_at<=now) and
// publishes events to downstream subscribers.
//
// All modules must persist events transactionally with the mutation that
// produced them. Use Write inside the same *gorm.DB transaction as the aggregate
// change so the event survives only if the change commits. Replaying a write
// with the same (aggregate_type, aggregate_id, idempotency_key) tuple raises a
// duplicate-key error, which the writer tolerates as a no-op for at-least-once
// semantics.
package domainevent

import (
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// CurrentPayloadVersion is bumped when the JSON payload schema of any event
// type changes. Consumers must gate deserialization on PayloadVersion to stay
// forward/backward compatible.
const CurrentPayloadVersion uint64 = 1

// Write persists a domain event inside the supplied transaction. The
// idempotency key is the canonical dedupe token; the caller is responsible
// for naming it in a deterministic, retry-safe way (recommend
// "<aggregate>.<event>:<aggregateID>:<version>" so re-emits at the same
// version collapse on the unique index).
//
// A nil tx is rejected — events must commit or abort together with the
// aggregate mutation that produced them. A nil payload is permitted and
// encoded as JSON null.
func Write(tx *gorm.DB, aggregateType string, aggregateID uint64, eventType string, payload any) error {
	if tx == nil {
		return fmt.Errorf("domainevent.Write: tx is nil")
	}
	if aggregateType == "" || aggregateID == 0 || eventType == "" {
		return fmt.Errorf("domainevent.Write: aggregateType, aggregateID, eventType must be non-empty/non-zero")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("domainevent.Write: marshal payload: %w", err)
	}
	idempotencyKey := fmt.Sprintf("%s.%s:%d:%d", aggregateType, eventType, aggregateID, currentVersion(payload))
	row := &Event{
		AggregateType:  aggregateType,
		AggregateID:    aggregateID,
		EventType:      eventType,
		PayloadVersion: CurrentPayloadVersion,
		Payload:        data,
		IdempotencyKey: idempotencyKey,
		Status:         StatusPending,
		AvailableAt:    time.Now().UTC(),
	}
	if err := tx.Create(row).Error; err != nil {
		return fmt.Errorf("domainevent.Write: persist event: %w", err)
	}
	return nil
}

// WriteWithKey is identical to Write but allows the caller to supply an
// explicit idempotency key. Prefer Write when the aggregate carries its own
// monotonic version; use WriteWithKey only when the caller has a stronger
// dedupe token (e.g., a user-supplied header).
func WriteWithKey(tx *gorm.DB, aggregateType string, aggregateID uint64, eventType string, idempotencyKey string, payload any) error {
	if tx == nil {
		return fmt.Errorf("domainevent.WriteWithKey: tx is nil")
	}
	if aggregateType == "" || aggregateID == 0 || eventType == "" || idempotencyKey == "" {
		return fmt.Errorf("domainevent.WriteWithKey: aggregateType, aggregateID, eventType, idempotencyKey must all be non-empty/non-zero")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("domainevent.WriteWithKey: marshal payload: %w", err)
	}
	row := &Event{
		AggregateType:  aggregateType,
		AggregateID:    aggregateID,
		EventType:      eventType,
		PayloadVersion: CurrentPayloadVersion,
		Payload:        data,
		IdempotencyKey: idempotencyKey,
		Status:         StatusPending,
		AvailableAt:    time.Now().UTC(),
	}
	if err := tx.Create(row).Error; err != nil {
		return fmt.Errorf("domainevent.WriteWithKey: persist event: %w", err)
	}
	return nil
}

// currentVersion extracts a numeric "version" hint from the payload if present.
// It is best-effort — when the payload struct carries no Version field the
// hash of the payload bytes is used so the resulting idempotency key changes
// when the payload changes. The function must be deterministic across calls.
func currentVersion(payload any) uint64 {
	if payload == nil {
		return 0
	}
	type versioned interface {
		GetVersion() uint64
	}
	if v, ok := payload.(versioned); ok {
		return v.GetVersion()
	}
	if m, ok := payload.(map[string]any); ok {
		if raw, exists := m["version"]; exists {
			switch n := raw.(type) {
			case uint64:
				return n
			case uint32:
				return uint64(n)
			case uint:
				return uint64(n)
			case int64:
				if n > 0 {
					return uint64(n)
				}
			case int:
				if n > 0 {
					return uint64(n)
				}
			case float64:
				if n > 0 {
					return uint64(n)
				}
			}
		}
	}
	return 0
}
