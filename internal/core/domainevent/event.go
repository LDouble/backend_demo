// Package domainevent defines durable, versioned integration-event records.
package domainevent

import "time"

const (
	// StatusPending indicates an event is ready for relay.
	StatusPending = "pending"
	// StatusLeased indicates an event is temporarily owned by a relay.
	StatusLeased = "leased"
	// StatusDispatched indicates successful delivery to subscribers.
	StatusDispatched = "dispatched"
	// StatusFailed indicates relay retries were exhausted.
	StatusFailed = "failed"
)

// Event is persisted transactionally with the aggregate mutation that produced it.
type Event struct {
	ID             uint64     `gorm:"primaryKey;autoIncrement" json:"id"`
	AggregateType  string     `gorm:"not null" json:"aggregate_type"`
	AggregateID    uint64     `gorm:"not null" json:"aggregate_id"`
	EventType      string     `gorm:"not null" json:"event_type"`
	PayloadVersion uint64     `gorm:"not null" json:"payload_version"`
	Payload        []byte     `gorm:"type:json;not null" json:"payload"`
	IdempotencyKey string     `gorm:"not null;uniqueIndex" json:"idempotency_key"`
	Status         string     `gorm:"not null;index" json:"status"`
	AvailableAt    time.Time  `gorm:"not null;index" json:"available_at"`
	LockedAt       *time.Time `json:"locked_at"`
	Attempts       int64      `gorm:"not null" json:"attempts"`
	LastError      *string    `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// TableName keeps this shared relay independent from notification outbox_events.
func (Event) TableName() string { return "domain_events" }
