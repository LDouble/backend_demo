package httpapi

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/api/generated"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	noticeapp "github.com/weouc-plus/campus-platform/internal/modules/notice/application"
	noticedomain "github.com/weouc-plus/campus-platform/internal/modules/notice/domain"
)

func (h *Handler) createNotice(c *gin.Context) {
	var req generated.CreateNoticeRequest
	if !bind(c, &req) {
		return
	}
	notice, err := h.notices.Create(c.Request.Context(), c.GetUint64(userIDKey), createNoticeInput(req))
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 201, noticeViewOf(notice))
}

func (h *Handler) updateNotice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req generated.UpdateNoticeRequest
	if !bind(c, &req) {
		return
	}
	input := noticedomain.DraftInput{Title: req.Title, Summary: req.Summary, Body: req.Body, Category: req.Category, Priority: string(req.Priority), ActionPath: stringValue(req.ActionPath), Audience: noticedomain.Audience{All: req.Audience.All, Roles: req.Audience.Roles, UserIDs: req.Audience.UserIds}}
	for _, channel := range req.Channels {
		input.Channels = append(input.Channels, string(channel))
	}
	notice, err := h.notices.Update(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion, input)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, noticeViewOf(notice))
}

func (h *Handler) listAdminNotices(c *gin.Context) {
	page, size := paging(c)
	rows, total, err := h.notices.ListAdmin(c.Request.Context(), page, size)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, pageData(noticeViews(rows), page, size, total))
}

func (h *Handler) getAdminNotice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	notice, audience, err := h.notices.GetAdmin(c.Request.Context(), id)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"notice": noticeViewOf(notice), "audience": audienceViewOf(audience)})
}

func (h *Handler) deleteNotice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	params, ok := generatedParams[generated.DeleteNoticeParams](c, "DeleteNotice")
	if !ok {
		failure(c, apperror.New(400, "invalid_parameter", "缺少已校验的通知删除参数"))
		return
	}
	if err := h.notices.Delete(c.Request.Context(), id, params.ExpectedVersion); err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"deleted": true})
}

func (h *Handler) publishNotice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req generated.PublishNoticeRequest
	if !bind(c, &req) {
		return
	}
	notice, err := h.notices.Publish(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion, req.PublishAt)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, noticeViewOf(notice))
}

func (h *Handler) revokeNotice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var req generated.VersionRequest
	if !bind(c, &req) {
		return
	}
	notice, err := h.notices.Revoke(c.Request.Context(), id, c.GetUint64(userIDKey), req.ExpectedVersion)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, noticeViewOf(notice))
}

func (h *Handler) listNoticeDeliveries(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	page, size := paging(c)
	rows, total, err := h.notices.ListDeliveries(c.Request.Context(), id, page, size)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, pageData(deliveryViews(rows), page, size, total))
}

func (h *Handler) retryNoticeDeliveries(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	count, err := h.notices.RetryDeliveries(c.Request.Context(), id)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"retried": count})
}

func (h *Handler) listMyNotices(c *gin.Context) {
	page, size := paging(c)
	unread, err := strconv.ParseBool(c.DefaultQuery("unread", "false"))
	if err != nil {
		failure(c, apperror.New(400, "invalid_request", "unread 参数无效"))
		return
	}
	rows, total, err := h.notices.ListInbox(c.Request.Context(), c.GetUint64(userIDKey), noticeapp.InboxFilter{Unread: unread, Category: strings.TrimSpace(c.Query("category")), Page: page, PageSize: size})
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, pageData(noticeViews(rows), page, size, total))
}

func (h *Handler) getUnreadNoticeCount(c *gin.Context) {
	count, err := h.notices.UnreadCount(c.Request.Context(), c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"count": count})
}

func (h *Handler) getMyNotice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	notice, err := h.notices.GetInbox(c.Request.Context(), c.GetUint64(userIDKey), id)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, noticeViewOf(notice))
}

func (h *Handler) readNotice(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.notices.MarkRead(c.Request.Context(), c.GetUint64(userIDKey), id); err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"read": true})
}

func (h *Handler) readAllNotices(c *gin.Context) {
	count, err := h.notices.MarkAllRead(c.Request.Context(), c.GetUint64(userIDKey))
	if err != nil {
		failure(c, err)
		return
	}
	success(c, 200, gin.H{"updated": count})
}

func createNoticeInput(req generated.CreateNoticeRequest) noticedomain.DraftInput {
	input := noticedomain.DraftInput{Title: req.Title, Summary: req.Summary, Body: req.Body, Category: req.Category, Priority: string(req.Priority), ActionPath: stringValue(req.ActionPath), Audience: noticedomain.Audience{All: req.Audience.All, Roles: req.Audience.Roles, UserIDs: req.Audience.UserIds}}
	for _, channel := range req.Channels {
		input.Channels = append(input.Channels, string(channel))
	}
	return input
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type noticeView struct {
	ID          uint64     `json:"id"`
	Title       string     `json:"title"`
	Summary     string     `json:"summary"`
	Body        string     `json:"body"`
	Category    string     `json:"category"`
	Priority    string     `json:"priority"`
	Status      string     `json:"status"`
	ActionPath  string     `json:"action_path"`
	Channels    []string   `json:"channels"`
	PublishAt   *time.Time `json:"publish_at"`
	PublishedAt *time.Time `json:"published_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	Version     uint64     `json:"version"`
	CreatedBy   uint64     `json:"created_by"`
	UpdatedBy   uint64     `json:"updated_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func noticeViewOf(notice *noticedomain.Notice) noticeView {
	channels := []string{noticedomain.ChannelInApp}
	if notice.PushEnabled {
		channels = append(channels, noticedomain.ChannelPush)
	}
	return noticeView{ID: notice.ID, Title: notice.Title, Summary: notice.Summary, Body: notice.Body, Category: notice.Category, Priority: notice.Priority, Status: notice.Status, ActionPath: stringValue(notice.ActionPath), Channels: channels, PublishAt: notice.PublishAt, PublishedAt: notice.PublishedAt, RevokedAt: notice.RevokedAt, Version: notice.Version, CreatedBy: notice.CreatedBy, UpdatedBy: notice.UpdatedBy, CreatedAt: notice.CreatedAt, UpdatedAt: notice.UpdatedAt}
}

func noticeViews(rows []noticedomain.Notice) []noticeView {
	views := make([]noticeView, len(rows))
	for i := range rows {
		views[i] = noticeViewOf(&rows[i])
	}
	return views
}

func audienceViewOf(rows []noticedomain.NoticeAudience) noticedomain.Audience {
	view := noticedomain.Audience{}
	for _, row := range rows {
		switch row.AudienceType {
		case noticedomain.AudienceAll:
			view.All = true
		case noticedomain.AudienceRole:
			view.Roles = append(view.Roles, row.AudienceValue)
		case noticedomain.AudienceUser:
			if id, err := strconv.ParseUint(row.AudienceValue, 10, 64); err == nil {
				view.UserIDs = append(view.UserIDs, id)
			}
		}
	}
	return view
}

type deliveryView struct {
	ID                uint64    `json:"id"`
	NoticeID          uint64    `json:"notice_id"`
	UserID            uint64    `json:"user_id"`
	Channel           string    `json:"channel"`
	Status            string    `json:"status"`
	Attempts          int64     `json:"attempts"`
	ProviderMessageID string    `json:"provider_message_id,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func deliveryViews(rows []noticedomain.NoticeDelivery) []deliveryView {
	views := make([]deliveryView, len(rows))
	for i, row := range rows {
		views[i] = deliveryView{ID: row.ID, NoticeID: row.NoticeId, UserID: row.UserId, Channel: row.Channel, Status: row.Status, Attempts: row.Attempts, ProviderMessageID: stringValue(row.ProviderMessageId), LastError: stringValue(row.LastError), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	}
	return views
}
