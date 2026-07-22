// Package payment defines the provider-agnostic internal payment contract.
package payment

import (
	"context"
	"fmt"
)

// Status describes the lifecycle of a payment intent.
type Status string

const (
	// StatusCreated is a locally created intent.
	StatusCreated Status = "created"
	// StatusPending is awaiting payer action or provider confirmation.
	StatusPending Status = "pending"
	// StatusSucceeded is a successfully paid intent.
	StatusSucceeded Status = "succeeded"
	// StatusFailed is a terminal payment failure.
	StatusFailed Status = "failed"
	// StatusCancelled is an intent cancelled before payment.
	StatusCancelled Status = "cancelled"
	// StatusRefunding is awaiting refund completion.
	StatusRefunding Status = "refunding"
	// StatusRefunded is a completed refund.
	StatusRefunded Status = "refunded"
)

// Intent is a reusable internal representation of a payment request.
type Intent struct {
	ID             string
	ResourceType   string
	ResourceID     string
	AmountCents    int64
	Currency       string
	Method         string
	Status         Status
	IdempotencyKey string
}

// Provider abstracts an eventual payment channel. It is intentionally not HTTP-facing.
type Provider interface {
	CreateIntent(context.Context, Intent) (Intent, error)
	Cancel(context.Context, Intent) (Intent, error)
	Refund(context.Context, Intent) (Intent, error)
	ParseCallback(context.Context, []byte, map[string]string) (Intent, error)
}

// ValidateIntent checks invariant fields shared by all future payment providers.
func ValidateIntent(intent Intent) error {
	if intent.ResourceType == "" || intent.ResourceID == "" || intent.Method == "" || intent.IdempotencyKey == "" {
		return fmt.Errorf("payment intent resource, method and idempotency key are required")
	}
	if intent.AmountCents <= 0 || intent.Currency != "CNY" {
		return fmt.Errorf("payment intent must use a positive CNY amount")
	}
	return nil
}
