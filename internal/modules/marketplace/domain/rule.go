// Package domain contains the marketplace aggregate rules.
package domain

import (
	"fmt"
	"net/url"
	"strings"
)

// Marketplace currency, listing states, and order states.
const (
	// CurrencyCNY is the only marketplace currency in the offline-payment release.
	CurrencyCNY = "CNY"

	// ListingDraft and the following constants define listing lifecycle states.
	ListingDraft         = "draft"
	ListingPendingReview = "pending_review"
	ListingPublished     = "published"
	ListingReserved      = "reserved"
	ListingSold          = "sold"
	ListingRejected      = "rejected"
	ListingWithdrawn     = "withdrawn"
	ListingRemoved       = "removed"

	// ReservationActive and the following constants define listing reservation states.
	ReservationActive    = "active"
	ReservationCompleted = "completed"
	ReservationCancelled = "cancelled"
	ReservationExpired   = "expired"
)

// ListingInput is the user-controlled mutable portion of a listing.
type ListingInput struct {
	Title       string
	Description string
	PriceCents  int64
	ImageURLs   []string
}

// ValidateListingInput validates untrusted listing content before persistence.
func ValidateListingInput(input ListingInput) error {
	if length := len([]rune(strings.TrimSpace(input.Title))); length == 0 || length > 200 {
		return fmt.Errorf("商品标题长度必须为 1-200 个字符")
	}
	if length := len([]rune(strings.TrimSpace(input.Description))); length == 0 || length > 20_000 {
		return fmt.Errorf("商品描述长度必须为 1-20000 个字符")
	}
	if input.PriceCents <= 0 {
		return fmt.Errorf("商品价格必须大于 0 分")
	}
	if len(input.ImageURLs) > 9 {
		return fmt.Errorf("商品图片最多 9 张")
	}
	for _, rawURL := range input.ImageURLs {
		value, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
		if err != nil || value.Scheme != "https" || value.Host == "" {
			return fmt.Errorf("商品图片必须为有效 HTTPS URL")
		}
	}
	return nil
}

// CanListingTransition reports whether a listing state change is valid.
func CanListingTransition(from, to string) bool {
	allowed := map[string]map[string]bool{
		ListingDraft:         {ListingPendingReview: true, ListingWithdrawn: true},
		ListingPendingReview: {ListingPublished: true, ListingRejected: true, ListingWithdrawn: true, ListingRemoved: true},
		ListingPublished:     {ListingReserved: true, ListingWithdrawn: true, ListingRemoved: true},
		ListingReserved:      {ListingPublished: true, ListingSold: true, ListingRemoved: true},
		ListingRejected:      {ListingDraft: true, ListingWithdrawn: true},
	}
	return allowed[from][to]
}
