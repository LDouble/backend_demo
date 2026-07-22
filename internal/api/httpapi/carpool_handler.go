package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/privacy"
	carpool "github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
)

type carpoolRequest struct {
	Title       string    `json:"title"`
	Origin      string    `json:"origin"`
	Destination string    `json:"destination"`
	DepartureAt time.Time `json:"departure_at"`
	TotalSeats  int64     `json:"total_seats"`
	ContactType string    `json:"contact_type"`
	Contact     string    `json:"contact"`
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
	views := make([]carpoolView, len(rows))
	for i := range rows {
		views[i] = h.carpoolView(&rows[i], false)
	}
	success(c, http.StatusOK, pageData(views, p, s, total))
}
func (h *Handler) createCarpoolTrip(c *gin.Context) {
	var req carpoolRequest
	if !bind(c, &req) {
		return
	}
	trip, err := h.carpools.Create(c.Request.Context(), c.GetUint64(userIDKey), carpool.TripInput{Title: req.Title, Origin: req.Origin, Destination: req.Destination, DepartureAt: req.DepartureAt, TotalSeats: req.TotalSeats, ContactType: req.ContactType, Contact: req.Contact})
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusCreated, h.carpoolView(trip, true))
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
	success(c, http.StatusOK, h.carpoolView(trip, visible))
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
	success(c, http.StatusOK, h.carpoolView(trip, false))
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
	success(c, http.StatusOK, h.carpoolView(trip, true))
}

type carpoolView struct {
	ID            uint64    `json:"id"`
	Title         string    `json:"title"`
	Origin        string    `json:"origin"`
	Destination   string    `json:"destination"`
	DepartureAt   time.Time `json:"departure_at"`
	TotalSeats    int64     `json:"total_seats"`
	OccupiedSeats int64     `json:"occupied_seats"`
	Status        string    `json:"status"`
	OrganizerID   uint64    `json:"organizer_id"`
	ContactType   string    `json:"contact_type"`
	Contact       string    `json:"contact"`
	Version       uint64    `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func (h *Handler) carpoolView(trip *carpool.Trip, full bool) carpoolView {
	contact := ""
	if plain, err := h.carpools.RevealContact(trip); err == nil {
		contact = privacy.MaskContact(plain)
		if full {
			contact = plain
		}
	}
	return carpoolView{ID: trip.ID, Title: trip.Title, Origin: trip.Origin, Destination: trip.Destination, DepartureAt: trip.DepartureAt, TotalSeats: trip.TotalSeats, OccupiedSeats: trip.OccupiedSeats, Status: trip.Status, OrganizerID: trip.OrganizerId, ContactType: trip.ContactType, Contact: strings.TrimSpace(contact), Version: trip.Version, CreatedAt: trip.CreatedAt, UpdatedAt: trip.UpdatedAt}
}
