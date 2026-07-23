package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/api/generated"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	campuscircleapp "github.com/weouc-plus/campus-platform/internal/modules/campus_circle/application"
	campuscircledomain "github.com/weouc-plus/campus-platform/internal/modules/campus_circle/domain"
)

func (h *Handler) listCampusCircleSections(c *gin.Context) {
	h.listCampusCircleSectionTree(c, false)
}

func (h *Handler) listAdminCampusCircleSections(c *gin.Context) {
	h.listCampusCircleSectionTree(c, true)
}

func (h *Handler) listCampusCircleSectionTree(c *gin.Context, admin bool) {
	nodes, err := h.campusCircle.ListSections(c.Request.Context(), admin)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, generated.CampusCircleSectionTree{
		Items: campusCircleSectionViews(nodes),
	})
}

func (h *Handler) createCampusCircleSection(c *gin.Context) {
	var request generated.CreateCampusCircleSectionJSONBody
	if !bind(c, &request) {
		return
	}
	section, err := h.campusCircle.CreateSection(
		c.Request.Context(),
		c.GetUint64(userIDKey),
		campuscircledomain.SectionInput{
			ParentID:    request.ParentID,
			Slug:        request.Slug,
			Name:        request.Name,
			Description: campusCircleString(request.Description),
			IconURL:     campusCircleString(request.IconURL),
			CoverURL:    campusCircleString(request.CoverURL),
			SortOrder:   request.SortOrder,
		},
	)
	h.successCampusCircleSection(c, http.StatusCreated, section, err)
}

func (h *Handler) updateCampusCircleSection(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.UpdateCampusCircleSectionJSONBody
	if !bind(c, &request) {
		return
	}
	section, err := h.campusCircle.UpdateSection(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		request.ExpectedVersion,
		campuscircledomain.SectionInput{
			Name:        request.Name,
			Description: campusCircleString(request.Description),
			IconURL:     campusCircleString(request.IconURL),
			CoverURL:    campusCircleString(request.CoverURL),
			SortOrder:   request.SortOrder,
		},
	)
	h.successCampusCircleSection(c, http.StatusOK, section, err)
}

func (h *Handler) archiveCampusCircleSection(c *gin.Context) {
	var request generated.ArchiveCampusCircleSectionJSONBody
	if !bind(c, &request) {
		return
	}
	h.changeCampusCircleSectionStatus(
		c,
		request.ExpectedVersion,
		h.campusCircle.ArchiveSection,
	)
}

func (h *Handler) activateCampusCircleSection(c *gin.Context) {
	var request generated.ActivateCampusCircleSectionJSONBody
	if !bind(c, &request) {
		return
	}
	h.changeCampusCircleSectionStatus(
		c,
		request.ExpectedVersion,
		h.campusCircle.ActivateSection,
	)
}

func (h *Handler) changeCampusCircleSectionStatus(
	c *gin.Context,
	expectedVersion uint64,
	change func(context.Context, uint64, uint64, uint64) (*campuscircledomain.CampusCircleSection, error),
) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	section, err := change(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		expectedVersion,
	)
	h.successCampusCircleSection(c, http.StatusOK, section, err)
}

func (h *Handler) listCampusCirclePosts(c *gin.Context) {
	params, ok := generatedParams[generated.ListCampusCirclePostsParams](c, "ListCampusCirclePosts")
	if !ok {
		failure(c, apperror.New(http.StatusBadRequest, "invalid_parameter", "缺少已校验的校园圈列表参数"))
		return
	}
	page, size := paging(c)
	items, total, err := h.campusCircle.ListPublicPosts(
		c.Request.Context(),
		campuscircleapp.PublicSearch{
			SectionID:       campusCircleUint64(params.SectionID),
			ParentSectionID: campusCircleUint64(params.ParentSectionID),
			Keyword:         campusCircleString(params.Keyword),
		},
		c.GetUint64(userIDKey),
		page,
		size,
	)
	h.successCampusCirclePostPage(c, items, total, page, size, err, false)
}

func (h *Handler) listMyCampusCirclePosts(c *gin.Context) {
	params, ok := generatedParams[generated.ListMyCampusCirclePostsParams](c, "ListMyCampusCirclePosts")
	if !ok {
		failure(c, apperror.New(http.StatusBadRequest, "invalid_parameter", "缺少已校验的我的校园圈参数"))
		return
	}
	page, size := paging(c)
	items, total, err := h.campusCircle.ListMyPosts(
		c.Request.Context(),
		c.GetUint64(userIDKey),
		campuscircleapp.MineSearch{
			SectionID: campusCircleUint64(params.SectionID),
			Status:    campusCircleString(params.Status),
			Keyword:   campusCircleString(params.Keyword),
		},
		page,
		size,
	)
	h.successCampusCirclePostPage(c, items, total, page, size, err, false)
}

func (h *Handler) listAdminCampusCirclePosts(c *gin.Context) {
	params, ok := generatedParams[generated.ListAdminCampusCirclePostsParams](c, "ListAdminCampusCirclePosts")
	if !ok {
		failure(c, apperror.New(http.StatusBadRequest, "invalid_parameter", "缺少已校验的校园圈审核参数"))
		return
	}
	page, size := paging(c)
	items, total, err := h.campusCircle.ListAdminPosts(
		c.Request.Context(),
		campuscircleapp.AdminSearch{
			SectionID:   campusCircleUint64(params.SectionID),
			AuthorID:    campusCircleUint64(params.AuthorID),
			Status:      campusCircleString(params.Status),
			Keyword:     campusCircleString(params.Keyword),
			CreatedFrom: params.CreatedFrom,
			CreatedTo:   params.CreatedTo,
		},
		page,
		size,
	)
	h.successCampusCirclePostPage(c, items, total, page, size, err, true)
}

func (h *Handler) createCampusCirclePost(c *gin.Context) {
	var request generated.CreateCampusCirclePostJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.campusCircle.CreatePost(
		c.Request.Context(),
		c.GetUint64(userIDKey),
		campuscircledomain.PostInput{
			SectionID: request.SectionID,
			Title:     campusCircleString(request.Title),
			Content:   campusCircleString(request.Content),
			ImageURLs: campusCircleStrings(request.ImageUrls),
		},
	)
	h.successCampusCirclePost(c, http.StatusCreated, item, err, false)
}

func (h *Handler) updateCampusCirclePost(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.UpdateCampusCirclePostJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.campusCircle.UpdatePost(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		request.ExpectedVersion,
		campuscircledomain.PostInput{
			SectionID: request.SectionID,
			Title:     campusCircleString(request.Title),
			Content:   campusCircleString(request.Content),
			ImageURLs: campusCircleStrings(request.ImageUrls),
		},
	)
	h.successCampusCirclePost(c, http.StatusOK, item, err, false)
}

func (h *Handler) getCampusCirclePost(c *gin.Context) {
	h.getCampusCirclePostForViewer(c, false)
}

func (h *Handler) getAdminCampusCirclePost(c *gin.Context) {
	h.getCampusCirclePostForViewer(c, true)
}

func (h *Handler) getCampusCirclePostForViewer(c *gin.Context, admin bool) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	item, err := h.campusCircle.GetPost(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		admin,
	)
	h.successCampusCirclePost(c, http.StatusOK, item, err, admin)
}

func (h *Handler) submitCampusCirclePostReview(c *gin.Context) {
	var request generated.SubmitCampusCirclePostReviewJSONBody
	if !bind(c, &request) {
		return
	}
	h.changeCampusCirclePost(
		c,
		request.ExpectedVersion,
		h.campusCircle.SubmitPostReview,
		false,
	)
}

func (h *Handler) withdrawCampusCirclePost(c *gin.Context) {
	var request generated.WithdrawCampusCirclePostJSONBody
	if !bind(c, &request) {
		return
	}
	h.changeCampusCirclePost(
		c,
		request.ExpectedVersion,
		h.campusCircle.WithdrawPost,
		false,
	)
}

func (h *Handler) changeCampusCirclePost(
	c *gin.Context,
	expectedVersion uint64,
	change func(context.Context, uint64, uint64, uint64) (campuscircleapp.Item, error),
	admin bool,
) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	item, err := change(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		expectedVersion,
	)
	h.successCampusCirclePost(c, http.StatusOK, item, err, admin)
}

func (h *Handler) reviewCampusCirclePost(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.ReviewCampusCirclePostJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.campusCircle.ReviewPost(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		request.ExpectedVersion,
		request.Approved,
		campusCircleString(request.Reason),
	)
	h.successCampusCirclePost(c, http.StatusOK, item, err, true)
}

func (h *Handler) revokeCampusCirclePostReview(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var request generated.RevokeCampusCirclePostReviewJSONBody
	if !bind(c, &request) {
		return
	}
	item, err := h.campusCircle.RevokePostReview(
		c.Request.Context(),
		id,
		c.GetUint64(userIDKey),
		request.ExpectedVersion,
		request.Reason,
	)
	h.successCampusCirclePost(c, http.StatusOK, item, err, true)
}

func (h *Handler) likeCampusCirclePost(c *gin.Context) {
	h.changeCampusCirclePostLike(c, h.campusCircle.LikePost)
}

func (h *Handler) unlikeCampusCirclePost(c *gin.Context) {
	h.changeCampusCirclePostLike(c, h.campusCircle.UnlikePost)
}

func (h *Handler) changeCampusCirclePostLike(
	c *gin.Context,
	change func(context.Context, uint64, uint64) (campuscircleapp.Item, error),
) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	item, err := change(c.Request.Context(), id, c.GetUint64(userIDKey))
	h.successCampusCirclePost(c, http.StatusOK, item, err, false)
}

func (h *Handler) successCampusCircleSection(
	c *gin.Context,
	status int,
	section *campuscircledomain.CampusCircleSection,
	err error,
) {
	if err != nil {
		failure(c, err)
		return
	}
	success(c, status, campusCircleSectionView(section, []generated.CampusCircleSectionView{}))
}

func (h *Handler) successCampusCirclePostPage(
	c *gin.Context,
	items []campuscircleapp.Item,
	total int64,
	page int,
	size int,
	err error,
	admin bool,
) {
	if err != nil {
		failure(c, err)
		return
	}
	views, err := h.campusCirclePostViews(c, items, admin)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, http.StatusOK, generated.CampusCirclePostPage{
		Items: views, Page: page, PageSize: size, Total: total,
	})
}

func (h *Handler) successCampusCirclePost(
	c *gin.Context,
	status int,
	item campuscircleapp.Item,
	err error,
	admin bool,
) {
	if err != nil {
		failure(c, err)
		return
	}
	view, err := h.campusCirclePostView(c, item, admin)
	if err != nil {
		failure(c, err)
		return
	}
	success(c, status, view)
}

func (h *Handler) campusCirclePostViews(
	c *gin.Context,
	items []campuscircleapp.Item,
	admin bool,
) ([]generated.CampusCirclePostView, error) {
	views := make([]generated.CampusCirclePostView, len(items))
	for index := range items {
		view, err := h.campusCirclePostView(c, items[index], admin)
		if err != nil {
			return nil, err
		}
		views[index] = view
	}
	return views, nil
}

func (h *Handler) campusCirclePostView(
	c *gin.Context,
	item campuscircleapp.Item,
	admin bool,
) (generated.CampusCirclePostView, error) {
	relation, actions := h.campusCircle.ViewerContext(
		item,
		c.GetUint64(userIDKey),
		admin,
	)
	actions, err := h.campusCircleActionsForViewer(c, actions)
	if err != nil {
		return generated.CampusCirclePostView{}, err
	}
	images := make([]generated.CampusCirclePostImageView, len(item.Images))
	for index := range item.Images {
		images[index] = generated.CampusCirclePostImageView{
			ID:        item.Images[index].ID,
			URL:       item.Images[index].Url,
			SortOrder: item.Images[index].SortOrder,
		}
	}
	post := &item.Post
	return generated.CampusCirclePostView{
		ID:               post.ID,
		SectionID:        post.SectionId,
		AuthorID:         post.AuthorId,
		Title:            post.Title,
		Content:          post.Content,
		Images:           images,
		Status:           generated.CampusCirclePostStatus(post.Status),
		ReviewReason:     post.ReviewReason,
		ReviewedBy:       post.ReviewedBy,
		ReviewedAt:       post.ReviewedAt,
		PublishedAt:      post.PublishedAt,
		Version:          post.Version,
		CreatedAt:        post.CreatedAt,
		UpdatedAt:        post.UpdatedAt,
		LikeCount:        item.LikeCount,
		CommentCount:     item.CommentCount,
		Liked:            item.Liked,
		ViewerRelation:   generated.CampusCircleViewerRelation(relation),
		AvailableActions: generatedActions[generated.CampusCircleViewerAction](actions),
	}, nil
}

func (h *Handler) campusCircleActionsForViewer(
	c *gin.Context,
	actions []string,
) ([]string, error) {
	return h.availableActionsForViewer(
		c,
		actions,
		campuscircledomain.ActionLike,
		campuscircledomain.ActionUnlike,
		campuscircledomain.ActionComment,
	)
}

func campusCircleSectionViews(
	nodes []campuscircleapp.SectionNode,
) []generated.CampusCircleSectionView {
	views := make([]generated.CampusCircleSectionView, len(nodes))
	for index := range nodes {
		views[index] = campusCircleSectionView(
			&nodes[index].Section,
			campusCircleSectionViews(nodes[index].Children),
		)
	}
	return views
}

func campusCircleSectionView(
	section *campuscircledomain.CampusCircleSection,
	children []generated.CampusCircleSectionView,
) generated.CampusCircleSectionView {
	if children == nil {
		children = []generated.CampusCircleSectionView{}
	}
	return generated.CampusCircleSectionView{
		ID:          section.ID,
		ParentID:    section.ParentId,
		Slug:        section.Slug,
		Name:        section.Name,
		Description: section.Description,
		IconURL:     section.IconUrl,
		CoverURL:    section.CoverUrl,
		SortOrder:   section.SortOrder,
		Status:      generated.CampusCircleSectionStatus(section.Status),
		Version:     section.Version,
		CreatedAt:   section.CreatedAt,
		UpdatedAt:   section.UpdatedAt,
		Children:    children,
	}
}

func campusCircleString[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func campusCircleUint64(value *uint64) uint64 {
	if value == nil {
		return 0
	}
	return *value
}

func campusCircleStrings(value *[]string) []string {
	if value == nil {
		return []string{}
	}
	return append([]string{}, (*value)...)
}
