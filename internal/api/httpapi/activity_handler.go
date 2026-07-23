package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	activitydomain "github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
)

type activityRequest struct {
	Title           string    `json:"title"`
	Summary         string    `json:"summary"`
	Body            string    `json:"body"`
	Location        string    `json:"location"`
	SignupStartAt   time.Time `json:"signup_start_at"`
	SignupEndAt     time.Time `json:"signup_end_at"`
	StartAt         time.Time `json:"start_at"`
	EndAt           time.Time `json:"end_at"`
	Capacity        int64     `json:"capacity"`
	ContactType     string    `json:"contact_type"`
	Contact         string    `json:"contact"`
	ExpectedVersion uint64    `json:"expected_version"`
}

type activityVersionRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
}

type activityReviewRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
	ReviewComment   string `json:"review_comment"`
}

type activityView struct {
	ID              uint64    `json:"id"`
	Title           string    `json:"title"`
	Summary         string    `json:"summary"`
	Body            string    `json:"body"`
	Location        string    `json:"location"`
	SignupStartAt   time.Time `json:"signup_start_at"`
	SignupEndAt     time.Time `json:"signup_end_at"`
	StartAt         time.Time `json:"start_at"`
	EndAt           time.Time `json:"end_at"`
	Capacity        int64     `json:"capacity"`
	RegisteredCount int64     `json:"registered_count"`
	Status          string    `json:"status"`
	ReviewStatus    string    `json:"review_status"`
	ReviewComment   *string   `json:"review_comment,omitempty"`
	CreatedBy       uint64    `json:"created_by"`
	UpdatedBy       uint64    `json:"updated_by"`
	ContactType     string    `json:"contact_type"`
	Contact         string    `json:"contact"`
	Version         uint64    `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type myActivityRegistrationView struct {
	RegistrationID      uint64       `json:"registration_id"`
	ActivityID          uint64       `json:"activity_id"`
	Status              string       `json:"status"`
	RegisteredAt        time.Time    `json:"registered_at"`
	CancelledAt         *time.Time   `json:"cancelled_at,omitempty"`
	RegistrationVersion uint64       `json:"registration_version"`
	Activity            activityView `json:"activity"`
}

func (h *Handler) listAdminActivities(c *gin.Context) {
	p, s := paging(c)
	search, ok := parseActivitySearch(c, true)
	if !ok {
		return
	}
	rows, total, err := h.activities.ListAdmin(c.Request.Context(), search, p, s)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.activityViews(c, rows)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(views, p, s, total))
}

func (h *Handler) listMyActivities(c *gin.Context) {
	p, s := paging(c)
	search, ok := parseActivitySearch(c, true)
	if !ok {
		return
	}
	rows, total, err := h.activities.ListMine(c.Request.Context(), c.GetUint64(userIDKey), search, p, s)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.activityViews(c, rows)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(views, p, s, total))
}

func (h *Handler) createAdminActivity(c *gin.Context) {
	var req activityRequest
	if !bind(c, &req) {
		return
	}
	activity, err := h.activities.Create(c.Request.Context(), c.GetUint64(userIDKey), activityInput(req))
	if err != nil {
		failure(c, err)
		return
	}
	h.successActivity(c, http.StatusCreated, activity)
}

func (h *Handler) getAdminActivity(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	activity, err := h.activities.GetAdmin(c.Request.Context(), id)
	if err != nil {
		failure(c, err)
		return
	}
	h.successActivity(c, http.StatusOK, activity)
}

func (h *Handler) updateAdminActivity(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req activityRequest
	if !bind(c, &req) {
		return
	}
	activity, err := h.activities.Update(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion, activityInput(req))
	if err != nil {
		failure(c, err)
		return
	}
	h.successActivity(c, http.StatusOK, activity)
}

func (h *Handler) submitAdminActivityReview(c *gin.Context) {
	h.changeAdminActivity(c, h.activities.SubmitReview)
}
func (h *Handler) publishAdminActivity(c *gin.Context) {
	h.changeAdminActivity(c, h.activities.Publish)
}
func (h *Handler) cancelAdminActivity(c *gin.Context) { h.changeAdminActivity(c, h.activities.Cancel) }
func (h *Handler) finishAdminActivity(c *gin.Context) { h.changeAdminActivity(c, h.activities.Finish) }

func (h *Handler) approveAdminActivity(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req activityReviewRequest
	if !bind(c, &req) {
		return
	}
	activity, err := h.activities.Approve(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion, req.ReviewComment)
	if err != nil {
		failure(c, err)
		return
	}
	h.successActivity(c, http.StatusOK, activity)
}

func (h *Handler) rejectAdminActivity(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req activityReviewRequest
	if !bind(c, &req) {
		return
	}
	activity, err := h.activities.Reject(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion, req.ReviewComment)
	if err != nil {
		failure(c, err)
		return
	}
	h.successActivity(c, http.StatusOK, activity)
}

func (h *Handler) listActivities(c *gin.Context) {
	p, s := paging(c)
	search, ok := parseActivitySearch(c, false)
	if !ok {
		return
	}
	rows, total, err := h.activities.ListPublic(c.Request.Context(), activitydomain.PublicSearch{Keyword: search.Keyword, StartDate: search.StartDate}, p, s)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.activityViews(c, rows)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(views, p, s, total))
}

func (h *Handler) getActivity(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	activity, err := h.activities.GetPublic(c.Request.Context(), id, c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	h.successActivity(c, http.StatusOK, activity)
}

func (h *Handler) createActivityRegistration(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	registration, activity, err := h.activities.Register(c.Request.Context(), id, c.GetUint64(userIDKey), strings.TrimSpace(c.GetHeader("Idempotency-Key")))
	if err != nil {
		failure(c, err)
		return
	}
	view, err := h.activityView(c, activity)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusCreated, gin.H{
		"registration": gin.H{
			"id":                   registration.ID,
			"activity_id":          registration.ActivityId,
			"status":               registration.Status,
			"registered_at":        registration.RegisteredAt,
			"cancelled_at":         registration.CancelledAt,
			"registration_version": registration.Version,
		},
		"activity": view,
	})
}

func (h *Handler) cancelMyActivityRegistration(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req activityVersionRequest
	if !bind(c, &req) {
		return
	}
	registration, activity, err := h.activities.CancelRegistration(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	if err != nil {
		failure(c, err)
		return
	}
	view, err := h.activityView(c, activity)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, gin.H{
		"registration": gin.H{
			"id":                   registration.ID,
			"activity_id":          registration.ActivityId,
			"status":               registration.Status,
			"registered_at":        registration.RegisteredAt,
			"cancelled_at":         registration.CancelledAt,
			"registration_version": registration.Version,
		},
		"activity": view,
	})
}

func (h *Handler) listMyActivityRegistrations(c *gin.Context) {
	p, s := paging(c)
	rows, total, err := h.activities.ListMyRegistrations(c.Request.Context(), c.GetUint64(userIDKey), p, s)
	if err != nil {
		failure(c, err)
		return
	}
	views := make([]myActivityRegistrationView, 0, len(rows))
	for _, row := range rows {
		view, err := h.activityView(c, &row.Activity)
		if err != nil {
			failure(c, err)
			return
		}
		views = append(views, myActivityRegistrationView{
			RegistrationID:      row.Registration.ID,
			ActivityID:          row.Registration.ActivityId,
			Status:              row.Registration.Status,
			RegisteredAt:        row.Registration.RegisteredAt,
			CancelledAt:         row.Registration.CancelledAt,
			RegistrationVersion: row.Registration.Version,
			Activity:            view,
		})
	}
	success(c, http.StatusOK, pageData(views, p, s, total))
}

func (h *Handler) changeAdminActivity(c *gin.Context, change func(context.Context, uint64, uint64, uint64) (*activitydomain.Activity, error)) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req activityVersionRequest
	if !bind(c, &req) {
		return
	}
	activity, err := change(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	if err != nil {
		failure(c, err)
		return
	}
	h.successActivity(c, http.StatusOK, activity)
}

func (h *Handler) successActivity(c *gin.Context, status int, activity *activitydomain.Activity) {
	view, err := h.activityView(c, activity)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, status, view)
}

func (h *Handler) activityViews(c *gin.Context, rows []activitydomain.Activity) ([]activityView, error) {
	views := make([]activityView, 0, len(rows))
	ids := make([]uint64, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	viewerID := c.GetUint64(userIDKey)
	registered, err := h.activities.IsViewerRegisteredBatch(c.Request.Context(), viewerID, ids)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		view, err := h.activityViewWithAccess(c, &rows[i], registered[rows[i].ID])
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

// activityViewWithAccess is the list-path variant of activityView that takes
// a precomputed registration flag. Single-row callers use activityView
// (which falls back to issuing a fresh IsViewerRegistered query).
func (h *Handler) activityViewWithAccess(c *gin.Context, activity *activitydomain.Activity, registered bool) (activityView, error) {
	contact, err := h.activities.ContactWithAccess(c.Request.Context(), activity, c.GetUint64(userIDKey), registered)
	if err != nil {
		return activityView{}, err
	}
	return h.assembleActivityView(activity, contact), nil
}

func (h *Handler) activityView(c *gin.Context, activity *activitydomain.Activity) (activityView, error) {
	contact, err := h.activities.Contact(c.Request.Context(), activity, c.GetUint64(userIDKey))
	if err != nil {
		return activityView{}, err
	}
	return h.assembleActivityView(activity, contact), nil
}

func (h *Handler) assembleActivityView(activity *activitydomain.Activity, contact activitydomain.ContactDetails) activityView {
	return activityView{
		ID:              activity.ID,
		Title:           activity.Title,
		Summary:         activity.Summary,
		Body:            activity.Body,
		Location:        activity.Location,
		SignupStartAt:   activity.SignupStartAt,
		SignupEndAt:     activity.SignupEndAt,
		StartAt:         activity.StartAt,
		EndAt:           activity.EndAt,
		Capacity:        activity.Capacity,
		RegisteredCount: activity.RegisteredCount,
		Status:          activity.Status,
		ReviewStatus:    activity.ReviewStatus,
		ReviewComment:   activity.ReviewComment,
		CreatedBy:       activity.CreatedBy,
		UpdatedBy:       activity.UpdatedBy,
		ContactType:     contact.Type,
		Contact:         contact.Value,
		Version:         activity.Version,
		CreatedAt:       activity.CreatedAt,
		UpdatedAt:       activity.UpdatedAt,
	}
}

func activityInput(req activityRequest) activitydomain.ActivityInput {
	provided := strings.TrimSpace(req.ContactType) != "" || strings.TrimSpace(req.Contact) != ""
	return activitydomain.ActivityInput{
		Title:         req.Title,
		Summary:       req.Summary,
		Body:          req.Body,
		Location:      req.Location,
		SignupStartAt: req.SignupStartAt,
		SignupEndAt:   req.SignupEndAt,
		StartAt:       req.StartAt,
		EndAt:         req.EndAt,
		Capacity:      req.Capacity,
		Contact:       activitydomain.ContactInput{Type: req.ContactType, Value: req.Contact, Provided: provided},
	}
}

func parseActivitySearch(c *gin.Context, admin bool) (activitydomain.AdminSearch, bool) {
	search := activitydomain.AdminSearch{Keyword: c.Query("keyword")}
	if admin {
		search.Status = c.Query("status")
		search.ReviewStatus = c.Query("review_status")
	}
	if raw := c.Query("start_date"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			failure(c, apperror.New(http.StatusBadRequest, "invalid_start_date", "start_date 格式无效"))
			return activitydomain.AdminSearch{}, false
		}
		search.StartDate = &parsed
	}
	return search, true
}
