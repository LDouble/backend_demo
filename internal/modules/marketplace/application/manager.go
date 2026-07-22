// Package application coordinates marketplace use cases.
package application

import (
	"context"
	"net/http"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
)

// Store contains aggregate operations that must remain transactional.
type Store interface {
	CreateListing(context.Context, uint64, domain.ListingInput) (*domain.Listing, error)
	UpdateListing(context.Context, uint64, uint64, uint64, domain.ListingInput) (*domain.Listing, error)
	Submit(context.Context, uint64, uint64, uint64) (*domain.Listing, error)
	Withdraw(context.Context, uint64, uint64, uint64) (*domain.Listing, error)
	Review(context.Context, uint64, uint64, uint64, bool, string, time.Time) (*domain.Listing, error)
	Remove(context.Context, uint64, uint64, uint64, time.Time) (*domain.Listing, error)
	Reserve(context.Context, uint64, uint64, string, time.Time) (*tradedomain.Order, error)
	Cancel(context.Context, uint64, uint64, uint64, time.Time) (*tradedomain.Order, error)
	Complete(context.Context, uint64, uint64, uint64, time.Time) (*tradedomain.Order, error)
	Contact(context.Context, *domain.Listing, uint64) (domain.ContactDetails, error)
	Contacts(context.Context, []domain.ListingDetails, uint64) (map[uint64]domain.ContactDetails, error)
	ExpireReservations(context.Context, time.Time) (int64, error)
	GetVisible(context.Context, uint64, uint64) (*domain.ListingDetails, error)
	ListPublished(context.Context, domain.ListingSearch) ([]domain.ListingDetails, int64, error)
	ListOwned(context.Context, uint64, domain.ListingSearch) ([]domain.ListingDetails, int64, error)
	ListAdmin(context.Context, domain.ListingSearch) ([]domain.ListingDetails, int64, error)
}

// Get returns a listing visible to a published viewer, owner, or active buyer.
func (m *Manager) Get(ctx context.Context, id, viewerID uint64) (*domain.ListingDetails, error) {
	return m.store.GetVisible(ctx, id, viewerID)
}

// ListPublished returns the public member catalog.
func (m *Manager) ListPublished(ctx context.Context, search domain.ListingSearch) ([]domain.ListingDetails, int64, error) {
	if err := normalizeSearch(&search); err != nil {
		return nil, 0, err
	}
	return m.store.ListPublished(ctx, search)
}

// ListOwned returns every listing state owned by one member.
func (m *Manager) ListOwned(ctx context.Context, ownerID uint64, search domain.ListingSearch) ([]domain.ListingDetails, int64, error) {
	if err := normalizeSearch(&search); err != nil {
		return nil, 0, err
	}
	return m.store.ListOwned(ctx, ownerID, search)
}

// ListAdmin returns moderator-facing listing history.
func (m *Manager) ListAdmin(ctx context.Context, search domain.ListingSearch) ([]domain.ListingDetails, int64, error) {
	if err := normalizeSearch(&search); err != nil {
		return nil, 0, err
	}
	return m.store.ListAdmin(ctx, search)
}

func normalizeSearch(search *domain.ListingSearch) error {
	if search.Page < 1 {
		search.Page = 1
	}
	if search.PageSize < 1 || search.PageSize > 100 {
		search.PageSize = 20
	}
	if search.MinPriceCents != nil && search.MaxPriceCents != nil && *search.MinPriceCents > *search.MaxPriceCents {
		return apperror.New(http.StatusBadRequest, "invalid_price_range", "最低价格不能高于最高价格")
	}
	return nil
}

// Contact returns a listing contact only when the store confirms the viewer is active.
func (m *Manager) Contact(ctx context.Context, listing *domain.Listing, viewerID uint64) (domain.ContactDetails, error) {
	return m.store.Contact(ctx, listing, viewerID)
}

// Contacts resolves contact visibility for a page with one authorization query.
func (m *Manager) Contacts(
	ctx context.Context,
	listings []domain.ListingDetails,
	viewerID uint64,
) (map[uint64]domain.ContactDetails, error) {
	return m.store.Contacts(ctx, listings, viewerID)
}

// Manager enforces input validation before the transactional repository boundary.
type Manager struct {
	store Store
	now   func() time.Time
}

// NewManager creates a marketplace service.
func NewManager(store Store) *Manager { return &Manager{store: store, now: time.Now} }

// Create validates and creates an owned listing draft.
func (m *Manager) Create(ctx context.Context, ownerID uint64, input domain.ListingInput) (*domain.Listing, error) {
	if err := domain.ValidateListingInput(input); err != nil {
		return nil, apperror.Wrap(http.StatusBadRequest, "invalid_listing", err.Error(), err)
	}
	return m.store.CreateListing(ctx, ownerID, input)
}

// Update changes editable listing content with optimistic locking.
func (m *Manager) Update(ctx context.Context, id, ownerID, version uint64, input domain.ListingInput) (*domain.Listing, error) {
	if err := domain.ValidateListingUpdateInput(input); err != nil {
		return nil, apperror.Wrap(http.StatusBadRequest, "invalid_listing", err.Error(), err)
	}
	return m.store.UpdateListing(ctx, id, ownerID, version, input)
}

// Submit moves an owned listing into moderation.
func (m *Manager) Submit(ctx context.Context, id, ownerID, version uint64) (*domain.Listing, error) {
	return m.store.Submit(ctx, id, ownerID, version)
}

// Withdraw closes an owned listing before sale.
func (m *Manager) Withdraw(ctx context.Context, id, ownerID, version uint64) (*domain.Listing, error) {
	return m.store.Withdraw(ctx, id, ownerID, version)
}

// Review approves or rejects a pending listing.
func (m *Manager) Review(ctx context.Context, id, adminID, version uint64, approved bool, reason string) (*domain.Listing, error) {
	return m.store.Review(ctx, id, adminID, version, approved, reason, m.now().UTC())
}

// Remove administratively removes a listing and its open order.
func (m *Manager) Remove(ctx context.Context, id, adminID, version uint64) (*domain.Listing, error) {
	return m.store.Remove(ctx, id, adminID, version, m.now().UTC())
}

// Reserve atomically reserves a published listing for a buyer.
func (m *Manager) Reserve(ctx context.Context, listingID, buyerID uint64, key string) (*tradedomain.Order, error) {
	return m.store.Reserve(ctx, listingID, buyerID, key, m.now().UTC())
}

// Cancel cancels an order as its buyer or seller.
func (m *Manager) Cancel(ctx context.Context, orderID, actorID, version uint64) (*tradedomain.Order, error) {
	return m.store.Cancel(ctx, orderID, actorID, version, m.now().UTC())
}

// Complete marks an order completed as its seller.
func (m *Manager) Complete(ctx context.Context, orderID, sellerID, version uint64) (*tradedomain.Order, error) {
	return m.store.Complete(ctx, orderID, sellerID, version, m.now().UTC())
}

// ExpireReservations expires due orders and releases their listings.
func (m *Manager) ExpireReservations(ctx context.Context) (int64, error) {
	return m.store.ExpireReservations(ctx, m.now().UTC())
}
