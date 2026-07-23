package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/api/generated"
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
	params, ok := generatedParams[generated.ListMarketplaceListingsParams](c, "ListMarketplaceListings")
	if !ok {
		missingMarketplaceListParams(c)
		return
	}
	h.listMarketplace(c, "published", marketplaceSearchFromPublicParams(params))
}

func (h *Handler) listMyMarketplaceListings(c *gin.Context) {
	params, ok := generatedParams[generated.ListMyMarketplaceListingsParams](c, "ListMyMarketplaceListings")
	if !ok {
		missingMarketplaceListParams(c)
		return
	}
	h.listMarketplace(c, "mine", marketplaceSearchFromMineParams(params))
}

func (h *Handler) listAdminMarketplaceListings(c *gin.Context) {
	params, ok := generatedParams[generated.ListAdminMarketplaceListingsParams](c, "ListAdminMarketplaceListings")
	if !ok {
		missingMarketplaceListParams(c)
		return
	}
	h.listMarketplace(c, "admin", marketplaceSearchFromAdminParams(params))
}

func (h *Handler) listMarketplace(c *gin.Context, scope string, search marketplacedomain.ListingSearch) {
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
	success(c, http.StatusOK, pageData(views, search.Page, search.PageSize, total))
}

func marketplacePaging(page, pageSize *int32) (int, int) {
	resultPage, resultSize := 1, 20
	if page != nil {
		resultPage = int(*page)
	}
	if pageSize != nil {
		resultSize = int(*pageSize)
	}
	return resultPage, resultSize
}

func marketplaceSearchFromPublicParams(params generated.ListMarketplaceListingsParams) marketplacedomain.ListingSearch {
	page, size := marketplacePaging(params.Page, params.PageSize)
	return marketplacedomain.ListingSearch{
		Keyword:       trimmedMarketplaceFilter(params.Keyword),
		MinPriceCents: params.MinPriceCents,
		MaxPriceCents: params.MaxPriceCents,
		Page:          page,
		PageSize:      size,
	}
}

func marketplaceSearchFromMineParams(params generated.ListMyMarketplaceListingsParams) marketplacedomain.ListingSearch {
	page, size := marketplacePaging(params.Page, params.PageSize)
	return marketplacedomain.ListingSearch{
		Status:   trimmedMarketplaceFilter(params.Status),
		Page:     page,
		PageSize: size,
	}
}

func marketplaceSearchFromAdminParams(params generated.ListAdminMarketplaceListingsParams) marketplacedomain.ListingSearch {
	page, size := marketplacePaging(params.Page, params.PageSize)
	return marketplacedomain.ListingSearch{
		Keyword:       trimmedMarketplaceFilter(params.Keyword),
		Status:        trimmedMarketplaceFilter(params.Status),
		MinPriceCents: params.MinPriceCents,
		MaxPriceCents: params.MaxPriceCents,
		Page:          page,
		PageSize:      size,
	}
}

func trimmedMarketplaceFilter(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func missingMarketplaceListParams(c *gin.Context) {
	failure(c, apperror.New(http.StatusBadRequest, "invalid_parameter", "缺少已校验的商品列表参数"))
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
	var req generated.UpdateMarketplaceListingJSONBody
	if !bind(c, &req) {
		return
	}
	listing, err := h.marketplace.Update(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		req.ExpectedVersion,
		marketplaceListingUpdateInput(req),
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
	params, ok := generatedParams[generated.RemoveMarketplaceListingParams](c, "RemoveMarketplaceListing")
	if !ok {
		failure(c, apperror.New(http.StatusBadRequest, "invalid_parameter", "缺少已校验的商品下架参数"))
		return
	}
	if _, err := h.marketplace.Remove(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		params.ExpectedVersion,
	); err != nil {
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
	return marketplacedomain.ListingInput{
		Title: req.Title, Description: req.Description, PriceCents: req.PriceCents,
		ImageURLs: req.ImageURLs, ImageURLsProvided: true,
		Contact: marketplacedomain.ContactInput{
			Type: req.ContactType, Value: req.Contact, Provided: provided,
		},
	}
}

func marketplaceListingUpdateInput(req generated.UpdateMarketplaceListingJSONBody) marketplacedomain.ListingInput {
	imageURLs := []string{}
	imagesProvided := req.ImageUrls != nil
	if imagesProvided {
		imageURLs = append(imageURLs, (*req.ImageUrls)...)
	}
	contactType := ""
	if req.ContactType != nil {
		contactType = *req.ContactType
	}
	contact := ""
	if req.Contact != nil {
		contact = *req.Contact
	}
	return marketplacedomain.ListingInput{
		Title: req.Title, Description: req.Description, PriceCents: req.PriceCents,
		ImageURLs: imageURLs, ImageURLsProvided: imagesProvided,
		Contact: marketplacedomain.ContactInput{
			Type: contactType, Value: contact,
			Provided: req.ContactType != nil || req.Contact != nil,
		},
	}
}

type marketplaceListingView struct {
	ID               uint64     `json:"id"`
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	PriceCents       int64      `json:"price_cents"`
	Currency         string     `json:"currency"`
	Status           string     `json:"status"`
	OwnerID          uint64     `json:"owner_id"`
	ImageURLs        []string   `json:"image_urls"`
	RejectionReason  *string    `json:"rejection_reason,omitempty"`
	ReviewedBy       *uint64    `json:"reviewed_by,omitempty"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
	ContactType      string     `json:"contact_type"`
	Contact          string     `json:"contact"`
	Version          uint64     `json:"version"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ViewerRelation   string     `json:"viewer_relation"`
	AvailableActions []string   `json:"available_actions"`
}

func (h *Handler) marketplaceListingViews(c *gin.Context, rows []marketplacedomain.ListingDetails) ([]marketplaceListingView, error) {
	viewerID := c.GetUint64(userIDKey)
	contacts, err := h.marketplace.Contacts(c.Request.Context(), rows, viewerID)
	if err != nil {
		return nil, err
	}
	relations, actions, err := h.marketplace.ViewerContexts(c.Request.Context(), rows, viewerID)
	if err != nil {
		return nil, err
	}
	views := make([]marketplaceListingView, 0, len(rows))
	for _, row := range rows {
		availableActions, actionErr := h.availableActionsForViewer(
			c,
			actions[row.ID],
			marketplacedomain.ActionPurchase,
		)
		if actionErr != nil {
			return nil, actionErr
		}
		views = append(views, marketplaceListingViewOf(
			row,
			contacts[row.ID],
			relations[row.ID],
			availableActions,
		))
	}
	return views, nil
}

func (h *Handler) marketplaceListingView(c *gin.Context, details marketplacedomain.ListingDetails) (marketplaceListingView, error) {
	viewerID := c.GetUint64(userIDKey)
	contact, err := h.marketplace.Contact(c.Request.Context(), &details.Listing, viewerID)
	if err != nil {
		return marketplaceListingView{}, err
	}
	relations, actions, err := h.marketplace.ViewerContexts(
		c.Request.Context(),
		[]marketplacedomain.ListingDetails{details},
		viewerID,
	)
	if err != nil {
		return marketplaceListingView{}, err
	}
	availableActions, err := h.availableActionsForViewer(
		c,
		actions[details.ID],
		marketplacedomain.ActionPurchase,
	)
	if err != nil {
		return marketplaceListingView{}, err
	}
	return marketplaceListingViewOf(details, contact, relations[details.ID], availableActions), nil
}

func marketplaceListingViewOf(
	details marketplacedomain.ListingDetails,
	contact marketplacedomain.ContactDetails,
	relation string,
	actions []string,
) marketplaceListingView {
	return marketplaceListingView{
		ID: details.ID, Title: details.Title, Description: details.Description,
		PriceCents: details.PriceCents, Currency: details.Currency, Status: details.Status,
		OwnerID: details.OwnerId, ImageURLs: append([]string{}, details.ImageURLs...),
		RejectionReason: details.RejectionReason, ReviewedBy: details.ReviewedBy, ReviewedAt: details.ReviewedAt,
		ContactType: contact.Type, Contact: contact.Value, Version: details.Version,
		CreatedAt: details.CreatedAt, UpdatedAt: details.UpdatedAt,
		ViewerRelation: relation, AvailableActions: actions,
	}
}
