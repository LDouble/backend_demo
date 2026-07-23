package infrastructure

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/modules/campus_circle/application"
	"github.com/weouc-plus/campus-platform/internal/modules/campus_circle/domain"
	commentdomain "github.com/weouc-plus/campus-platform/internal/modules/comment/domain"
	"gorm.io/gorm"
)

func TestStoreSectionHierarchyLifecycle(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 14, 0, 0, 0, time.UTC)

	root, err := store.CreateSection(ctx, 10, domain.SectionInput{
		Slug: "life", Name: "校园生活", SortOrder: 1,
	}, now)
	if err != nil {
		t.Fatalf("CreateSection(root): %v", err)
	}
	child, err := store.CreateSection(ctx, 10, domain.SectionInput{
		ParentID: &root.ID, Slug: "daily", Name: "日常分享", SortOrder: 2,
	}, now)
	if err != nil {
		t.Fatalf("CreateSection(child): %v", err)
	}
	if _, err = store.CreateSection(ctx, 10, domain.SectionInput{
		ParentID: &child.ID, Slug: "too-deep", Name: "三级", SortOrder: 1,
	}, now); err == nil {
		t.Fatal("CreateSection(grandchild) error = nil")
	}
	if _, err = store.CreateSection(ctx, 10, domain.SectionInput{
		Slug: "life", Name: "重复标识", SortOrder: 9,
	}, now); err == nil {
		t.Fatal("CreateSection(duplicate slug) error = nil")
	}

	otherRoot, err := store.CreateSection(ctx, 10, domain.SectionInput{
		Slug: "help", Name: "校园互助", SortOrder: 3,
	}, now)
	if err != nil {
		t.Fatalf("CreateSection(other root): %v", err)
	}
	updated, err := store.UpdateSection(ctx, child.ID, 11, child.Version, domain.SectionInput{
		ParentID:  &otherRoot.ID,
		Name:      "新的日常",
		SortOrder: 4,
	}, now)
	if err != nil {
		t.Fatalf("UpdateSection: %v", err)
	}
	if updated.ParentId == nil || *updated.ParentId != root.ID {
		t.Fatalf("parent changed: %#v", updated.ParentId)
	}
	if updated.Slug != "daily" || updated.Name != "新的日常" || updated.UpdatedBy != 11 {
		t.Fatalf("updated section = %#v", updated)
	}
	if _, err = store.UpdateSection(
		ctx,
		child.ID,
		11,
		child.Version,
		domain.SectionInput{Name: "冲突"},
		now,
	); err == nil {
		t.Fatal("UpdateSection(stale version) error = nil")
	}

	archived, err := store.SetSectionStatus(
		ctx,
		child.ID,
		11,
		updated.Version,
		domain.SectionStatusArchived,
		now,
	)
	if err != nil {
		t.Fatalf("SetSectionStatus(archive): %v", err)
	}
	publicSections, err := store.ListSections(ctx, false)
	if err != nil {
		t.Fatalf("ListSections(public): %v", err)
	}
	if containsSection(publicSections, child.ID) {
		t.Fatal("public sections contain archived child")
	}
	adminSections, err := store.ListSections(ctx, true)
	if err != nil {
		t.Fatalf("ListSections(admin): %v", err)
	}
	if !containsSection(adminSections, child.ID) {
		t.Fatal("admin sections omit archived child")
	}

	if _, err = store.CreatePost(ctx, 20, domain.PostInput{
		SectionID: child.ID, Content: "不能发布",
	}, now); err == nil {
		t.Fatal("CreatePost(archived section) error = nil")
	}
	activated, err := store.SetSectionStatus(
		ctx,
		child.ID,
		11,
		archived.Version,
		domain.SectionStatusActive,
		now,
	)
	if err != nil || activated.Status != domain.SectionStatusActive {
		t.Fatalf("SetSectionStatus(activate) = %#v, %v", activated, err)
	}

	var events int64
	if err = db.Model(&domainevent.Event{}).Count(&events).Error; err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 6 {
		t.Fatalf("event count = %d, want 6", events)
	}
}

func TestStorePostModerationVisibilityAndAggregates(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 15, 0, 0, 0, time.UTC)
	_, child := createSectionTree(t, store, now)

	created, err := store.CreatePost(ctx, 20, domain.PostInput{
		SectionID: child.ID,
		Title:     "第一条",
		Content:   "校园生活",
		ImageURLs: []string{"https://example.com/1.png", "https://example.com/2.png"},
	}, now)
	if err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	if created.Post.Status != domain.PostStatusPendingReview || len(created.Images) != 2 {
		t.Fatalf("created item = %#v", created)
	}
	if _, err = store.GetPost(ctx, created.Post.ID, 0, false); err == nil {
		t.Fatal("GetPost(guest pending) error = nil")
	}
	if _, err = store.GetPost(ctx, created.Post.ID, 20, false); err != nil {
		t.Fatalf("GetPost(owner pending): %v", err)
	}

	archived, err := store.SetSectionStatus(
		ctx,
		child.ID,
		99,
		child.Version,
		domain.SectionStatusArchived,
		now,
	)
	if err != nil {
		t.Fatalf("SetSectionStatus(archive before review): %v", err)
	}
	if _, err = store.ReviewPost(
		ctx,
		created.Post.ID,
		99,
		created.Post.Version,
		true,
		"",
		now,
	); err == nil {
		t.Fatal("ReviewPost(archived section) error = nil")
	}
	if _, err = store.SetSectionStatus(
		ctx,
		child.ID,
		99,
		archived.Version,
		domain.SectionStatusActive,
		now,
	); err != nil {
		t.Fatalf("SetSectionStatus(reactivate before review): %v", err)
	}
	approved, err := store.ReviewPost(
		ctx,
		created.Post.ID,
		99,
		created.Post.Version,
		true,
		"",
		now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("ReviewPost(approve): %v", err)
	}
	if approved.Post.PublishedAt == nil || approved.Post.Status != domain.PostStatusApproved {
		t.Fatalf("approved post = %#v", approved.Post)
	}
	rootComment := &commentdomain.Comment{
		TargetType: commentTargetCampusCirclePost,
		TargetId:   approved.Post.ID,
		AuthorId:   30,
		Depth:      0,
		Content:    "可见评论",
		Status:     "approved",
		Version:    1,
	}
	if err = db.Create(rootComment).Error; err != nil {
		t.Fatalf("create approved comment: %v", err)
	}
	if err = db.Create(&commentdomain.Comment{
		TargetType: commentTargetCampusCirclePost,
		TargetId:   approved.Post.ID,
		AuthorId:   32,
		ParentId:   &rootComment.ID,
		RootId:     &rootComment.ID,
		Depth:      1,
		Content:    "审核通过的回复不计入根评论数",
		Status:     "approved",
		Version:    1,
	}).Error; err != nil {
		t.Fatalf("create approved reply: %v", err)
	}
	if err = db.Create(&commentdomain.Comment{
		TargetType: commentTargetCampusCirclePost,
		TargetId:   approved.Post.ID,
		AuthorId:   31,
		Depth:      0,
		Content:    "待审核评论",
		Status:     "pending_review",
		Version:    1,
	}).Error; err != nil {
		t.Fatalf("create pending comment: %v", err)
	}

	liked, err := store.LikePost(ctx, approved.Post.ID, 30, now)
	if err != nil {
		t.Fatalf("LikePost: %v", err)
	}
	if liked.LikeCount != 1 || !liked.Liked {
		t.Fatalf("liked item = %#v", liked)
	}
	likedAgain, err := store.LikePost(ctx, approved.Post.ID, 30, now.Add(time.Second))
	if err != nil || likedAgain.LikeCount != 1 {
		t.Fatalf("LikePost(idempotent) = %#v, %v", likedAgain, err)
	}
	if _, err = store.LikePost(ctx, approved.Post.ID, 20, now); err == nil {
		t.Fatal("LikePost(owner) error = nil")
	}

	items, total, err := store.ListPublicPosts(
		ctx,
		application.PublicSearch{
			ParentSectionID: *child.ParentId,
			Keyword:         "校园",
		},
		30,
		1,
		20,
	)
	if err != nil {
		t.Fatalf("ListPublicPosts: %v", err)
	}
	if total != 1 || len(items) != 1 || len(items[0].Images) != 2 ||
		items[0].LikeCount != 1 || items[0].CommentCount != 1 || !items[0].Liked {
		t.Fatalf("public page = %#v, total %d", items, total)
	}

	unliked, err := store.UnlikePost(ctx, approved.Post.ID, 30, now.Add(2*time.Second))
	if err != nil || unliked.LikeCount != 0 || unliked.Liked {
		t.Fatalf("UnlikePost = %#v, %v", unliked, err)
	}
	unlikedAgain, err := store.UnlikePost(ctx, approved.Post.ID, 30, now.Add(3*time.Second))
	if err != nil || unlikedAgain.LikeCount != 0 {
		t.Fatalf("UnlikePost(idempotent) = %#v, %v", unlikedAgain, err)
	}

	if _, err = store.UpdatePost(ctx, approved.Post.ID, 21, approved.Post.Version, domain.PostInput{
		SectionID: child.ID, Content: "越权",
	}, now); err == nil {
		t.Fatal("UpdatePost(other user) error = nil")
	}
	updated, err := store.UpdatePost(ctx, approved.Post.ID, 20, approved.Post.Version, domain.PostInput{
		SectionID: child.ID,
		Content:   "修改后",
		ImageURLs: []string{"https://example.com/new.png"},
	}, now)
	if err != nil {
		t.Fatalf("UpdatePost(owner): %v", err)
	}
	if updated.Post.Status != domain.PostStatusPendingReview || len(updated.Images) != 1 {
		t.Fatalf("updated item = %#v", updated)
	}
	public, total, err := store.ListPublicPosts(
		ctx,
		application.PublicSearch{SectionID: child.ID},
		0,
		1,
		20,
	)
	if err != nil || total != 0 || len(public) != 0 {
		t.Fatalf("public after update = %#v, %d, %v", public, total, err)
	}
}

func TestListPublicPostsOrdersByPublishedAt(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 17, 0, 0, 0, time.UTC)
	_, child := createSectionTree(t, store, now)
	olderPublished := now
	newerPublished := now.Add(time.Hour)
	newerCreated := now.Add(2 * time.Hour)
	olderCreated := now.Add(-time.Hour)
	createdLaterButPublishedEarlier := &domain.CampusCirclePost{
		SectionId:   child.ID,
		AuthorId:    1,
		Content:     optionalString("较早发布"),
		Status:      domain.PostStatusApproved,
		PublishedAt: &olderPublished,
		Version:     1,
		CreatedAt:   newerCreated,
	}
	createdEarlierButPublishedLater := &domain.CampusCirclePost{
		SectionId:   child.ID,
		AuthorId:    2,
		Content:     optionalString("较晚发布"),
		Status:      domain.PostStatusApproved,
		PublishedAt: &newerPublished,
		Version:     1,
		CreatedAt:   olderCreated,
	}
	if err := db.Create(createdLaterButPublishedEarlier).Error; err != nil {
		t.Fatalf("create earlier-published post: %v", err)
	}
	if err := db.Create(createdEarlierButPublishedLater).Error; err != nil {
		t.Fatalf("create later-published post: %v", err)
	}
	items, total, err := store.ListPublicPosts(
		ctx,
		application.PublicSearch{SectionID: child.ID},
		0,
		1,
		20,
	)
	if err != nil {
		t.Fatalf("ListPublicPosts: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("public page len=%d total=%d", len(items), total)
	}
	if items[0].Post.ID != createdEarlierButPublishedLater.ID ||
		items[1].Post.ID != createdLaterButPublishedEarlier.ID {
		t.Fatalf("public order = [%d, %d]", items[0].Post.ID, items[1].Post.ID)
	}
}

func TestStorePostRejectResubmitRevokeWithdrawAndFilters(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 23, 16, 0, 0, 0, time.UTC)
	_, child := createSectionTree(t, store, now)
	created, err := store.CreatePost(ctx, 40, domain.PostInput{
		SectionID: child.ID, Title: "审核记录", Content: "等待审核",
	}, now)
	if err != nil {
		t.Fatalf("CreatePost: %v", err)
	}
	rejected, err := store.ReviewPost(
		ctx,
		created.Post.ID,
		99,
		created.Post.Version,
		false,
		"内容不完整",
		now,
	)
	if err != nil || rejected.Post.Status != domain.PostStatusRejected {
		t.Fatalf("ReviewPost(reject) = %#v, %v", rejected, err)
	}
	submitted, err := store.SubmitPostReview(
		ctx,
		rejected.Post.ID,
		40,
		rejected.Post.Version,
		now,
	)
	if err != nil || submitted.Post.Status != domain.PostStatusPendingReview {
		t.Fatalf("SubmitPostReview = %#v, %v", submitted, err)
	}
	approved, err := store.ReviewPost(
		ctx,
		submitted.Post.ID,
		99,
		submitted.Post.Version,
		true,
		"",
		now,
	)
	if err != nil {
		t.Fatalf("ReviewPost(approve): %v", err)
	}
	revoked, err := store.RevokePostReview(
		ctx,
		approved.Post.ID,
		99,
		approved.Post.Version,
		"重新检查",
		now,
	)
	if err != nil || revoked.Post.Status != domain.PostStatusPendingReview {
		t.Fatalf("RevokePostReview = %#v, %v", revoked, err)
	}
	withdrawn, err := store.WithdrawPost(
		ctx,
		revoked.Post.ID,
		40,
		revoked.Post.Version,
		now,
	)
	if err != nil || withdrawn.Post.Status != domain.PostStatusWithdrawn {
		t.Fatalf("WithdrawPost = %#v, %v", withdrawn, err)
	}
	if _, err = store.SubmitPostReview(
		ctx,
		withdrawn.Post.ID,
		40,
		withdrawn.Post.Version,
		now,
	); err == nil {
		t.Fatal("SubmitPostReview(withdrawn) error = nil")
	}

	mine, total, err := store.ListMyPosts(ctx, 40, application.MineSearch{
		SectionID: child.ID,
		Status:    domain.PostStatusWithdrawn,
		Keyword:   "审核",
	}, 1, 20)
	if err != nil || total != 1 || len(mine) != 1 {
		t.Fatalf("ListMyPosts = %#v, %d, %v", mine, total, err)
	}
	from := created.Post.CreatedAt.Add(-time.Hour)
	to := created.Post.CreatedAt.Add(time.Hour)
	admin, total, err := store.ListAdminPosts(ctx, application.AdminSearch{
		SectionID:   child.ID,
		AuthorID:    40,
		Status:      domain.PostStatusWithdrawn,
		Keyword:     "等待",
		CreatedFrom: &from,
		CreatedTo:   &to,
	}, 1, 20)
	if err != nil || total != 1 || len(admin) != 1 {
		t.Fatalf("ListAdminPosts = %#v, %d, %v", admin, total, err)
	}
}

func TestStoreCreatePostRollsBackWhenEventWriteFails(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&domain.CampusCircleSection{},
		&domain.CampusCirclePost{},
		&domain.CampusCirclePostImage{},
		&domain.CampusCirclePostLike{},
		&commentdomain.Comment{},
	); err != nil {
		t.Fatalf("AutoMigrate without events: %v", err)
	}
	store := NewStore(db)
	now := time.Now().UTC()
	_, child := createSectionTreeWithoutEvents(t, db)
	if _, err := store.CreatePost(context.Background(), 50, domain.PostInput{
		SectionID: child.ID,
		Content:   "应该回滚",
		ImageURLs: []string{"https://example.com/rollback.png"},
	}, now); err == nil {
		t.Fatal("CreatePost without domain_events error = nil")
	}
	var posts, images int64
	if err := db.Model(&domain.CampusCirclePost{}).Count(&posts).Error; err != nil {
		t.Fatalf("count posts: %v", err)
	}
	if err := db.Model(&domain.CampusCirclePostImage{}).Count(&images).Error; err != nil {
		t.Fatalf("count images: %v", err)
	}
	if posts != 0 || images != 0 {
		t.Fatalf("rollback counts posts=%d images=%d", posts, images)
	}
}

func TestGeneratedRepositoryAndWiring(t *testing.T) {
	_, db := newTestStore(t)
	ctx := context.Background()
	repository := NewRepository(db)
	section := &domain.CampusCircleSection{
		Slug:      "repository",
		Name:      "Repository",
		Status:    domain.SectionStatusActive,
		CreatedBy: 1,
		UpdatedBy: 1,
		Version:   1,
	}
	if err := repository.Create(ctx, section); err != nil {
		t.Fatalf("Repository.Create: %v", err)
	}
	found, err := repository.Get(ctx, section.ID)
	if err != nil || found.Slug != section.Slug {
		t.Fatalf("Repository.Get = %#v, %v", found, err)
	}
	rows, total, err := repository.List(ctx, 0, 0)
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("Repository.List = %#v, %d, %v", rows, total, err)
	}
	section.Name = "Updated"
	section.Version++
	if err = repository.Update(ctx, section); err != nil {
		t.Fatalf("Repository.Update: %v", err)
	}
	if manager := NewManager(db); manager == nil {
		t.Fatal("NewManager() = nil")
	}
}

func TestStoreRejectsInvalidSectionTransitions(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	root, child := createSectionTree(t, store, now)

	if _, err := store.SetSectionStatus(
		ctx,
		root.ID,
		1,
		root.Version,
		"unknown",
		now,
	); err == nil {
		t.Fatal("SetSectionStatus(unknown) error = nil")
	}
	if _, err := store.SetSectionStatus(
		ctx,
		root.ID,
		1,
		root.Version,
		domain.SectionStatusActive,
		now,
	); err == nil {
		t.Fatal("SetSectionStatus(active -> active) error = nil")
	}
	archivedRoot, err := store.SetSectionStatus(
		ctx,
		root.ID,
		1,
		root.Version,
		domain.SectionStatusArchived,
		now,
	)
	if err != nil {
		t.Fatalf("archive root: %v", err)
	}
	archivedChild, err := store.SetSectionStatus(
		ctx,
		child.ID,
		1,
		child.Version,
		domain.SectionStatusArchived,
		now,
	)
	if err != nil {
		t.Fatalf("archive child: %v", err)
	}
	if _, err = store.SetSectionStatus(
		ctx,
		child.ID,
		1,
		archivedChild.Version,
		domain.SectionStatusActive,
		now,
	); err == nil {
		t.Fatal("activate child below archived root error = nil")
	}
	if _, err = store.SetSectionStatus(
		ctx,
		root.ID,
		1,
		archivedRoot.Version,
		domain.SectionStatusActive,
		now,
	); err != nil {
		t.Fatalf("reactivate root: %v", err)
	}
}

func newTestStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	db := openTestDB(t)
	if err := db.AutoMigrate(
		&domain.CampusCircleSection{},
		&domain.CampusCirclePost{},
		&domain.CampusCirclePostImage{},
		&domain.CampusCirclePostLike{},
		&commentdomain.Comment{},
		&domainevent.Event{},
	); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	return NewStore(db), db
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:campus_circle_%s?mode=memory&cache=shared&_foreign_keys=1", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	return db
}

func createSectionTree(
	t *testing.T,
	store *Store,
	now time.Time,
) (*domain.CampusCircleSection, *domain.CampusCircleSection) {
	t.Helper()
	root, err := store.CreateSection(context.Background(), 1, domain.SectionInput{
		Slug: "root", Name: "一级", SortOrder: 1,
	}, now)
	if err != nil {
		t.Fatalf("CreateSection(root): %v", err)
	}
	child, err := store.CreateSection(context.Background(), 1, domain.SectionInput{
		ParentID: &root.ID, Slug: "child", Name: "二级", SortOrder: 1,
	}, now)
	if err != nil {
		t.Fatalf("CreateSection(child): %v", err)
	}
	return root, child
}

func createSectionTreeWithoutEvents(
	t *testing.T,
	db *gorm.DB,
) (*domain.CampusCircleSection, *domain.CampusCircleSection) {
	t.Helper()
	root := &domain.CampusCircleSection{
		Slug: "root", Name: "一级", Status: domain.SectionStatusActive,
		CreatedBy: 1, UpdatedBy: 1, Version: 1,
	}
	if err := db.Create(root).Error; err != nil {
		t.Fatalf("create root fixture: %v", err)
	}
	child := &domain.CampusCircleSection{
		ParentId: &root.ID, Slug: "child", Name: "二级",
		Status: domain.SectionStatusActive, CreatedBy: 1, UpdatedBy: 1, Version: 1,
	}
	if err := db.Create(child).Error; err != nil {
		t.Fatalf("create child fixture: %v", err)
	}
	return root, child
}

func containsSection(sections []domain.CampusCircleSection, id uint64) bool {
	for _, section := range sections {
		if section.ID == id {
			return true
		}
	}
	return false
}
