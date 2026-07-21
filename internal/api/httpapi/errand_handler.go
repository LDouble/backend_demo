package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	erraddomain "github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
)

type errandRequest struct {
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	RewardCents     int64     `json:"reward_cents"`
	PickupLocation  string    `json:"pickup_location"`
	DropoffLocation string    `json:"dropoff_location"`
	Deadline        time.Time `json:"deadline"`
	ExpectedVersion uint64    `json:"expected_version"`
	ContactType     string    `json:"contact_type"`
	Contact         string    `json:"contact"`
}
type errandVersionRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
}

func (h *Handler) listErrands(c *gin.Context) {
	p, s := paging(c)
	rows, total, err := h.errands.ListOpen(c.Request.Context(), p, s)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.errandViews(c, rows)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(views, p, s, total))
}
func (h *Handler) listMyErrands(c *gin.Context) {
	p, s := paging(c)
	rows, total, err := h.errands.ListMine(c.Request.Context(), c.GetUint64(userIDKey), p, s)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.errandViews(c, rows)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, pageData(views, p, s, total))
}
func (h *Handler) createErrand(c *gin.Context) {
	var req errandRequest
	if !bind(c, &req) {
		return
	}
	task, err := h.errands.Create(c.Request.Context(), c.GetUint64(userIDKey), errandInput(req))
	if err != nil {
		failure(c, apperror.Wrap(400, "invalid_errand", err.Error(), err))
		return
	}
	h.successErrand(c, http.StatusCreated, task)
}
func (h *Handler) getErrand(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	task, err := h.errands.Get(c.Request.Context(), id)
	if err != nil {
		failure(c, err)
		return
	}
	h.successErrand(c, http.StatusOK, task)
}
func (h *Handler) updateErrand(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req errandRequest
	if !bind(c, &req) {
		return
	}
	task, err := h.errands.Update(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion, errandInput(req))
	if err != nil {
		var appErr *apperror.Error
		if errors.As(err, &appErr) {
			failure(c, err)
		} else {
			failure(c, apperror.Wrap(400, "invalid_errand", err.Error(), err))
		}
		return
	}
	h.successErrand(c, http.StatusOK, task)
}
func (h *Handler) acceptErrand(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req errandVersionRequest
	if !bind(c, &req) {
		return
	}
	task, order, err := h.errands.Accept(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion, strings.TrimSpace(c.GetHeader("Idempotency-Key")))
	if err != nil {
		failure(c, err)
		return
	}
	view, err := h.errandView(c, task)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusCreated, gin.H{"errand": view, "order": tradeOrderViewOf(order)})
}
func (h *Handler) pickupErrand(c *gin.Context)  { h.moveErrand(c, true) }
func (h *Handler) deliverErrand(c *gin.Context) { h.moveErrand(c, false) }
func (h *Handler) moveErrand(c *gin.Context, pickup bool) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req errandVersionRequest
	if !bind(c, &req) {
		return
	}
	var task *erraddomain.Task
	var err error
	if pickup {
		task, err = h.errands.Pickup(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	} else {
		task, err = h.errands.Deliver(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	}
	if err != nil {
		failure(c, err)
		return
	}
	h.successErrand(c, http.StatusOK, task)
}
func (h *Handler) completeErrand(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req errandVersionRequest
	if !bind(c, &req) {
		return
	}
	task, order, err := h.errands.Complete(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	if err != nil {
		failure(c, err)
		return
	}
	view, err := h.errandView(c, task)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, gin.H{"errand": view, "order": tradeOrderViewOf(order)})
}
func (h *Handler) cancelErrand(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req errandVersionRequest
	if !bind(c, &req) {
		return
	}
	task, order, err := h.errands.Cancel(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	if err != nil {
		failure(c, err)
		return
	}
	view, err := h.errandView(c, task)
	if err != nil {
		failure(c, err)
		return
	}
	out := gin.H{"errand": view}
	if order.ID != 0 {
		out["order"] = tradeOrderViewOf(order)
	}
	success(c, http.StatusOK, out)
}
func errandInput(req errandRequest) erraddomain.TaskInput {
	provided := strings.TrimSpace(req.ContactType) != "" || strings.TrimSpace(req.Contact) != ""
	return erraddomain.TaskInput{Title: req.Title, Description: req.Description, RewardCents: req.RewardCents, PickupLocation: req.PickupLocation, DropoffLocation: req.DropoffLocation, Deadline: req.Deadline, Contact: erraddomain.ContactInput{Type: req.ContactType, Value: req.Contact, Provided: provided}}
}

type errandView struct {
	ID              uint64     `json:"id"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	RewardCents     int64      `json:"reward_cents"`
	Currency        string     `json:"currency"`
	PickupLocation  string     `json:"pickup_location"`
	DropoffLocation string     `json:"dropoff_location"`
	Deadline        time.Time  `json:"deadline"`
	Status          string     `json:"status"`
	RequesterID     uint64     `json:"requester_id"`
	ContactType     string     `json:"contact_type"`
	Contact         string     `json:"contact"`
	RunnerID        *uint64    `json:"runner_id"`
	TradeOrderID    *uint64    `json:"trade_order_id"`
	AcceptedAt      *time.Time `json:"accepted_at"`
	PickedUpAt      *time.Time `json:"picked_up_at"`
	DeliveredAt     *time.Time `json:"delivered_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	CancelledAt     *time.Time `json:"cancelled_at"`
	Version         uint64     `json:"version"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func errandViewOf(task *erraddomain.Task, contact erraddomain.ContactDetails) errandView {
	return errandView{ID: task.ID, Title: task.Title, Description: task.Description, RewardCents: task.RewardCents, Currency: task.Currency, PickupLocation: task.PickupLocation, DropoffLocation: task.DropoffLocation, Deadline: task.Deadline, Status: task.Status, RequesterID: task.RequesterId, ContactType: contact.Type, Contact: contact.Value, RunnerID: task.RunnerId, TradeOrderID: task.TradeOrderId, AcceptedAt: task.AcceptedAt, PickedUpAt: task.PickedUpAt, DeliveredAt: task.DeliveredAt, CompletedAt: task.CompletedAt, CancelledAt: task.CancelledAt, Version: task.Version, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt}
}
func (h *Handler) errandView(c *gin.Context, task *erraddomain.Task) (errandView, error) {
	contact, err := h.errands.Contact(c.Request.Context(), task, c.GetUint64(userIDKey))
	if err != nil {
		return errandView{}, err
	}
	return errandViewOf(task, contact), nil
}
func (h *Handler) errandViews(c *gin.Context, tasks []erraddomain.Task) ([]errandView, error) {
	out := make([]errandView, len(tasks))
	for i := range tasks {
		view, err := h.errandView(c, &tasks[i])
		if err != nil {
			return nil, err
		}
		out[i] = view
	}
	return out, nil
}
func (h *Handler) successErrand(c *gin.Context, status int, task *erraddomain.Task) {
	view, err := h.errandView(c, task)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, status, view)
}
