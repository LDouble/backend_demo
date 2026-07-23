package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/api/generated"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/privacy"
	carpool "github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
)

type carpoolRequest struct {
	Title           string    `json:"title"`
	Origin          string    `json:"origin"`
	Destination     string    `json:"destination"`
	DepartureAt     time.Time `json:"departure_at"`
	TotalSeats      int64     `json:"total_seats"`
	ContactType     string    `json:"contact_type"`
	Contact         string    `json:"contact"`
	ExpectedVersion uint64    `json:"expected_version"`
}
type carpoolVersionRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
}

func (h *Handler) listCarpoolTrips(c *gin.Context) {
	p, s := paging(c)
	search := carpool.Search{Origin: c.Query("origin"), Destination: c.Query("destination")}
	if v := c.Query("seats_needed"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			failure(c, apperror.New(400, "invalid_seats_needed", "seats_needed 必须为正整数"))
			return
		}
		search.SeatsNeeded = n
	}
	if v := c.Query("departure_date"); v != "" {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			failure(c, apperror.New(400, "invalid_departure_date", "departure_date 格式无效"))
			return
		}
		search.DepartureDate = &d
	}
	rows, total, err := h.carpools.Search(c.Request.Context(), search, p, s)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.carpoolViews(c, rows, false)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(views, p, s, total))
}
func (h *Handler) createCarpoolTrip(c *gin.Context) {
	var req carpoolRequest
	if !bind(c, &req) {
		return
	}
	trip, err := h.carpools.Create(c.Request.Context(), c.GetUint64(userIDKey), carpool.TripInput{Title: req.Title, Origin: req.Origin, Destination: req.Destination, DepartureAt: req.DepartureAt, TotalSeats: req.TotalSeats, ContactType: req.ContactType, Contact: req.Contact, ContactProvided: true})
	if err != nil {
		failure(c, err)
		return
	}
	h.successCarpool(c, http.StatusCreated, trip, true)
}
func (h *Handler) updateCarpoolTrip(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req carpoolRequest
	if !bind(c, &req) {
		return
	}
	contactProvided := strings.TrimSpace(req.ContactType) != "" || strings.TrimSpace(req.Contact) != ""
	trip, err := h.carpools.Update(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion, carpool.TripInput{Title: req.Title, Origin: req.Origin, Destination: req.Destination, DepartureAt: req.DepartureAt, TotalSeats: req.TotalSeats, ContactType: req.ContactType, Contact: req.Contact, ContactProvided: contactProvided})
	if err != nil {
		failure(c, err)
		return
	}
	h.successCarpool(c, http.StatusOK, trip, true)
}
func (h *Handler) listAdminCarpoolTrips(c *gin.Context) {
	params, ok := generatedParams[generated.ListAdminCarpoolTripsParams](c, "ListAdminCarpoolTrips")
	if !ok {
		failure(c, apperror.New(400, "invalid_parameter", "缺少已校验的拼车列表参数"))
		return
	}
	p, size := paging(c)
	rows, total, err := h.carpools.ListAdmin(c.Request.Context(), carpool.AdminSearch{
		Status: trimmedCarpoolFilter(params.Status), ReviewStatus: trimmedCarpoolFilter(params.ReviewStatus),
		Keyword: trimmedCarpoolFilter(params.Keyword),
	}, p, size)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.carpoolViews(c, rows, false)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(views, p, size, total))
}
func (h *Handler) listMyCarpoolTrips(c *gin.Context) {
	params, ok := generatedParams[generated.ListMyCarpoolTripsParams](c, "ListMyCarpoolTrips")
	if !ok {
		failure(c, apperror.New(400, "invalid_parameter", "缺少已校验的拼车列表参数"))
		return
	}
	p, size := paging(c)
	rows, total, err := h.carpools.ListMine(c.Request.Context(), c.GetUint64(userIDKey), carpool.AdminSearch{
		Status: trimmedCarpoolFilter(params.Status), ReviewStatus: trimmedCarpoolFilter(params.ReviewStatus),
		Keyword: trimmedCarpoolFilter(params.Keyword),
	}, p, size)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.carpoolViews(c, rows, true)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(views, p, size, total))
}
func (h *Handler) submitCarpoolTripReview(c *gin.Context) {
	h.carpoolChange(c, func(id, user, version uint64) (*carpool.Trip, error) {
		return h.carpools.SubmitReview(c.Request.Context(), id, user, version)
	})
}
func (h *Handler) reviewCarpoolTrip(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req generated.ReviewCarpoolTripJSONBody
	if !bind(c, &req) {
		return
	}
	reason := ""
	if req.Reason != nil {
		reason = *req.Reason
	}
	trip, err := h.carpools.Review(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion, req.Approved, reason)
	if err != nil {
		failure(c, err)
		return
	}
	h.successCarpool(c, http.StatusOK, trip, false)
}
func (h *Handler) revokeCarpoolTripReview(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req generated.RevokeCarpoolTripReviewJSONBody
	if !bind(c, &req) {
		return
	}
	trip, err := h.carpools.RevokeReview(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion, req.Reason)
	if err != nil {
		failure(c, err)
		return
	}
	h.successCarpool(c, http.StatusOK, trip, false)
}
func trimmedCarpoolFilter(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
func (h *Handler) getCarpoolTrip(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	trip, visible, err := h.carpools.Get(c.Request.Context(), id, c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	h.successCarpool(c, http.StatusOK, trip, visible)
}
func (h *Handler) joinCarpoolTrip(c *gin.Context) {
	h.carpoolChange(c, func(id, user, version uint64) (*carpool.Trip, error) {
		return h.carpools.Join(c.Request.Context(), id, user, version)
	})
}
func (h *Handler) leaveCarpoolTrip(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req carpoolVersionRequest
	if !bind(c, &req) {
		return
	}
	trip, err := h.carpools.Leave(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	if err != nil {
		failure(c, err)
		return
	}
	h.successCarpool(c, http.StatusOK, trip, false)
}
func (h *Handler) cancelCarpoolTrip(c *gin.Context) {
	h.carpoolChange(c, func(id, user, version uint64) (*carpool.Trip, error) {
		return h.carpools.Cancel(c.Request.Context(), id, user, version)
	})
}
func (h *Handler) carpoolChange(c *gin.Context, change func(uint64, uint64, uint64) (*carpool.Trip, error)) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req carpoolVersionRequest
	if !bind(c, &req) {
		return
	}
	trip, err := change(id, c.GetUint64(userIDKey), req.ExpectedVersion)
	if err != nil {
		failure(c, err)
		return
	}
	h.successCarpool(c, http.StatusOK, trip, true)
}

type carpoolView struct {
	ID               uint64     `json:"id"`
	Title            string     `json:"title"`
	Origin           string     `json:"origin"`
	Destination      string     `json:"destination"`
	DepartureAt      time.Time  `json:"departure_at"`
	TotalSeats       int64      `json:"total_seats"`
	OccupiedSeats    int64      `json:"occupied_seats"`
	Status           string     `json:"status"`
	ReviewStatus     string     `json:"review_status"`
	ReviewReason     *string    `json:"review_reason"`
	ReviewedBy       *uint64    `json:"reviewed_by"`
	ReviewedAt       *time.Time `json:"reviewed_at"`
	OrganizerID      uint64     `json:"organizer_id"`
	ContactType      string     `json:"contact_type"`
	Contact          string     `json:"contact"`
	Version          uint64     `json:"version"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ViewerRelation   string     `json:"viewer_relation"`
	AvailableActions []string   `json:"available_actions"`
}

func (h *Handler) carpoolViews(c *gin.Context, trips []carpool.Trip, full bool) ([]carpoolView, error) {
	viewerID := c.GetUint64(userIDKey)
	relations, actions, err := h.carpools.ViewerContexts(c.Request.Context(), trips, viewerID)
	if err != nil {
		return nil, err
	}
	views := make([]carpoolView, len(trips))
	for i := range trips {
		availableActions, actionErr := h.availableActionsForViewer(
			c,
			actions[trips[i].ID],
			carpool.ActionJoin,
		)
		if actionErr != nil {
			return nil, actionErr
		}
		views[i] = h.carpoolViewOf(&trips[i], full, relations[trips[i].ID], availableActions)
	}
	return views, nil
}

func (h *Handler) carpoolView(c *gin.Context, trip *carpool.Trip, full bool) (carpoolView, error) {
	relations, actions, err := h.carpools.ViewerContexts(
		c.Request.Context(),
		[]carpool.Trip{*trip},
		c.GetUint64(userIDKey),
	)
	if err != nil {
		return carpoolView{}, err
	}
	availableActions, err := h.availableActionsForViewer(c, actions[trip.ID], carpool.ActionJoin)
	if err != nil {
		return carpoolView{}, err
	}
	return h.carpoolViewOf(trip, full, relations[trip.ID], availableActions), nil
}

func (h *Handler) carpoolViewOf(trip *carpool.Trip, full bool, relation string, actions []string) carpoolView {
	contact := ""
	if plain, err := h.carpools.RevealContact(trip); err == nil {
		contact = privacy.MaskContact(plain)
		if full {
			contact = plain
		}
	}
	return carpoolView{ID: trip.ID, Title: trip.Title, Origin: trip.Origin, Destination: trip.Destination, DepartureAt: trip.DepartureAt, TotalSeats: trip.TotalSeats, OccupiedSeats: trip.OccupiedSeats, Status: trip.Status, ReviewStatus: trip.ReviewStatus, ReviewReason: trip.ReviewReason, ReviewedBy: trip.ReviewedBy, ReviewedAt: trip.ReviewedAt, OrganizerID: trip.OrganizerId, ContactType: trip.ContactType, Contact: strings.TrimSpace(contact), Version: trip.Version, CreatedAt: trip.CreatedAt, UpdatedAt: trip.UpdatedAt, ViewerRelation: relation, AvailableActions: actions}
}

func (h *Handler) successCarpool(c *gin.Context, status int, trip *carpool.Trip, full bool) {
	view, err := h.carpoolView(c, trip, full)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, status, view)
}
