package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	marketplacedomain "github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
)

type marketplaceListingRequest struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	PriceCents  int64    `json:"price_cents"`
	ImageURLs   []string `json:"image_urls"`
	ContactType string   `json:"contact_type"`
	Contact     string   `json:"contact"`
}

type marketplaceVersionRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
}
type marketplaceOrderRequest struct {
	ListingID uint64 `json:"listing_id"`
}

func (h *Handler) createMarketplaceListing(c *gin.Context) {
	var req marketplaceListingRequest
	if !bind(c, &req) {
		return
	}
	listing, err := h.marketplace.Create(c.Request.Context(), c.GetUint64(userIDKey), marketplaceListingInput(req))
	if err != nil {
		failure(c, apperror.Wrap(http.StatusBadRequest, "invalid_listing", err.Error(), err))
		return
	}
	contact, err := h.marketplace.Contact(c.Request.Context(), listing, c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusCreated, marketplaceListingView{ID: listing.ID, Title: listing.Title, Description: listing.Description, PriceCents: listing.PriceCents, Currency: listing.Currency, Status: listing.Status, OwnerID: listing.OwnerId, ContactType: contact.Type, Contact: contact.Value, Version: listing.Version, CreatedAt: listing.CreatedAt, UpdatedAt: listing.UpdatedAt})
}
func (h *Handler) submitMarketplaceListing(c *gin.Context)   { h.changeMarketplaceListing(c, true) }
func (h *Handler) withdrawMarketplaceListing(c *gin.Context) { h.changeMarketplaceListing(c, false) }
func (h *Handler) changeMarketplaceListing(c *gin.Context, submit bool) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req marketplaceVersionRequest
	if !bind(c, &req) {
		return
	}
	var err error
	if submit {
		_, err = h.marketplace.Submit(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	} else {
		_, err = h.marketplace.Withdraw(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	}
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, gin.H{"updated": true})
}
func (h *Handler) createMarketplaceOrder(c *gin.Context) {
	var req marketplaceOrderRequest
	if !bind(c, &req) {
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	order, err := h.marketplace.Reserve(c.Request.Context(), req.ListingID, c.GetUint64(userIDKey), key)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusCreated, tradeOrderViewOf(order))
}
func marketplaceListingInput(req marketplaceListingRequest) marketplacedomain.ListingInput {
	provided := strings.TrimSpace(req.ContactType) != "" || strings.TrimSpace(req.Contact) != ""
	return marketplacedomain.ListingInput{Title: req.Title, Description: req.Description, PriceCents: req.PriceCents, ImageURLs: req.ImageURLs, Contact: marketplacedomain.ContactInput{Type: req.ContactType, Value: req.Contact, Provided: provided}}
}

type marketplaceListingView struct {
	ID          uint64    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	PriceCents  int64     `json:"price_cents"`
	Currency    string    `json:"currency"`
	Status      string    `json:"status"`
	OwnerID     uint64    `json:"owner_id"`
	ContactType string    `json:"contact_type"`
	Contact     string    `json:"contact"`
	Version     uint64    `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
