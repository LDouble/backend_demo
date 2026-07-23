// Package application coordinates campus-circle use cases.
package application

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/campus_circle/domain"
)

// SectionNode is one node in the two-level section tree.
type SectionNode struct {
	Section  domain.CampusCircleSection
	Children []SectionNode
}

// Item adds images and viewer-derived interaction state to a post.
type Item struct {
	Post         domain.CampusCirclePost
	Images       []domain.CampusCirclePostImage
	LikeCount    int64
	CommentCount int64
	Liked        bool
}

// PublicSearch filters the public approved-post feed.
type PublicSearch struct {
	SectionID       uint64
	ParentSectionID uint64
	Keyword         string
}

// MineSearch filters posts owned by one author.
type MineSearch struct {
	SectionID uint64
	Status    string
	Keyword   string
}

// AdminSearch filters the moderation queue.
type AdminSearch struct {
	SectionID   uint64
	AuthorID    uint64
	Status      string
	Keyword     string
	CreatedFrom *time.Time
	CreatedTo   *time.Time
}

// Store is the transactional persistence boundary for campus-circle use cases.
type Store interface {
	ListSections(context.Context, bool) ([]domain.CampusCircleSection, error)
	CreateSection(context.Context, uint64, domain.SectionInput, time.Time) (*domain.CampusCircleSection, error)
	UpdateSection(context.Context, uint64, uint64, uint64, domain.SectionInput, time.Time) (*domain.CampusCircleSection, error)
	SetSectionStatus(context.Context, uint64, uint64, uint64, string, time.Time) (*domain.CampusCircleSection, error)

	CreatePost(context.Context, uint64, domain.PostInput, time.Time) (Item, error)
	UpdatePost(context.Context, uint64, uint64, uint64, domain.PostInput, time.Time) (Item, error)
	GetPost(context.Context, uint64, uint64, bool) (Item, error)
	ListPublicPosts(context.Context, PublicSearch, uint64, int, int) ([]Item, int64, error)
	ListMyPosts(context.Context, uint64, MineSearch, int, int) ([]Item, int64, error)
	ListAdminPosts(context.Context, AdminSearch, int, int) ([]Item, int64, error)
	SubmitPostReview(context.Context, uint64, uint64, uint64, time.Time) (Item, error)
	WithdrawPost(context.Context, uint64, uint64, uint64, time.Time) (Item, error)
	ReviewPost(context.Context, uint64, uint64, uint64, bool, string, time.Time) (Item, error)
	RevokePostReview(context.Context, uint64, uint64, uint64, string, time.Time) (Item, error)
	LikePost(context.Context, uint64, uint64, time.Time) (Item, error)
	UnlikePost(context.Context, uint64, uint64, time.Time) (Item, error)
}

// Manager validates inputs before delegating atomic work to the Store.
type Manager struct {
	store Store
	now   func() time.Time
}

// NewManager creates a campus-circle use-case manager.
func NewManager(store Store) *Manager {
	return &Manager{store: store, now: time.Now}
}

// ListSections returns a deterministic two-level tree. Public callers receive
// active nodes only; the store applies that visibility filter.
func (m *Manager) ListSections(ctx context.Context, admin bool) ([]SectionNode, error) {
	sections, err := m.store.ListSections(ctx, admin)
	if err != nil {
		return nil, err
	}
	return BuildSectionTree(sections), nil
}

// CreateSection validates and creates a root or child section.
func (m *Manager) CreateSection(
	ctx context.Context,
	actorID uint64,
	input domain.SectionInput,
) (*domain.CampusCircleSection, error) {
	input = domain.NormalizeSectionInput(input)
	if err := domain.ValidateSectionInput(input, true); err != nil {
		return nil, invalidSection(err)
	}
	return m.store.CreateSection(ctx, actorID, input, m.now().UTC())
}

// UpdateSection changes mutable section presentation and placement.
func (m *Manager) UpdateSection(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	input domain.SectionInput,
) (*domain.CampusCircleSection, error) {
	input = domain.NormalizeSectionInput(input)
	if id == 0 || actorID == 0 || version == 0 {
		return nil, invalidSection(nil)
	}
	if err := domain.ValidateSectionInput(input, false); err != nil {
		return nil, invalidSection(err)
	}
	return m.store.UpdateSection(ctx, id, actorID, version, input, m.now().UTC())
}

// ArchiveSection stops a section from accepting new posts.
func (m *Manager) ArchiveSection(
	ctx context.Context,
	id,
	actorID,
	version uint64,
) (*domain.CampusCircleSection, error) {
	return m.setSectionStatus(ctx, id, actorID, version, domain.SectionStatusArchived)
}

// ActivateSection enables a valid section.
func (m *Manager) ActivateSection(
	ctx context.Context,
	id,
	actorID,
	version uint64,
) (*domain.CampusCircleSection, error) {
	return m.setSectionStatus(ctx, id, actorID, version, domain.SectionStatusActive)
}

// CreatePost creates a pending post.
func (m *Manager) CreatePost(
	ctx context.Context,
	actorID uint64,
	input domain.PostInput,
) (Item, error) {
	input = domain.NormalizePostInput(input)
	if actorID == 0 {
		return Item{}, invalidPost(nil)
	}
	if err := domain.ValidatePostInput(input); err != nil {
		return Item{}, invalidPost(err)
	}
	return m.store.CreatePost(ctx, actorID, input, m.now().UTC())
}

// UpdatePost edits an owned post and returns it to moderation.
func (m *Manager) UpdatePost(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	input domain.PostInput,
) (Item, error) {
	input = domain.NormalizePostInput(input)
	if id == 0 || actorID == 0 || version == 0 {
		return Item{}, invalidPost(nil)
	}
	if err := domain.ValidatePostInput(input); err != nil {
		return Item{}, invalidPost(err)
	}
	return m.store.UpdatePost(ctx, id, actorID, version, input, m.now().UTC())
}

// GetPost returns a post using normal viewer or administrator visibility.
func (m *Manager) GetPost(ctx context.Context, id, viewerID uint64, admin bool) (Item, error) {
	if id == 0 {
		return Item{}, postNotFound()
	}
	return m.store.GetPost(ctx, id, viewerID, admin)
}

// ListPublicPosts returns approved posts using public filters.
func (m *Manager) ListPublicPosts(
	ctx context.Context,
	search PublicSearch,
	viewerID uint64,
	page,
	size int,
) ([]Item, int64, error) {
	search.Keyword = strings.TrimSpace(search.Keyword)
	page, size = NormalizePage(page, size)
	return m.store.ListPublicPosts(ctx, search, viewerID, page, size)
}

// ListMyPosts returns every lifecycle state owned by one author.
func (m *Manager) ListMyPosts(
	ctx context.Context,
	authorID uint64,
	search MineSearch,
	page,
	size int,
) ([]Item, int64, error) {
	search.Keyword = strings.TrimSpace(search.Keyword)
	page, size = NormalizePage(page, size)
	return m.store.ListMyPosts(ctx, authorID, search, page, size)
}

// ListAdminPosts returns moderation-visible posts.
func (m *Manager) ListAdminPosts(
	ctx context.Context,
	search AdminSearch,
	page,
	size int,
) ([]Item, int64, error) {
	search.Keyword = strings.TrimSpace(search.Keyword)
	page, size = NormalizePage(page, size)
	if search.CreatedFrom != nil && search.CreatedTo != nil &&
		search.CreatedFrom.After(*search.CreatedTo) {
		return nil, 0, apperror.New(http.StatusBadRequest, "invalid_campus_circle_time_range", "开始时间不能晚于结束时间")
	}
	return m.store.ListAdminPosts(ctx, search, page, size)
}

// SubmitPostReview resubmits a rejected post.
func (m *Manager) SubmitPostReview(ctx context.Context, id, actorID, version uint64) (Item, error) {
	if id == 0 || actorID == 0 || version == 0 {
		return Item{}, invalidPost(nil)
	}
	return m.store.SubmitPostReview(ctx, id, actorID, version, m.now().UTC())
}

// WithdrawPost hides an owned post.
func (m *Manager) WithdrawPost(ctx context.Context, id, actorID, version uint64) (Item, error) {
	if id == 0 || actorID == 0 || version == 0 {
		return Item{}, invalidPost(nil)
	}
	return m.store.WithdrawPost(ctx, id, actorID, version, m.now().UTC())
}

// ReviewPost records an administrator decision.
func (m *Manager) ReviewPost(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	approved bool,
	reason string,
) (Item, error) {
	reason = strings.TrimSpace(reason)
	if id == 0 || adminID == 0 || version == 0 {
		return Item{}, invalidReview(nil)
	}
	if !approved && reason == "" {
		return Item{}, apperror.New(http.StatusBadRequest, "rejection_reason_required", "驳回原因不能为空")
	}
	if len([]rune(reason)) > 500 {
		return Item{}, invalidReview(nil)
	}
	return m.store.ReviewPost(ctx, id, adminID, version, approved, reason, m.now().UTC())
}

// RevokePostReview returns an approved or rejected post to moderation.
func (m *Manager) RevokePostReview(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	reason string,
) (Item, error) {
	reason = strings.TrimSpace(reason)
	if id == 0 || adminID == 0 || version == 0 || reason == "" ||
		len([]rune(reason)) > 500 {
		return Item{}, apperror.New(http.StatusBadRequest, "revoke_reason_required", "撤销审核原因不能为空或过长")
	}
	return m.store.RevokePostReview(ctx, id, adminID, version, reason, m.now().UTC())
}

// LikePost inherently idempotently likes another user's approved post.
func (m *Manager) LikePost(ctx context.Context, id, actorID uint64) (Item, error) {
	if id == 0 || actorID == 0 {
		return Item{}, postNotFound()
	}
	return m.store.LikePost(ctx, id, actorID, m.now().UTC())
}

// UnlikePost inherently idempotently removes a like.
func (m *Manager) UnlikePost(ctx context.Context, id, actorID uint64) (Item, error) {
	if id == 0 || actorID == 0 {
		return Item{}, postNotFound()
	}
	return m.store.UnlikePost(ctx, id, actorID, m.now().UTC())
}

// ViewerContext derives transport-facing viewer state.
func (m *Manager) ViewerContext(item Item, viewerID uint64, admin bool) (string, []string) {
	return domain.ViewerRelation(&item.Post, viewerID, admin),
		domain.AvailableActions(&item.Post, viewerID, item.Liked, admin)
}

// BuildSectionTree builds roots and attaches direct children while ignoring
// malformed deeper/orphan rows that should never be produced by the Store.
func BuildSectionTree(sections []domain.CampusCircleSection) []SectionNode {
	roots := make([]SectionNode, 0)
	rootIndex := make(map[uint64]int)
	for _, section := range sections {
		if section.ParentId != nil {
			continue
		}
		rootIndex[section.ID] = len(roots)
		roots = append(roots, SectionNode{Section: section, Children: []SectionNode{}})
	}
	for _, section := range sections {
		if section.ParentId == nil {
			continue
		}
		index, ok := rootIndex[*section.ParentId]
		if !ok {
			continue
		}
		roots[index].Children = append(
			roots[index].Children,
			SectionNode{Section: section, Children: []SectionNode{}},
		)
	}
	return roots
}

// NormalizePage applies the shared pagination defaults.
func NormalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return page, size
}

func (m *Manager) setSectionStatus(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	status string,
) (*domain.CampusCircleSection, error) {
	if id == 0 || actorID == 0 || version == 0 {
		return nil, invalidSection(nil)
	}
	return m.store.SetSectionStatus(ctx, id, actorID, version, status, m.now().UTC())
}

func invalidSection(err error) error {
	return apperror.Wrap(http.StatusBadRequest, "invalid_campus_circle_section", "校园圈子模块参数无效", err)
}

func invalidPost(err error) error {
	return apperror.Wrap(http.StatusBadRequest, "invalid_campus_circle_post", "校园圈帖子参数无效", err)
}

func invalidReview(err error) error {
	return apperror.Wrap(http.StatusBadRequest, "invalid_campus_circle_review", "校园圈审核参数无效", err)
}

func postNotFound() error {
	return apperror.New(http.StatusNotFound, "campus_circle_post_not_found", "校园圈帖子不存在")
}
