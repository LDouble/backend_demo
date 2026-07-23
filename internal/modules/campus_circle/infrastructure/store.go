// Package infrastructure persists campus-circle aggregates with GORM.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	platformquery "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql/query"
	"github.com/weouc-plus/campus-platform/internal/modules/campus_circle/application"
	"github.com/weouc-plus/campus-platform/internal/modules/campus_circle/domain"
	"gorm.io/gen/field"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const commentTargetCampusCirclePost = "campus_circle_post"

// Store owns campus-circle hierarchy, moderation, image and like transactions.
type Store struct {
	db *gorm.DB
}

// NewStore creates a campus-circle persistence adapter.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// ListSections returns deterministic roots followed by their direct children.
func (s *Store) ListSections(ctx context.Context, admin bool) ([]domain.CampusCircleSection, error) {
	q := platformquery.Use(idempotency.DB(ctx, s.db)).CampusCircleSection
	dao := q.WithContext(ctx)
	if !admin {
		dao = dao.Where(q.Status.Eq(domain.SectionStatusActive))
	}
	rows, err := dao.Order(q.ParentId.Asc(), q.SortOrder.Asc(), q.ID.Asc()).Find()
	if err != nil {
		return nil, err
	}
	result := make([]domain.CampusCircleSection, 0, len(rows))
	for _, row := range rows {
		result = append(result, *row)
	}
	return result, nil
}

// CreateSection inserts an active root or direct child.
func (s *Store) CreateSection(
	ctx context.Context,
	actorID uint64,
	input domain.SectionInput,
	_ time.Time,
) (*domain.CampusCircleSection, error) {
	section := &domain.CampusCircleSection{
		ParentId:    input.ParentID,
		Slug:        input.Slug,
		Name:        input.Name,
		Description: optionalString(input.Description),
		IconUrl:     optionalString(input.IconURL),
		CoverUrl:    optionalString(input.CoverURL),
		SortOrder:   input.SortOrder,
		Status:      domain.SectionStatusActive,
		CreatedBy:   actorID,
		UpdatedBy:   actorID,
		Version:     1,
	}
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		parent, err := sectionParent(ctx, tx, input.ParentID, true)
		if err != nil {
			return err
		}
		if err = domain.ValidateSectionParent(0, input.ParentID, parent); err != nil {
			return invalidSectionHierarchy()
		}
		if err = platformquery.Use(tx).CampusCircleSection.WithContext(ctx).Create(section); err != nil {
			return err
		}
		return writeSectionEvent(tx, section, "campus_circle.section_created")
	})
	if err != nil {
		return nil, normalizeSectionError(err)
	}
	return section, nil
}

// UpdateSection changes presentation fields while preserving its immutable parent and slug.
func (s *Store) UpdateSection(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	input domain.SectionInput,
	_ time.Time,
) (*domain.CampusCircleSection, error) {
	var section domain.CampusCircleSection
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		value, err := lockSection(ctx, tx, id)
		if err != nil {
			return err
		}
		section = *value
		if section.Version != version {
			return versionConflict()
		}
		section.Name = input.Name
		section.Description = optionalString(input.Description)
		section.IconUrl = optionalString(input.IconURL)
		section.CoverUrl = optionalString(input.CoverURL)
		section.SortOrder = input.SortOrder
		section.UpdatedBy = actorID
		section.Version++
		if err = platformquery.Use(tx).CampusCircleSection.WithContext(ctx).Save(&section); err != nil {
			return err
		}
		return writeSectionEvent(tx, &section, "campus_circle.section_updated")
	})
	if err != nil {
		return nil, normalizeSectionError(err)
	}
	return &section, nil
}

// SetSectionStatus archives or reactivates a section with optimistic locking.
func (s *Store) SetSectionStatus(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	status string,
	_ time.Time,
) (*domain.CampusCircleSection, error) {
	var section domain.CampusCircleSection
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		value, err := lockSection(ctx, tx, id)
		if err != nil {
			return err
		}
		section = *value
		if section.Version != version {
			return versionConflict()
		}
		if err = validateSectionTransition(ctx, tx, &section, status); err != nil {
			return err
		}
		section.Status = status
		section.UpdatedBy = actorID
		section.Version++
		if err = platformquery.Use(tx).CampusCircleSection.WithContext(ctx).Save(&section); err != nil {
			return err
		}
		eventType := "campus_circle.section_archived"
		if status == domain.SectionStatusActive {
			eventType = "campus_circle.section_activated"
		}
		return writeSectionEvent(tx, &section, eventType)
	})
	if err != nil {
		return nil, normalizeSectionError(err)
	}
	return &section, nil
}

// CreatePost inserts a pending post and its ordered images.
func (s *Store) CreatePost(
	ctx context.Context,
	authorID uint64,
	input domain.PostInput,
	_ time.Time,
) (application.Item, error) {
	post := domain.CampusCirclePost{
		SectionId: input.SectionID,
		AuthorId:  authorID,
		Title:     optionalString(input.Title),
		Content:   optionalString(input.Content),
		Status:    domain.PostStatusPendingReview,
		Version:   1,
	}
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		if err := ensureSectionAcceptsPosts(ctx, tx, input.SectionID); err != nil {
			return err
		}
		if err := platformquery.Use(tx).CampusCirclePost.WithContext(ctx).Create(&post); err != nil {
			return err
		}
		if err := replaceImages(ctx, tx, post.ID, input.ImageURLs); err != nil {
			return err
		}
		return writePostEvent(tx, &post, "campus_circle.post_created")
	})
	if err != nil {
		return application.Item{}, normalizePostError(err)
	}
	return s.item(ctx, idempotency.DB(ctx, s.db), post, authorID)
}

// UpdatePost edits an owned post, replaces its images and returns it to moderation.
func (s *Store) UpdatePost(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	input domain.PostInput,
	_ time.Time,
) (application.Item, error) {
	return s.mutatePost(ctx, id, actorID, version, actorID, func(tx *gorm.DB, post *domain.CampusCirclePost) error {
		if !domain.CanEditPost(post.Status) {
			return invalidPostState()
		}
		if err := ensureSectionAcceptsPosts(ctx, tx, input.SectionID); err != nil {
			return err
		}
		post.SectionId = input.SectionID
		post.Title = optionalString(input.Title)
		post.Content = optionalString(input.Content)
		post.Status = domain.PostStatusPendingReview
		post.ReviewReason = nil
		post.ReviewedBy = nil
		post.ReviewedAt = nil
		post.Version++
		if err := savePost(ctx, tx, post); err != nil {
			return err
		}
		if err := replaceImages(ctx, tx, post.ID, input.ImageURLs); err != nil {
			return err
		}
		return writePostEvent(tx, post, "campus_circle.post_updated")
	})
}

// GetPost returns one visible post without leaking hidden rows.
func (s *Store) GetPost(
	ctx context.Context,
	id,
	viewerID uint64,
	admin bool,
) (application.Item, error) {
	db := idempotency.DB(ctx, s.db)
	q := platformquery.Use(db).CampusCirclePost
	post, err := q.WithContext(ctx).Where(q.ID.Eq(id)).First()
	if err != nil {
		return application.Item{}, normalizePostError(err)
	}
	if !domain.VisibleTo(post, viewerID, admin) {
		return application.Item{}, postNotFound()
	}
	return s.item(ctx, db, *post, viewerID)
}

// ListPublicPosts returns approved posts matching public feed filters.
func (s *Store) ListPublicPosts(
	ctx context.Context,
	search application.PublicSearch,
	viewerID uint64,
	page,
	size int,
) ([]application.Item, int64, error) {
	db := idempotency.DB(ctx, s.db)
	q := platformquery.Use(db).CampusCirclePost
	dao := q.WithContext(ctx).Where(q.Status.Eq(domain.PostStatusApproved))
	if search.SectionID != 0 {
		dao = dao.Where(q.SectionId.Eq(search.SectionID))
	}
	if search.ParentSectionID != 0 {
		sectionIDs, err := childSectionIDs(ctx, db, search.ParentSectionID)
		if err != nil {
			return nil, 0, err
		}
		if len(sectionIDs) == 0 {
			return []application.Item{}, 0, nil
		}
		dao = dao.Where(q.SectionId.In(sectionIDs...))
	}
	dao = filterPostKeyword(dao, q.Title, q.Content, search.Keyword)
	return s.listPosts(ctx, db, dao, q.PublishedAt, viewerID, page, size)
}

// ListMyPosts returns every lifecycle state owned by the author.
func (s *Store) ListMyPosts(
	ctx context.Context,
	authorID uint64,
	search application.MineSearch,
	page,
	size int,
) ([]application.Item, int64, error) {
	db := idempotency.DB(ctx, s.db)
	q := platformquery.Use(db).CampusCirclePost
	dao := q.WithContext(ctx).Where(q.AuthorId.Eq(authorID))
	if search.SectionID != 0 {
		dao = dao.Where(q.SectionId.Eq(search.SectionID))
	}
	if search.Status != "" {
		dao = dao.Where(q.Status.Eq(search.Status))
	}
	dao = filterPostKeyword(dao, q.Title, q.Content, search.Keyword)
	return s.listPosts(ctx, db, dao, q.CreatedAt, authorID, page, size)
}

// ListAdminPosts returns all states matching moderation filters.
func (s *Store) ListAdminPosts(
	ctx context.Context,
	search application.AdminSearch,
	page,
	size int,
) ([]application.Item, int64, error) {
	db := idempotency.DB(ctx, s.db)
	q := platformquery.Use(db).CampusCirclePost
	dao := q.WithContext(ctx)
	if search.SectionID != 0 {
		dao = dao.Where(q.SectionId.Eq(search.SectionID))
	}
	if search.AuthorID != 0 {
		dao = dao.Where(q.AuthorId.Eq(search.AuthorID))
	}
	if search.Status != "" {
		dao = dao.Where(q.Status.Eq(search.Status))
	}
	if search.CreatedFrom != nil {
		dao = dao.Where(q.CreatedAt.Gte(*search.CreatedFrom))
	}
	if search.CreatedTo != nil {
		dao = dao.Where(q.CreatedAt.Lte(*search.CreatedTo))
	}
	dao = filterPostKeyword(dao, q.Title, q.Content, search.Keyword)
	return s.listPosts(ctx, db, dao, q.CreatedAt, 0, page, size)
}

// SubmitPostReview returns an owned rejected post to pending moderation.
func (s *Store) SubmitPostReview(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	_ time.Time,
) (application.Item, error) {
	return s.mutatePost(ctx, id, actorID, version, actorID, func(tx *gorm.DB, post *domain.CampusCirclePost) error {
		if !domain.CanSubmitPostReview(post.Status) {
			return invalidPostState()
		}
		if err := ensureSectionAcceptsPosts(ctx, tx, post.SectionId); err != nil {
			return err
		}
		post.Status = domain.PostStatusPendingReview
		post.ReviewReason = nil
		post.ReviewedBy = nil
		post.ReviewedAt = nil
		post.Version++
		if err := savePost(ctx, tx, post); err != nil {
			return err
		}
		return writePostEvent(tx, post, "campus_circle.review_submitted")
	})
}

// WithdrawPost hides an owned post.
func (s *Store) WithdrawPost(
	ctx context.Context,
	id,
	actorID,
	version uint64,
	_ time.Time,
) (application.Item, error) {
	return s.mutatePost(ctx, id, actorID, version, actorID, func(tx *gorm.DB, post *domain.CampusCirclePost) error {
		if !domain.CanWithdrawPost(post.Status) {
			return invalidPostState()
		}
		post.Status = domain.PostStatusWithdrawn
		post.Version++
		if err := savePost(ctx, tx, post); err != nil {
			return err
		}
		return writePostEvent(tx, post, "campus_circle.post_withdrawn")
	})
}

// ReviewPost records an administrator moderation decision.
func (s *Store) ReviewPost(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	approved bool,
	reason string,
	now time.Time,
) (application.Item, error) {
	return s.mutatePost(ctx, id, 0, version, adminID, func(tx *gorm.DB, post *domain.CampusCirclePost) error {
		if !domain.CanReviewPost(post.Status) {
			return invalidPostState()
		}
		if approved {
			if err := ensureSectionAcceptsPosts(ctx, tx, post.SectionId); err != nil {
				return err
			}
		}
		post.Status = domain.PostStatusRejected
		if approved {
			post.Status = domain.PostStatusApproved
			if post.PublishedAt == nil {
				post.PublishedAt = &now
			}
		}
		post.ReviewReason = optionalString(reason)
		post.ReviewedBy = &adminID
		post.ReviewedAt = &now
		post.Version++
		if err := savePost(ctx, tx, post); err != nil {
			return err
		}
		return writePostEvent(tx, post, "campus_circle.post_reviewed")
	})
}

// RevokePostReview returns a decided post to pending moderation.
func (s *Store) RevokePostReview(
	ctx context.Context,
	id,
	adminID,
	version uint64,
	reason string,
	now time.Time,
) (application.Item, error) {
	return s.mutatePost(ctx, id, 0, version, adminID, func(tx *gorm.DB, post *domain.CampusCirclePost) error {
		if !domain.CanRevokePostReview(post.Status) {
			return invalidPostState()
		}
		post.Status = domain.PostStatusPendingReview
		post.ReviewReason = &reason
		post.ReviewedBy = &adminID
		post.ReviewedAt = &now
		post.Version++
		if err := savePost(ctx, tx, post); err != nil {
			return err
		}
		return writePostEvent(tx, post, "campus_circle.review_revoked")
	})
}

// LikePost inherently idempotently inserts one user like.
func (s *Store) LikePost(
	ctx context.Context,
	id,
	actorID uint64,
	now time.Time,
) (application.Item, error) {
	return s.changeLike(ctx, id, actorID, now, true)
}

// UnlikePost inherently idempotently removes one user like.
func (s *Store) UnlikePost(
	ctx context.Context,
	id,
	actorID uint64,
	now time.Time,
) (application.Item, error) {
	return s.changeLike(ctx, id, actorID, now, false)
}

func (s *Store) changeLike(
	ctx context.Context,
	id,
	actorID uint64,
	now time.Time,
	liked bool,
) (application.Item, error) {
	var post domain.CampusCirclePost
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		value, err := lockPost(ctx, tx, id)
		if err != nil {
			return err
		}
		post = *value
		if post.Status != domain.PostStatusApproved {
			return postNotFound()
		}
		if post.AuthorId == actorID {
			return apperror.New(http.StatusConflict, "cannot_like_own_post", "不能点赞自己的帖子")
		}
		q := platformquery.Use(tx).CampusCirclePostLike
		existing, findErr := q.WithContext(ctx).Where(
			q.PostId.Eq(id),
			q.UserId.Eq(actorID),
		).First()
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		if liked && errors.Is(findErr, gorm.ErrRecordNotFound) {
			value := &domain.CampusCirclePostLike{PostId: id, UserId: actorID}
			if err = q.WithContext(ctx).Create(value); err != nil {
				return err
			}
			return writeLikeEvent(tx, &post, actorID, "campus_circle.post_liked", now)
		}
		if !liked && findErr == nil {
			if _, err = q.WithContext(ctx).Delete(existing); err != nil {
				return err
			}
			return writeLikeEvent(tx, &post, actorID, "campus_circle.post_unliked", now)
		}
		return nil
	})
	if err != nil {
		return application.Item{}, normalizePostError(err)
	}
	return s.item(ctx, idempotency.DB(ctx, s.db), post, actorID)
}

func (s *Store) mutatePost(
	ctx context.Context,
	id,
	ownerID,
	version,
	viewerID uint64,
	change func(*gorm.DB, *domain.CampusCirclePost) error,
) (application.Item, error) {
	var post domain.CampusCirclePost
	err := idempotency.DB(ctx, s.db).Transaction(func(tx *gorm.DB) error {
		value, err := lockPost(ctx, tx, id)
		if err != nil {
			return err
		}
		post = *value
		if ownerID != 0 && post.AuthorId != ownerID {
			return apperror.New(http.StatusForbidden, "not_campus_circle_post_author", "仅帖子作者可以执行此操作")
		}
		if post.Version != version {
			return versionConflict()
		}
		return change(tx, &post)
	})
	if err != nil {
		return application.Item{}, normalizePostError(err)
	}
	return s.item(ctx, idempotency.DB(ctx, s.db), post, viewerID)
}

func (s *Store) listPosts(
	ctx context.Context,
	db *gorm.DB,
	dao platformquery.ICampusCirclePostDo,
	orderAt field.Time,
	viewerID uint64,
	page,
	size int,
) ([]application.Item, int64, error) {
	total, err := dao.Count()
	if err != nil {
		return nil, 0, err
	}
	q := platformquery.Use(db).CampusCirclePost
	rows, err := dao.Order(orderAt.Desc(), q.ID.Desc()).
		Offset((page - 1) * size).
		Limit(size).
		Find()
	if err != nil {
		return nil, 0, err
	}
	items, err := s.items(ctx, db, rows, viewerID)
	return items, total, err
}

func (s *Store) item(
	ctx context.Context,
	db *gorm.DB,
	post domain.CampusCirclePost,
	viewerID uint64,
) (application.Item, error) {
	rows := []*domain.CampusCirclePost{&post}
	items, err := s.items(ctx, db, rows, viewerID)
	if err != nil {
		return application.Item{}, err
	}
	return items[0], nil
}

func (s *Store) items(
	ctx context.Context,
	db *gorm.DB,
	rows []*domain.CampusCirclePost,
	viewerID uint64,
) ([]application.Item, error) {
	items := make([]application.Item, 0, len(rows))
	if len(rows) == 0 {
		return items, nil
	}
	postIDs := make([]uint64, 0, len(rows))
	for _, row := range rows {
		postIDs = append(postIDs, row.ID)
	}
	images, err := loadImages(ctx, db, postIDs)
	if err != nil {
		return nil, err
	}
	likeCounts, liked, err := loadLikes(ctx, db, postIDs, viewerID)
	if err != nil {
		return nil, err
	}
	commentCounts, err := loadCommentCounts(ctx, db, postIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		items = append(items, application.Item{
			Post:         *row,
			Images:       images[row.ID],
			LikeCount:    likeCounts[row.ID],
			CommentCount: commentCounts[row.ID],
			Liked:        liked[row.ID],
		})
	}
	return items, nil
}

func loadImages(
	ctx context.Context,
	db *gorm.DB,
	postIDs []uint64,
) (map[uint64][]domain.CampusCirclePostImage, error) {
	result := make(map[uint64][]domain.CampusCirclePostImage, len(postIDs))
	for _, postID := range postIDs {
		result[postID] = []domain.CampusCirclePostImage{}
	}
	q := platformquery.Use(db).CampusCirclePostImage
	rows, err := q.WithContext(ctx).Where(q.PostId.In(postIDs...)).
		Order(q.PostId.Asc(), q.SortOrder.Asc(), q.ID.Asc()).
		Find()
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.PostId] = append(result[row.PostId], *row)
	}
	return result, nil
}

type aggregateCount struct {
	PostID uint64 `gorm:"column:post_id"`
	Count  int64  `gorm:"column:count"`
}

func loadLikes(
	ctx context.Context,
	db *gorm.DB,
	postIDs []uint64,
	viewerID uint64,
) (map[uint64]int64, map[uint64]bool, error) {
	counts := make(map[uint64]int64, len(postIDs))
	liked := make(map[uint64]bool, len(postIDs))
	q := platformquery.Use(db).CampusCirclePostLike
	rows := []aggregateCount{}
	err := q.WithContext(ctx).
		Select(q.PostId.As("post_id"), q.ID.Count().As("count")).
		Where(q.PostId.In(postIDs...)).
		Group(q.PostId).
		Scan(&rows)
	if err != nil {
		return nil, nil, err
	}
	for _, row := range rows {
		counts[row.PostID] = row.Count
	}
	if viewerID == 0 {
		return counts, liked, nil
	}
	viewerLikes, err := q.WithContext(ctx).Where(
		q.PostId.In(postIDs...),
		q.UserId.Eq(viewerID),
	).Find()
	if err != nil {
		return nil, nil, err
	}
	for _, like := range viewerLikes {
		liked[like.PostId] = true
	}
	return counts, liked, nil
}

func loadCommentCounts(
	ctx context.Context,
	db *gorm.DB,
	postIDs []uint64,
) (map[uint64]int64, error) {
	counts := make(map[uint64]int64, len(postIDs))
	q := platformquery.Use(db).Comment
	rows := []aggregateCount{}
	err := q.WithContext(ctx).
		Select(q.TargetId.As("post_id"), q.ID.Count().As("count")).
		Where(
			q.TargetType.Eq(commentTargetCampusCirclePost),
			q.TargetId.In(postIDs...),
			q.Status.Eq("approved"),
			q.ParentId.IsNull(),
		).
		Group(q.TargetId).
		Scan(&rows)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		counts[row.PostID] = row.Count
	}
	return counts, nil
}

func filterPostKeyword(
	dao platformquery.ICampusCirclePostDo,
	title,
	content field.String,
	keyword string,
) platformquery.ICampusCirclePostDo {
	if keyword == "" {
		return dao
	}
	pattern := "%" + keyword + "%"
	return dao.Where(field.Or(title.Like(pattern), content.Like(pattern)))
}

func childSectionIDs(ctx context.Context, db *gorm.DB, parentID uint64) ([]uint64, error) {
	q := platformquery.Use(db).CampusCircleSection
	rows, err := q.WithContext(ctx).Where(
		q.ParentId.Eq(parentID),
		q.Status.Eq(domain.SectionStatusActive),
	).Find()
	if err != nil {
		return nil, err
	}
	ids := make([]uint64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

func ensureSectionAcceptsPosts(ctx context.Context, tx *gorm.DB, sectionID uint64) error {
	q := platformquery.Use(tx).CampusCircleSection
	section, err := q.WithContext(ctx).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where(q.ID.Eq(sectionID)).
		First()
	if err != nil {
		return normalizeSectionError(err)
	}
	if section.ParentId == nil {
		return invalidPostSection()
	}
	parent, err := q.WithContext(ctx).
		Clauses(clause.Locking{Strength: "SHARE"}).
		Where(q.ID.Eq(*section.ParentId)).
		First()
	if err != nil {
		return normalizeSectionError(err)
	}
	if !domain.CanAcceptPosts(section, parent) {
		return invalidPostSection()
	}
	return nil
}

func sectionParent(
	ctx context.Context,
	tx *gorm.DB,
	parentID *uint64,
	lock bool,
) (*domain.CampusCircleSection, error) {
	if parentID == nil {
		return nil, nil
	}
	q := platformquery.Use(tx).CampusCircleSection
	dao := q.WithContext(ctx).Where(q.ID.Eq(*parentID))
	if lock {
		dao = dao.Clauses(clause.Locking{Strength: "SHARE"})
	}
	parent, err := dao.First()
	if err != nil {
		return nil, normalizeSectionError(err)
	}
	return parent, nil
}

func validateSectionTransition(
	ctx context.Context,
	tx *gorm.DB,
	section *domain.CampusCircleSection,
	status string,
) error {
	switch status {
	case domain.SectionStatusArchived:
		if !domain.CanArchiveSection(section.Status) {
			return invalidSectionState()
		}
	case domain.SectionStatusActive:
		if !domain.CanActivateSection(section.Status) {
			return invalidSectionState()
		}
		if section.ParentId != nil {
			parent, err := sectionParent(ctx, tx, section.ParentId, true)
			if err != nil {
				return err
			}
			if err = domain.ValidateSectionParent(section.ID, section.ParentId, parent); err != nil {
				return invalidSectionHierarchy()
			}
		}
	default:
		return invalidSectionState()
	}
	return nil
}

func replaceImages(ctx context.Context, tx *gorm.DB, postID uint64, urls []string) error {
	q := platformquery.Use(tx).CampusCirclePostImage
	if _, err := q.WithContext(ctx).Where(q.PostId.Eq(postID)).Delete(); err != nil {
		return err
	}
	images := make([]*domain.CampusCirclePostImage, 0, len(urls))
	for index, imageURL := range urls {
		images = append(images, &domain.CampusCirclePostImage{
			PostId:    postID,
			Url:       imageURL,
			SortOrder: int64(index),
		})
	}
	if len(images) == 0 {
		return nil
	}
	return q.WithContext(ctx).Create(images...)
}

func lockSection(ctx context.Context, tx *gorm.DB, id uint64) (*domain.CampusCircleSection, error) {
	q := platformquery.Use(tx).CampusCircleSection
	value, err := q.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(q.ID.Eq(id)).
		First()
	if err != nil {
		return nil, normalizeSectionError(err)
	}
	return value, nil
}

func lockPost(ctx context.Context, tx *gorm.DB, id uint64) (*domain.CampusCirclePost, error) {
	q := platformquery.Use(tx).CampusCirclePost
	value, err := q.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(q.ID.Eq(id)).
		First()
	if err != nil {
		return nil, normalizePostError(err)
	}
	return value, nil
}

func savePost(ctx context.Context, tx *gorm.DB, post *domain.CampusCirclePost) error {
	return platformquery.Use(tx).CampusCirclePost.WithContext(ctx).Save(post)
}

func writeSectionEvent(tx *gorm.DB, section *domain.CampusCircleSection, eventType string) error {
	payload := map[string]any{
		"section_id": section.ID,
		"parent_id":  section.ParentId,
		"status":     section.Status,
		"version":    section.Version,
	}
	key := fmt.Sprintf("%s:%d:%d", eventType, section.ID, section.Version)
	return domainevent.WriteWithKey(tx, "campus_circle_section", section.ID, eventType, key, payload)
}

func writePostEvent(tx *gorm.DB, post *domain.CampusCirclePost, eventType string) error {
	payload := map[string]any{
		"post_id":    post.ID,
		"section_id": post.SectionId,
		"author_id":  post.AuthorId,
		"status":     post.Status,
		"version":    post.Version,
	}
	key := fmt.Sprintf("%s:%d:%d", eventType, post.ID, post.Version)
	return domainevent.WriteWithKey(tx, "campus_circle_post", post.ID, eventType, key, payload)
}

func writeLikeEvent(
	tx *gorm.DB,
	post *domain.CampusCirclePost,
	actorID uint64,
	eventType string,
	now time.Time,
) error {
	payload := map[string]any{
		"post_id":     post.ID,
		"actor_id":    actorID,
		"occurred_at": now,
	}
	key := fmt.Sprintf("%s:%d:%d:%d", eventType, post.ID, actorID, now.UnixNano())
	return domainevent.WriteWithKey(tx, "campus_circle_post", post.ID, eventType, key, payload)
}

func normalizeSectionError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(http.StatusNotFound, "campus_circle_section_not_found", "校园圈子模块不存在")
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return apperror.New(http.StatusConflict, "campus_circle_section_slug_exists", "校园圈子模块标识已存在")
	}
	return err
}

func normalizePostError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return postNotFound()
	}
	return err
}

func postNotFound() error {
	return apperror.New(http.StatusNotFound, "campus_circle_post_not_found", "校园圈帖子不存在")
}

func versionConflict() error {
	return apperror.New(http.StatusConflict, "version_conflict", "内容已被其他请求更新")
}

func invalidSectionHierarchy() error {
	return apperror.New(http.StatusConflict, "invalid_campus_circle_section_hierarchy", "校园圈子模块层级无效")
}

func invalidSectionState() error {
	return apperror.New(http.StatusConflict, "invalid_campus_circle_section_state", "校园圈子模块状态不允许该操作")
}

func invalidPostSection() error {
	return apperror.New(http.StatusConflict, "campus_circle_section_unavailable", "只能向启用的叶子子模块发布帖子")
}

func invalidPostState() error {
	return apperror.New(http.StatusConflict, "invalid_campus_circle_post_state", "校园圈帖子当前状态不允许该操作")
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

var _ application.Store = (*Store)(nil)
