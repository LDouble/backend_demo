package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	marketplacedomain "github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
)

type marketplaceListingRequest struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	PriceCents  int64    `json:"price_cents"`
	ImageURLs   []string `json:"image_urls"`
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
	success(c, http.StatusCreated, listing)
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
	return marketplacedomain.ListingInput{Title: req.Title, Description: req.Description, PriceCents: req.PriceCents, ImageURLs: req.ImageURLs}
}
