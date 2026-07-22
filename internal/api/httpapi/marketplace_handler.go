package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	marketplacedomain "github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
)

type marketplaceListingRequest struct {
	ExpectedVersion uint64   `json:"expected_version"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	PriceCents      int64    `json:"price_cents"`
	ImageURLs       []string `json:"image_urls"`
	ContactType     string   `json:"contact_type"`
	Contact         string   `json:"contact"`
}

func (h *Handler) listMarketplaceListings(c *gin.Context) {
	h.listMarketplace(c, "published")
}

func (h *Handler) listMyMarketplaceListings(c *gin.Context) {
	h.listMarketplace(c, "mine")
}

func (h *Handler) listAdminMarketplaceListings(c *gin.Context) {
	h.listMarketplace(c, "admin")
}

func (h *Handler) listMarketplace(c *gin.Context, scope string) {
	page, size := paging(c)
	search := marketplacedomain.ListingSearch{
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		Status:   strings.TrimSpace(c.Query("status")),
		Page:     page,
		PageSize: size,
	}
	search.MinPriceCents = optionalInt64Query(c, "min_price_cents")
	search.MaxPriceCents = optionalInt64Query(c, "max_price_cents")
	var rows []marketplacedomain.ListingDetails
	var total int64
	var err error
	switch scope {
	case "mine":
		rows, total, err = h.marketplace.ListOwned(c.Request.Context(), c.GetUint64(userIDKey), search)
	case "admin":
		rows, total, err = h.marketplace.ListAdmin(c.Request.Context(), search)
	default:
		rows, total, err = h.marketplace.ListPublished(c.Request.Context(), search)
	}
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.marketplaceListingViews(c, rows)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(views, page, size, total))
}

func (h *Handler) getMarketplaceListing(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	details, err := h.marketplace.Get(c.Request.Context(), id, c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	view, err := h.marketplaceListingView(c, *details)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, view)
}

func (h *Handler) updateMarketplaceListing(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req marketplaceListingRequest
	if !bind(c, &req) {
		return
	}
	listing, err := h.marketplace.Update(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		req.ExpectedVersion,
		marketplaceListingInput(req),
	)
	if err != nil {
		failure(c, err)
		return
	}
	details, err := h.marketplace.Get(c.Request.Context(), listing.ID, c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	view, err := h.marketplaceListingView(c, *details)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, view)
}

func (h *Handler) removeMarketplaceListing(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	version, err := strconv.ParseUint(c.Query("expected_version"), 10, 64)
	if err != nil || version == 0 {
		failure(c, apperror.New(http.StatusBadRequest, "invalid_version", "expected_version 必须大于 0"))
		return
	}
	if _, err = h.marketplace.Remove(c.Request.Context(), id, c.GetUint64(userIDKey), version); err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, gin.H{"removed": true})
}

type marketplaceVersionRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
}
type marketplaceReviewRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
	Approved        bool   `json:"approved"`
	Reason          string `json:"reason"`
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
		failure(c, err)
		return
	}
	details, err := h.marketplace.Get(c.Request.Context(), listing.ID, c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	view, err := h.marketplaceListingView(c, *details)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusCreated, view)
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
func (h *Handler) reviewMarketplaceListing(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req marketplaceReviewRequest
	if !bind(c, &req) {
		return
	}
	_, err := h.marketplace.Review(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		req.ExpectedVersion,
		req.Approved,
		req.Reason,
	)
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
	ID              uint64     `json:"id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	PriceCents      int64      `json:"price_cents"`
	Currency        string     `json:"currency"`
	Status          string     `json:"status"`
	OwnerID         uint64     `json:"owner_id"`
	ImageURLs       []string   `json:"image_urls"`
	RejectionReason *string    `json:"rejection_reason,omitempty"`
	ReviewedBy      *uint64    `json:"reviewed_by,omitempty"`
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty"`
	ContactType     string     `json:"contact_type"`
	Contact         string     `json:"contact"`
	Version         uint64     `json:"version"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (h *Handler) marketplaceListingViews(c *gin.Context, rows []marketplacedomain.ListingDetails) ([]marketplaceListingView, error) {
	views := make([]marketplaceListingView, 0, len(rows))
	for _, row := range rows {
		view, err := h.marketplaceListingView(c, row)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (h *Handler) marketplaceListingView(c *gin.Context, details marketplacedomain.ListingDetails) (marketplaceListingView, error) {
	contact, err := h.marketplace.Contact(c.Request.Context(), &details.Listing, c.GetUint64(userIDKey))
	if err != nil {
		return marketplaceListingView{}, err
	}
	return marketplaceListingView{
		ID: details.ID, Title: details.Title, Description: details.Description,
		PriceCents: details.PriceCents, Currency: details.Currency, Status: details.Status,
		OwnerID: details.OwnerId, ImageURLs: append([]string{}, details.ImageURLs...),
		RejectionReason: details.RejectionReason, ReviewedBy: details.ReviewedBy, ReviewedAt: details.ReviewedAt,
		ContactType: contact.Type, Contact: contact.Value, Version: details.Version,
		CreatedAt: details.CreatedAt, UpdatedAt: details.UpdatedAt,
	}, nil
}

func optionalInt64Query(c *gin.Context, name string) *int64 {
	value := strings.TrimSpace(c.Query(name))
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}
