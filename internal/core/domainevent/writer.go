// Package domainevent provides durable integration-event records shared by modules.
// Events must be written with the aggregate mutation's *gorm.DB transaction.
package domainevent

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
)

// CurrentPayloadVersion identifies the current event JSON contract.
const CurrentPayloadVersion uint64 = 1

// ErrConflict means a deduplication key was reused for different event content.
var ErrConflict = errors.New("domain event idempotency key conflict")

// Write persists an event using aggregate version or payload content as its dedupe source.
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
	sourceKey := strconv.FormatUint(currentVersion(payload), 10)
	if currentVersion(payload) == 0 {
		sourceKey = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	return persist(tx, newEvent(
		aggregateType,
		aggregateID,
		eventType,
		eventKey(aggregateType, aggregateID, eventType, sourceKey),
		data,
	))
}

// WriteWithKey persists an event using an explicit caller-owned dedupe token.
func WriteWithKey(
	tx *gorm.DB,
	aggregateType string,
	aggregateID uint64,
	eventType string,
	idempotencyKey string,
	payload any,
) error {
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
	return persist(tx, newEvent(
		aggregateType,
		aggregateID,
		eventType,
		eventKey(aggregateType, aggregateID, eventType, idempotencyKey),
		data,
	))
}

func newEvent(aggregateType string, aggregateID uint64, eventType, key string, payload []byte) *Event {
	return &Event{
		AggregateType:  aggregateType,
		AggregateID:    aggregateID,
		EventType:      eventType,
		PayloadVersion: CurrentPayloadVersion,
		Payload:        payload,
		IdempotencyKey: key,
		Status:         StatusPending,
		AvailableAt:    time.Now().UTC(),
	}
}

func eventKey(aggregateType string, aggregateID uint64, eventType, sourceKey string) string {
	value := aggregateType + "\x00" + strconv.FormatUint(aggregateID, 10) + "\x00" + eventType + "\x00" + sourceKey
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func persist(tx *gorm.DB, row *Event) error {
	if err := tx.Create(row).Error; err == nil {
		return nil
	}
	var existing Event
	if err := tx.Where("idempotency_key = ?", row.IdempotencyKey).Take(&existing).Error; err != nil {
		return fmt.Errorf("domainevent: persist event: %w", err)
	}
	sameIdentity := existing.AggregateType == row.AggregateType &&
		existing.AggregateID == row.AggregateID &&
		existing.EventType == row.EventType &&
		existing.PayloadVersion == row.PayloadVersion
	if !sameIdentity || !bytes.Equal(existing.Payload, row.Payload) {
		return fmt.Errorf("%w: key=%s", ErrConflict, row.IdempotencyKey)
	}
	return nil
}

func currentVersion(payload any) uint64 {
	if payload == nil {
		return 0
	}
	type versioned interface {
		GetVersion() uint64
	}
	if value, ok := payload.(versioned); ok {
		return value.GetVersion()
	}
	values, ok := payload.(map[string]any)
	if !ok {
		return 0
	}
	raw, exists := values["version"]
	if !exists {
		return 0
	}
	switch number := raw.(type) {
	case uint64:
		return number
	case uint32:
		return uint64(number)
	case uint:
		return uint64(number)
	case int64:
		if number > 0 {
			return uint64(number)
		}
	case int:
		if number > 0 {
			return uint64(number)
		}
	case float64:
		if number > 0 {
			return uint64(number)
		}
	}
	return 0
}
