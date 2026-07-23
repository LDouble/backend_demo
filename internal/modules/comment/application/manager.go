// Package application coordinates moderated comment use cases.
package application

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/comment/domain"
)

// Supported comment target types.
const (
	TargetActivity         = "activity"
	TargetMarketplace      = "marketplace"
	TargetErrand           = "errand"
	TargetCarpool          = "carpool"
	TargetCampusCirclePost = "campus_circle_post"
)

// Target is a commentable resource resolved by its owning module.
type Target struct {
	OwnerID uint64
}

// TargetResolver keeps the comment module independent from target modules.
// Resolve must apply the target module's normal viewer visibility rules.
type TargetResolver interface {
	Resolve(context.Context, string, uint64, uint64) (Target, error)
}

// CreateInput describes a root comment or nested reply.
type CreateInput struct {
	TargetType string
	TargetID   uint64
	ParentID   *uint64
	Content    string
}

// Search contains administrator comment filters.
type Search struct {
	TargetType string
	TargetID   uint64
	AuthorID   uint64
	Status     string
	Keyword    string
}

// Item adds query-derived display state to a persistent comment.
type Item struct {
	Comment       domain.Comment
	Pinned        bool
	ReplyCount    int64
	TargetOwnerID uint64
}

// Thread is a depth-first flat representation of one comment tree.
type Thread struct {
	Root        Item
	Descendants []Item
}

// Store defines transactional persistence for the comment aggregate.
type Store interface {
	Create(context.Context, uint64, CreateInput, time.Time) (Item, error)
	Get(context.Context, uint64, uint64, bool) (Item, error)
	ListRoots(context.Context, string, uint64, uint64, int, int) ([]Item, int64, error)
	ListMine(context.Context, uint64, string, int, int) ([]Item, int64, error)
	ListAdmin(context.Context, Search, int, int) ([]Item, int64, error)
	Thread(context.Context, uint64, uint64, bool) (Item, []Item, error)
	Update(context.Context, uint64, uint64, uint64, string, time.Time) (Item, error)
	Withdraw(context.Context, uint64, uint64, uint64, time.Time) (Item, error)
	SubmitReview(context.Context, uint64, uint64, uint64, time.Time) (Item, error)
	Review(context.Context, uint64, uint64, uint64, bool, string, time.Time) (Item, error)
	RevokeReview(context.Context, uint64, uint64, uint64, string, time.Time) (Item, error)
	Pin(context.Context, uint64, uint64, uint64, time.Time) (Item, error)
	Unpin(context.Context, uint64, uint64, uint64, time.Time) (Item, error)
}

// Manager validates cross-module and user-authored input before persistence.
type Manager struct {
	store   Store
	targets TargetResolver
	now     func() time.Time
}

// NewManager creates a comment use-case manager.
func NewManager(store Store, targets TargetResolver) *Manager {
	return &Manager{store: store, targets: targets, now: time.Now}
}

// Create creates a pending root comment or nested reply.
func (m *Manager) Create(ctx context.Context, actorID uint64, input CreateInput) (Item, error) {
	targetType, err := normalizeTargetType(input.TargetType)
	if err != nil {
		return Item{}, err
	}
	content, err := domain.NormalizeContent(input.Content)
	if err != nil {
		return Item{}, invalidComment(err)
	}
	if input.TargetID == 0 {
		return Item{}, apperror.New(http.StatusBadRequest, "invalid_comment_target", "评论目标不能为空")
	}
	target, err := m.targets.Resolve(ctx, targetType, input.TargetID, actorID)
	if err != nil {
		return Item{}, err
	}
	input.TargetType = targetType
	input.Content = content
	item, err := m.store.Create(ctx, actorID, input, m.now().UTC())
	item.TargetOwnerID = target.OwnerID
	return item, err
}

// ListRoots returns visible root comments for one visible target.
func (m *Manager) ListRoots(
	ctx context.Context,
	targetType string,
	targetID,
	viewerID uint64,
	page,
	size int,
) ([]Item, int64, error) {
	targetType, err := normalizeTargetType(targetType)
	if err != nil {
		return nil, 0, err
	}
	target, err := m.targets.Resolve(ctx, targetType, targetID, viewerID)
	if err != nil {
		return nil, 0, err
	}
	page, size = normalizePage(page, size)
	items, total, err := m.store.ListRoots(ctx, targetType, targetID, viewerID, page, size)
	setTargetOwner(items, target.OwnerID)
	return items, total, err
}

// ListMine returns all comments authored by one user.
func (m *Manager) ListMine(
	ctx context.Context,
	authorID uint64,
	status string,
	page,
	size int,
) ([]Item, int64, error) {
	if status != "" && !validStatus(status) {
		return nil, 0, apperror.New(http.StatusBadRequest, "invalid_comment_status", "评论状态无效")
	}
	page, size = normalizePage(page, size)
	return m.store.ListMine(ctx, authorID, status, page, size)
}

// ListAdmin returns comments matching moderation filters.
func (m *Manager) ListAdmin(ctx context.Context, search Search, page, size int) ([]Item, int64, error) {
	if search.TargetType != "" {
		targetType, err := normalizeTargetType(search.TargetType)
		if err != nil {
			return nil, 0, err
		}
		search.TargetType = targetType
	}
	if search.Status != "" && !validStatus(search.Status) {
		return nil, 0, apperror.New(http.StatusBadRequest, "invalid_comment_status", "评论状态无效")
	}
	search.Keyword = strings.TrimSpace(search.Keyword)
	page, size = normalizePage(page, size)
	return m.store.ListAdmin(ctx, search, page, size)
}

// Thread returns a visible root and its visible descendants in depth-first order.
func (m *Manager) Thread(ctx context.Context, id, viewerID uint64, admin bool) (Thread, error) {
	root, descendants, err := m.store.Thread(ctx, id, viewerID, admin)
	if err != nil {
		return Thread{}, err
	}
	ordered := depthFirst(root.Comment.ID, descendants)
	if admin {
		return Thread{Root: root, Descendants: ordered}, nil
	}
	target, err := m.targets.Resolve(
		ctx,
		root.Comment.TargetType,
		root.Comment.TargetId,
		viewerID,
	)
	if err != nil {
		return Thread{}, err
	}
	root.TargetOwnerID = target.OwnerID
	setTargetOwner(ordered, target.OwnerID)
	return Thread{Root: root, Descendants: ordered}, nil
}

// Update edits an owned comment and returns it to moderation.
func (m *Manager) Update(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	content string,
) (Item, error) {
	content, err := domain.NormalizeContent(content)
	if err != nil {
		return Item{}, invalidComment(err)
	}
	return m.store.Update(ctx, id, actorID, version, content, m.now().UTC())
}

// Withdraw hides an owned comment.
func (m *Manager) Withdraw(ctx context.Context, id, actorID, version uint64) (Item, error) {
	return m.store.Withdraw(ctx, id, actorID, version, m.now().UTC())
}

// SubmitReview resubmits a rejected comment for moderation.
func (m *Manager) SubmitReview(ctx context.Context, id, actorID, version uint64) (Item, error) {
	return m.store.SubmitReview(ctx, id, actorID, version, m.now().UTC())
}

// Review records an administrator moderation decision.
func (m *Manager) Review(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	approved bool,
	reason string,
) (Item, error) {
	reason = strings.TrimSpace(reason)
	if !approved && reason == "" {
		return Item{}, apperror.New(http.StatusBadRequest, "comment_review_reason_required", "驳回评论时必须填写原因")
	}
	return m.store.Review(ctx, id, adminID, version, approved, reason, m.now().UTC())
}

// RevokeReview returns a decided comment to pending moderation.
func (m *Manager) RevokeReview(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	reason string,
) (Item, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Item{}, apperror.New(http.StatusBadRequest, "comment_review_reason_required", "撤销审核时必须填写原因")
	}
	return m.store.RevokeReview(ctx, id, adminID, version, reason, m.now().UTC())
}

// Pin atomically replaces the target's pinned root comment.
func (m *Manager) Pin(ctx context.Context, id, actorID, version uint64) (Item, error) {
	item, err := m.store.Get(ctx, id, actorID, false)
	if err != nil {
		return Item{}, err
	}
	target, err := m.targets.Resolve(
		ctx,
		item.Comment.TargetType,
		item.Comment.TargetId,
		actorID,
	)
	if err != nil {
		return Item{}, err
	}
	if target.OwnerID != actorID {
		return Item{}, apperror.New(http.StatusForbidden, "not_comment_target_owner", "仅资源发布者可以置顶评论")
	}
	return m.store.Pin(ctx, id, actorID, version, m.now().UTC())
}

// Unpin removes a target's pinned root comment.
func (m *Manager) Unpin(ctx context.Context, id, actorID, version uint64) (Item, error) {
	item, err := m.store.Get(ctx, id, actorID, false)
	if err != nil {
		return Item{}, err
	}
	target, err := m.targets.Resolve(
		ctx,
		item.Comment.TargetType,
		item.Comment.TargetId,
		actorID,
	)
	if err != nil {
		return Item{}, err
	}
	if target.OwnerID != actorID {
		return Item{}, apperror.New(http.StatusForbidden, "not_comment_target_owner", "仅资源发布者可以取消置顶评论")
	}
	return m.store.Unpin(ctx, id, actorID, version, m.now().UTC())
}

// ViewerContext derives the relation and currently available member actions.
func (m *Manager) ViewerContext(
	ctx context.Context,
	item *Item,
	viewerID uint64,
	admin bool,
) (string, []string, error) {
	if item == nil {
		return "guest", []string{}, nil
	}
	if admin {
		return "admin", []string{}, nil
	}
	relation := "member"
	switch {
	case viewerID == 0:
		relation = "guest"
	case item.Comment.AuthorId == viewerID:
		relation = "author"
	}
	targetOwnerID := item.TargetOwnerID
	mayPin := viewerID != 0 &&
		item.Comment.ParentId == nil &&
		item.Comment.Status == domain.StatusApproved
	if targetOwnerID == 0 && mayPin {
		target, err := m.targets.Resolve(
			ctx,
			item.Comment.TargetType,
			item.Comment.TargetId,
			viewerID,
		)
		if err != nil {
			if relation == "author" {
				actions := domain.AvailableActions(
					&item.Comment,
					viewerID,
					false,
					item.Pinned,
				)
				return relation, actions, nil
			}
			return "", nil, err
		}
		targetOwnerID = target.OwnerID
		item.TargetOwnerID = targetOwnerID
	}
	if targetOwnerID == viewerID && viewerID != 0 && relation != "author" {
		relation = "target_owner"
	}
	actions := domain.AvailableActions(
		&item.Comment,
		viewerID,
		targetOwnerID == viewerID,
		item.Pinned,
	)
	return relation, actions, nil
}

func normalizeTargetType(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case TargetActivity,
		TargetMarketplace,
		TargetErrand,
		TargetCarpool,
		TargetCampusCirclePost:
		return value, nil
	default:
		return "", apperror.New(http.StatusBadRequest, "unsupported_comment_target", "该资源类型暂不支持评论")
	}
}

func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return page, size
}

func validStatus(status string) bool {
	switch status {
	case domain.StatusPendingReview, domain.StatusApproved, domain.StatusRejected, domain.StatusWithdrawn:
		return true
	default:
		return false
	}
}

func invalidComment(err error) error {
	return apperror.Wrap(http.StatusBadRequest, "invalid_comment", err.Error(), err)
}

func depthFirst(rootID uint64, values []Item) []Item {
	children := make(map[uint64][]Item, len(values))
	for _, item := range values {
		if item.Comment.ParentId != nil {
			children[*item.Comment.ParentId] = append(children[*item.Comment.ParentId], item)
		}
	}
	result := make([]Item, 0, len(values))
	var visit func(uint64)
	visit = func(parentID uint64) {
		for _, child := range children[parentID] {
			result = append(result, child)
			visit(child.Comment.ID)
		}
	}
	visit(rootID)
	return result
}

func setTargetOwner(items []Item, ownerID uint64) {
	for index := range items {
		items[index].TargetOwnerID = ownerID
	}
}
