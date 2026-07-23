package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	platformquery "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"github.com/weouc-plus/campus-platform/internal/modules/comment/application"
	"github.com/weouc-plus/campus-platform/internal/modules/comment/domain"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store owns comment tree and moderation transactions.
type Store struct {
	db *gorm.DB
}

// NewStore creates a comment persistence adapter.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Create inserts a pending root comment or nested reply.
func (s *Store) Create(
	ctx context.Context,
	authorID uint64,
	input application.CreateInput,
	_ time.Time,
) (application.Item, error) {
	comment := domain.Comment{
		TargetType: input.TargetType,
		TargetId:   input.TargetID,
		AuthorId:   authorID,
		Content:    input.Content,
		Status:     domain.StatusPendingReview,
		Version:    1,
	}
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		q := platformquery.Use(tx).Comment
		if input.ParentID != nil {
			parent, err := q.WithContext(ctx).
				Clauses(clause.Locking{Strength: "UPDATE"}).
				Where(q.ID.Eq(*input.ParentID)).
				First()
			if err != nil {
				return commentNotFound(err)
			}
			if parent.TargetType != input.TargetType || parent.TargetId != input.TargetID {
				return apperror.New(http.StatusBadRequest, "comment_parent_target_mismatch", "回复评论不属于当前资源")
			}
			if parent.Status == domain.StatusWithdrawn ||
				(parent.Status != domain.StatusApproved && parent.AuthorId != authorID) {
				return commentNotFound(gorm.ErrRecordNotFound)
			}
			depth, err := domain.ReplyDepth(parent)
			if err != nil {
				return apperror.Wrap(http.StatusBadRequest, "comment_depth_exceeded", err.Error(), err)
			}
			rootID := parent.ID
			if parent.RootId != nil {
				rootID = *parent.RootId
			}
			comment.ParentId = &parent.ID
			comment.RootId = &rootID
			comment.Depth = depth
			comment.ReplyToUserId = &parent.AuthorId
		}
		if err := q.WithContext(ctx).Create(&comment); err != nil {
			return err
		}
		if comment.RootId == nil {
			comment.RootId = &comment.ID
			if _, err := q.WithContext(ctx).
				Where(q.ID.Eq(comment.ID)).
				Update(q.RootId, comment.ID); err != nil {
				return err
			}
		}
		return writeCommentEvent(tx, &comment, "comment.created")
	})
	if err != nil {
		return application.Item{}, err
	}
	return s.item(ctx, idempotency.DB(ctx, s.db), comment, authorID, false)
}

// Get returns one visible comment with derived display state.
func (s *Store) Get(
	ctx context.Context,
	id,
	viewerID uint64,
	admin bool,
) (application.Item, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).Comment
	comment, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if err != nil {
		return application.Item{}, commentNotFound(err)
	}
	if !domain.VisibleTo(comment, viewerID, admin) {
		return application.Item{}, commentNotFound(gorm.ErrRecordNotFound)
	}
	return s.item(ctx, idempotency.DB(ctx, s.db), *comment, viewerID, admin)
}

// ListRoots returns one page of roots. Pending or rejected rows are visible
// only to their author, while approved roots remain public.
func (s *Store) ListRoots(
	ctx context.Context,
	targetType string,
	targetID,
	viewerID uint64,
	page,
	size int,
) ([]application.Item, int64, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).Comment
	dao := q.WithContext(ctx).Where(
		q.TargetType.Eq(targetType),
		q.TargetId.Eq(targetID),
		q.ParentId.IsNull(),
	)
	dao = visibleComments(dao, q.Status, q.AuthorId, viewerID, false)
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	p := platformquery.Use(idempotency.DB(ctx, s.db)).CommentPin
	pin, pinErr := p.WithContext(ctx).Where(
		p.TargetType.Eq(targetType),
		p.TargetId.Eq(targetID),
	).First()
	if pinErr != nil && !errors.Is(pinErr, gorm.ErrRecordNotFound) {
		return nil, 0, pinErr
	}
	pinnedID := uint64(0)
	if pinErr == nil {
		pinnedID = pin.CommentId
		dao = dao.Where(q.ID.Neq(pinnedID))
	}
	offset := (page - 1) * size
	limit := size
	rows := make([]*domain.Comment, 0, size)
	if pinnedID != 0 {
		if page == 1 {
			pinned, findErr := q.WithContext(ctx).Where(q.ID.Eq(pinnedID)).First()
			if findErr != nil {
				return nil, 0, findErr
			}
			rows = append(rows, pinned)
			limit--
		} else {
			offset--
		}
	}
	if limit > 0 {
		regular, findErr := dao.Order(q.CreatedAt.Desc(), q.ID.Desc()).
			Offset(offset).
			Limit(limit).
			Find()
		if findErr != nil {
			return nil, 0, findErr
		}
		rows = append(rows, regular...)
	}
	items, err := s.items(ctx, idempotency.DB(ctx, s.db), rows, viewerID, false)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListMine returns all lifecycle states owned by an author.
func (s *Store) ListMine(
	ctx context.Context,
	authorID uint64,
	status string,
	page,
	size int,
) ([]application.Item, int64, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).Comment
	dao := q.WithContext(ctx).Where(q.AuthorId.Eq(authorID))
	if status != "" {
		dao = dao.Where(q.Status.Eq(status))
	}
	return s.list(ctx, dao, q.CreatedAt, q.ID, authorID, false, page, size)
}

// ListAdmin returns comments without public visibility filtering.
func (s *Store) ListAdmin(
	ctx context.Context,
	search application.Search,
	page,
	size int,
) ([]application.Item, int64, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).Comment
	dao := q.WithContext(ctx)
	if search.TargetType != "" {
		dao = dao.Where(q.TargetType.Eq(search.TargetType))
	}
	if search.TargetID != 0 {
		dao = dao.Where(q.TargetId.Eq(search.TargetID))
	}
	if search.AuthorID != 0 {
		dao = dao.Where(q.AuthorId.Eq(search.AuthorID))
	}
	if search.Status != "" {
		dao = dao.Where(q.Status.Eq(search.Status))
	}
	if search.Keyword != "" {
		dao = dao.Where(q.Content.Like("%" + search.Keyword + "%"))
	}
	return s.list(ctx, dao, q.CreatedAt, q.ID, 0, true, page, size)
}

// Thread loads the requested comment's complete visible tree.
func (s *Store) Thread(
	ctx context.Context,
	id,
	viewerID uint64,
	admin bool,
) (application.Item, []application.Item, error) {
	db := idempotency.DB(ctx, s.db)
	q := platformquery.Use(db).Comment
	requested, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if err != nil || !domain.VisibleTo(requested, viewerID, admin) {
		if err == nil {
			err = gorm.ErrRecordNotFound
		}
		return application.Item{}, nil, commentNotFound(err)
	}
	rootID := requested.ID
	if requested.RootId != nil {
		rootID = *requested.RootId
	}
	root, err := q.WithContext(ctx).Where(q.ID.Eq(rootID)).First()
	if err != nil || !domain.VisibleTo(root, viewerID, admin) {
		if err == nil {
			err = gorm.ErrRecordNotFound
		}
		return application.Item{}, nil, commentNotFound(err)
	}
	dao := q.WithContext(ctx).Where(q.RootId.Eq(rootID), q.ID.Neq(rootID))
	dao = visibleComments(dao, q.Status, q.AuthorId, viewerID, admin)
	rows, err := dao.Order(q.ID.Asc()).Find()
	if err != nil {
		return application.Item{}, nil, err
	}
	rootItem, err := s.item(ctx, db, *root, viewerID, admin)
	if err != nil {
		return application.Item{}, nil, err
	}
	descendants, err := s.items(ctx, db, rows, viewerID, admin)
	return rootItem, descendants, err
}

// Update edits an owned comment and returns it to moderation.
func (s *Store) Update(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	content string,
	_ time.Time,
) (application.Item, error) {
	return s.mutate(ctx, id, actorID, version, func(tx *gorm.DB, comment *domain.Comment) error {
		if !domain.CanEdit(comment.Status) {
			return invalidState()
		}
		comment.Content = content
		comment.Status = domain.StatusPendingReview
		comment.ReviewReason = nil
		comment.ReviewedBy = nil
		comment.ReviewedAt = nil
		comment.Version++
		if err := saveComment(ctx, tx, comment); err != nil {
			return err
		}
		if err := removePin(ctx, tx, comment); err != nil {
			return err
		}
		return writeCommentEvent(tx, comment, "comment.updated")
	})
}

// Withdraw hides an owned comment in every public query.
func (s *Store) Withdraw(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	_ time.Time,
) (application.Item, error) {
	return s.mutate(ctx, id, actorID, version, func(tx *gorm.DB, comment *domain.Comment) error {
		if !domain.CanWithdraw(comment.Status) {
			return invalidState()
		}
		comment.Status = domain.StatusWithdrawn
		comment.Version++
		if err := saveComment(ctx, tx, comment); err != nil {
			return err
		}
		if err := removePin(ctx, tx, comment); err != nil {
			return err
		}
		return writeCommentEvent(tx, comment, "comment.withdrawn")
	})
}

// SubmitReview resubmits a rejected comment.
func (s *Store) SubmitReview(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	_ time.Time,
) (application.Item, error) {
	return s.mutate(ctx, id, actorID, version, func(tx *gorm.DB, comment *domain.Comment) error {
		if !domain.CanSubmitReview(comment.Status) {
			return invalidState()
		}
		comment.Status = domain.StatusPendingReview
		comment.ReviewReason = nil
		comment.ReviewedBy = nil
		comment.ReviewedAt = nil
		comment.Version++
		if err := saveComment(ctx, tx, comment); err != nil {
			return err
		}
		return writeCommentEvent(tx, comment, "comment.review_submitted")
	})
}

// Review records an administrator decision.
func (s *Store) Review(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	approved bool,
	reason string,
	now time.Time,
) (application.Item, error) {
	return s.mutate(ctx, id, 0, version, func(tx *gorm.DB, comment *domain.Comment) error {
		if !domain.CanReview(comment.Status) {
			return invalidState()
		}
		comment.Status = domain.StatusRejected
		if approved {
			comment.Status = domain.StatusApproved
		}
		comment.ReviewReason = stringPointer(reason)
		comment.ReviewedBy = &adminID
		comment.ReviewedAt = &now
		comment.Version++
		if err := saveComment(ctx, tx, comment); err != nil {
			return err
		}
		if !approved {
			if err := removePin(ctx, tx, comment); err != nil {
				return err
			}
		}
		return writeCommentEvent(tx, comment, "comment.reviewed")
	})
}

// RevokeReview returns an approved or rejected comment to moderation.
func (s *Store) RevokeReview(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	reason string,
	now time.Time,
) (application.Item, error) {
	return s.mutate(ctx, id, 0, version, func(tx *gorm.DB, comment *domain.Comment) error {
		if !domain.CanRevokeReview(comment.Status) {
			return invalidState()
		}
		comment.Status = domain.StatusPendingReview
		comment.ReviewReason = &reason
		comment.ReviewedBy = &adminID
		comment.ReviewedAt = &now
		comment.Version++
		if err := saveComment(ctx, tx, comment); err != nil {
			return err
		}
		if err := removePin(ctx, tx, comment); err != nil {
			return err
		}
		return writeCommentEvent(tx, comment, "comment.review_revoked")
	})
}

// Pin atomically replaces the target's pinned root comment.
func (s *Store) Pin(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	now time.Time,
) (application.Item, error) {
	var comment domain.Comment
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		q := platformquery.Use(tx).Comment
		value, err := q.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(q.ID.Eq(id)).
			First()
		if err != nil {
			return commentNotFound(err)
		}
		comment = *value
		if comment.Version != version {
			return versionConflict()
		}
		if comment.ParentId != nil || comment.Status != domain.StatusApproved {
			return apperror.New(http.StatusConflict, "comment_not_pinnable", "仅审核通过的根评论可以置顶")
		}
		pin := domain.CommentPin{
			TargetType: comment.TargetType,
			TargetId:   comment.TargetId,
			CommentId:  comment.ID,
			PinnedBy:   actorID,
			PinnedAt:   now,
			Version:    1,
		}
		p := platformquery.Use(tx).CommentPin
		// The target unique key is the aggregate lock: concurrent replacements
		// resolve to one row through MySQL's atomic upsert.
		if err := p.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "target_type"}, {Name: "target_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"comment_id": pin.CommentId,
				"pinned_by":  pin.PinnedBy,
				"pinned_at":  pin.PinnedAt,
				"version":    pin.Version,
			}),
		}).Create(&pin); err != nil {
			return err
		}
		return writePinEvent(tx, &comment, actorID, "comment.pinned", now)
	})
	if err != nil {
		return application.Item{}, err
	}
	return s.item(ctx, idempotency.DB(ctx, s.db), comment, actorID, false)
}

// Unpin removes the matching pinned root.
func (s *Store) Unpin(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	now time.Time,
) (application.Item, error) {
	var comment domain.Comment
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		q := platformquery.Use(tx).Comment
		value, err := q.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(q.ID.Eq(id)).
			First()
		if err != nil {
			return commentNotFound(err)
		}
		comment = *value
		if comment.Version != version {
			return versionConflict()
		}
		p := platformquery.Use(tx).CommentPin
		info, err := p.WithContext(ctx).Where(
			p.TargetType.Eq(comment.TargetType),
			p.TargetId.Eq(comment.TargetId),
			p.CommentId.Eq(comment.ID),
		).Delete()
		if err != nil {
			return err
		}
		if info.RowsAffected == 0 {
			return apperror.New(http.StatusConflict, "comment_not_pinned", "该评论当前未置顶")
		}
		return writePinEvent(tx, &comment, actorID, "comment.unpinned", now)
	})
	if err != nil {
		return application.Item{}, err
	}
	return s.item(ctx, idempotency.DB(ctx, s.db), comment, actorID, false)
}

func (s *Store) list(
	ctx context.Context,
	dao platformquery.ICommentDo,
	createdAt field.Time,
	id field.Uint64,
	viewerID uint64,
	admin bool,
	page,
	size int,
) ([]application.Item, int64, error) {
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	rows, err := dao.Order(createdAt.Desc(), id.Desc()).
		Offset((page - 1) * size).
		Limit(size).
		Find()
	if err != nil {
		return nil, 0, err
	}
	items, err := s.items(ctx, idempotency.DB(ctx, s.db), rows, viewerID, admin)
	return items, total, err
}

func (s *Store) mutate(
	ctx context.Context,
	id,
	ownerID,
	version uint64,
	change func(*gorm.DB, *domain.Comment) error,
) (application.Item, error) {
	var comment domain.Comment
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		q := platformquery.Use(tx).Comment
		value, err := q.WithContext(ctx).
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Where(q.ID.Eq(id)).
			First()
		if err != nil {
			return commentNotFound(err)
		}
		comment = *value
		if ownerID != 0 && comment.AuthorId != ownerID {
			return apperror.New(http.StatusForbidden, "not_comment_author", "仅评论作者可以执行此操作")
		}
		if comment.Version != version {
			return versionConflict()
		}
		return change(tx, &comment)
	})
	if err != nil {
		return application.Item{}, err
	}
	return s.item(ctx, idempotency.DB(ctx, s.db), comment, ownerID, ownerID == 0)
}

func (s *Store) items(
	ctx context.Context,
	db *gorm.DB,
	rows []*domain.Comment,
	viewerID uint64,
	admin bool,
) ([]application.Item, error) {
	result := make([]application.Item, 0, len(rows))
	for _, row := range rows {
		item, err := s.item(ctx, db, *row, viewerID, admin)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (s *Store) item(
	ctx context.Context,
	db *gorm.DB,
	comment domain.Comment,
	viewerID uint64,
	admin bool,
) (application.Item, error) {
	q := platformquery.Use(db)
	pinned, err := q.CommentPin.WithContext(ctx).Where(
		q.CommentPin.CommentId.Eq(comment.ID),
	).Count()
	if err != nil {
		return application.Item{}, err
	}
	replies := q.Comment.WithContext(ctx)
	if comment.ParentId == nil {
		replies = replies.Where(q.Comment.RootId.Eq(comment.ID), q.Comment.ID.Neq(comment.ID))
	} else {
		replies = replies.Where(q.Comment.ParentId.Eq(comment.ID))
	}
	replies = visibleComments(
		replies,
		q.Comment.Status,
		q.Comment.AuthorId,
		viewerID,
		admin,
	)
	replyCount, err := replies.Count()
	if err != nil {
		return application.Item{}, err
	}
	return application.Item{Comment: comment, Pinned: pinned != 0, ReplyCount: replyCount}, nil
}

func visibleComments(
	dao platformquery.ICommentDo,
	status field.String,
	authorID field.Uint64,
	viewerID uint64,
	admin bool,
) platformquery.ICommentDo {
	if admin {
		return dao
	}
	if viewerID == 0 {
		return dao.Where(status.Eq(domain.StatusApproved))
	}
	return dao.Where(field.Or(
		status.Eq(domain.StatusApproved),
		authorID.Eq(viewerID),
	))
}

func saveComment(ctx context.Context, tx *gorm.DB, comment *domain.Comment) error {
	return platformquery.Use(tx).Comment.WithContext(ctx).Save(comment)
}

func removePin(ctx context.Context, tx *gorm.DB, comment *domain.Comment) error {
	if comment.ParentId != nil {
		return nil
	}
	p := platformquery.Use(tx).CommentPin
	_, err := p.WithContext(ctx).Where(
		p.TargetType.Eq(comment.TargetType),
		p.TargetId.Eq(comment.TargetId),
		p.CommentId.Eq(comment.ID),
	).Delete()
	return err
}

func writeCommentEvent(tx *gorm.DB, comment *domain.Comment, eventType string) error {
	payload := map[string]any{
		"comment_id":  comment.ID,
		"target_type": comment.TargetType,
		"target_id":   comment.TargetId,
		"author_id":   comment.AuthorId,
		"status":      comment.Status,
		"version":     comment.Version,
	}
	key := fmt.Sprintf("%s:%d:%d", eventType, comment.ID, comment.Version)
	return domainevent.WriteWithKey(tx, "comment", comment.ID, eventType, key, payload)
}

func writePinEvent(
	tx *gorm.DB,
	comment *domain.Comment,
	actorID uint64,
	eventType string,
	now time.Time,
) error {
	payload := map[string]any{
		"comment_id":  comment.ID,
		"target_type": comment.TargetType,
		"target_id":   comment.TargetId,
		"actor_id":    actorID,
		"occurred_at": now,
	}
	key := fmt.Sprintf("%s:%d:%d", eventType, comment.ID, now.UnixNano())
	return domainevent.WriteWithKey(tx, "comment", comment.ID, eventType, key, payload)
}

func commentNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(http.StatusNotFound, "comment_not_found", "评论不存在")
	}
	return err
}

func versionConflict() error {
	return apperror.New(http.StatusConflict, "version_conflict", "评论已被其他请求更新")
}

func invalidState() error {
	return apperror.New(http.StatusConflict, "invalid_comment_state", "当前评论状态不允许此操作")
}

func stringPointer(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
