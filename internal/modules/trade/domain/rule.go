// Package domain contains shared trade-order rules.
package domain

// Trade order constants define stable cross-module semantics.
const (
	OrderTypeMarketplace = "marketplace"
	ResourceListing      = "marketplace_listing"
	PaymentOffline       = "offline"

	StatusConfirmed = "confirmed"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"

	FulfillmentNotStarted = "not_started"
	FulfillmentDelivered  = "delivered"
)

// CanTransition reports whether a trade state change is legal.
func CanTransition(from, to string) bool {
	return map[string]map[string]bool{
		StatusConfirmed: {StatusCompleted: true, StatusCancelled: true, StatusExpired: true},
	}[from][to]
}
