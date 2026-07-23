package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/api/generated"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	commentapp "github.com/weouc-plus/campus-platform/internal/modules/comment/application"
	commentdomain "github.com/weouc-plus/campus-platform/internal/modules/comment/domain"
)

func (h *Handler) listComments(c *gin.Context) {
	params, ok := generatedParams[generated.ListCommentsParams](c, "ListComments")
	if !ok {
		failure(c, apperror.New(http.StatusBadRequest, "invalid_parameter", "缺少已校验的评论列表参数"))
		return
	}
	page, size := paging(c)
	items, total, err := h.comments.ListRoots(
		c.Request.Context(),
		string(params.TargetType),
		params.TargetID,
		c.GetUint64(userIDKey),
		page,
		size,
	)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.commentViews(c, items, false)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, generated.CommentPage{
		Items: views, Page: page, PageSize: size, Total: total,
	})
}

func (h *Handler) listMyComments(c *gin.Context) {
	params, ok := generatedParams[generated.ListMyCommentsParams](c, "ListMyComments")
	if !ok {
		failure(c, apperror.New(http.StatusBadRequest, "invalid_parameter", "缺少已校验的我的评论参数"))
		return
	}
	page, size := paging(c)
	items, total, err := h.comments.ListMine(
		c.Request.Context(),
		c.GetUint64(userIDKey),
		commentStringValue(params.Status),
		page,
		size,
	)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.commentViews(c, items, false)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, generated.CommentPage{
		Items: views, Page: page, PageSize: size, Total: total,
	})
}

func (h *Handler) listAdminComments(c *gin.Context) {
	params, ok := generatedParams[generated.ListAdminCommentsParams](c, "ListAdminComments")
	if !ok {
		failure(c, apperror.New(http.StatusBadRequest, "invalid_parameter", "缺少已校验的评论审核参数"))
		return
	}
	page, size := paging(c)
	items, total, err := h.comments.ListAdmin(c.Request.Context(), commentapp.Search{
		TargetType: commentStringValue(params.TargetType),
		TargetID:   commentUint64Value(params.TargetID),
		AuthorID:   commentUint64Value(params.AuthorID),
		Status:     commentStringValue(params.Status),
		Keyword:    commentStringValue(params.Keyword),
	}, page, size)
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.commentViews(c, items, true)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, generated.CommentPage{
		Items: views, Page: page, PageSize: size, Total: total,
	})
}

func (h *Handler) getCommentThread(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	thread, err := h.comments.Thread(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		false,
	)
	if err != nil {
		failure(c, err)
		return
	}
	root, err := h.commentView(c, &thread.Root, false)
	if err != nil {
		failure(c, err)
		return
	}
	descendants, err := h.commentViews(c, thread.Descendants, false)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, generated.CommentThread{
		Root: root, Descendants: descendants,
	})
}

func (h *Handler) createComment(c *gin.Context) {
	var request generated.CreateCommentJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.comments.Create(c.Request.Context(), c.GetUint64(userIDKey), commentapp.CreateInput{
		TargetType: request.TargetType,
		TargetID:   request.TargetID,
		ParentID:   request.ParentID,
		Content:    request.Content,
	})
	h.successComment(c, http.StatusCreated, item, err, false)
}

func (h *Handler) updateComment(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.UpdateCommentJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.comments.Update(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		request.ExpectedVersion,
		request.Content,
	)
	h.successComment(c, http.StatusOK, item, err, false)
}

func (h *Handler) withdrawComment(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.WithdrawCommentJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.comments.Withdraw(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		request.ExpectedVersion,
	)
	h.successComment(c, http.StatusOK, item, err, false)
}

func (h *Handler) submitCommentReview(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.SubmitCommentReviewJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.comments.SubmitReview(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		request.ExpectedVersion,
	)
	h.successComment(c, http.StatusOK, item, err, false)
}

func (h *Handler) reviewComment(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.ReviewCommentJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.comments.Review(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		request.ExpectedVersion,
		request.Approved,
		commentStringValue(request.Reason),
	)
	h.successComment(c, http.StatusOK, item, err, true)
}

func (h *Handler) revokeCommentReview(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.RevokeCommentReviewJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.comments.RevokeReview(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		request.ExpectedVersion,
		request.Reason,
	)
	h.successComment(c, http.StatusOK, item, err, true)
}

func (h *Handler) pinComment(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.PinCommentJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.comments.Pin(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		request.ExpectedVersion,
	)
	h.successComment(c, http.StatusOK, item, err, false)
}

func (h *Handler) unpinComment(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	params, ok := generatedParams[generated.UnpinCommentParams](c, "UnpinComment")
	if !ok {
		failure(c, apperror.New(http.StatusBadRequest, "invalid_parameter", "缺少已校验的取消置顶参数"))
		return
	}
	item, err := h.comments.Unpin(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		params.ExpectedVersion,
	)
	h.successComment(c, http.StatusOK, item, err, false)
}

func (h *Handler) successComment(
	c *gin.Context,
	status int,
	item commentapp.Item,
	err error,
	admin bool,
) {
	if err != nil {
		failure(c, err)
		return
	}
	view, err := h.commentView(c, &item, admin)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, status, view)
}

func (h *Handler) commentViews(
	c *gin.Context,
	items []commentapp.Item,
	admin bool,
) ([]generated.CommentView, error) {
	views := make([]generated.CommentView, len(items))
	for index := range items {
		view, err := h.commentView(c, &items[index], admin)
		if err != nil {
			return nil, err
		}
		views[index] = view
	}
	return views, nil
}

func (h *Handler) commentView(
	c *gin.Context,
	item *commentapp.Item,
	admin bool,
) (generated.CommentView, error) {
	relation, actions, err := h.comments.ViewerContext(
		c.Request.Context(),
		item,
		c.GetUint64(userIDKey),
		admin,
	)
	if err != nil {
		return generated.CommentView{}, err
	}
	actions, err = h.commentActionsForViewer(c, actions)
	if err != nil {
		return generated.CommentView{}, err
	}
	comment := &item.Comment
	rootID := comment.ID
	if comment.RootId != nil {
		rootID = *comment.RootId
	}
	return generated.CommentView{
		ID:               comment.ID,
		TargetType:       generated.CommentTargetType(comment.TargetType),
		TargetID:         comment.TargetId,
		AuthorID:         comment.AuthorId,
		ParentID:         comment.ParentId,
		RootID:           rootID,
		Depth:            comment.Depth,
		ReplyToUserID:    comment.ReplyToUserId,
		Content:          comment.Content,
		Status:           generated.CommentViewStatus(comment.Status),
		ReviewReason:     comment.ReviewReason,
		ReviewedBy:       comment.ReviewedBy,
		ReviewedAt:       comment.ReviewedAt,
		Version:          comment.Version,
		CreatedAt:        comment.CreatedAt,
		UpdatedAt:        comment.UpdatedAt,
		ViewerRelation:   relation,
		AvailableActions: generatedActions[generated.CommentViewerAction](actions),
		Pinned:           item.Pinned,
		ReplyCount:       item.ReplyCount,
	}, nil
}

func (h *Handler) commentActionsForViewer(c *gin.Context, actions []string) ([]string, error) {
	if c.GetUint64(userIDKey) == 0 || !containsAction(actions, commentdomain.ActionReply) {
		return actions, nil
	}
	verified, err := h.viewerAcademicVerified(c)
	if err != nil {
		return nil, err
	}
	if verified {
		return actions, nil
	}
	result := make([]string, 0, len(actions))
	for _, action := range actions {
		if action != commentdomain.ActionReply {
			result = append(result, action)
		}
	}
	return append(result, actionVerifyAcademic), nil
}

func commentStringValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func commentUint64Value(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}
