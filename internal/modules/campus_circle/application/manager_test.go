package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/campus_circle/domain"
)

type stubStore struct {
	called       string
	sectionInput domain.SectionInput
	postInput    domain.PostInput
	publicSearch PublicSearch
	mineSearch   MineSearch
	adminSearch  AdminSearch
	page         int
	size         int
	status       string
	reason       string
	approved     bool
	now          time.Time
	err          error
	sections     []domain.CampusCircleSection
	item         Item
}

func (s *stubStore) ListSections(context.Context, bool) ([]domain.CampusCircleSection, error) {
	s.called = "list_sections"
	return s.sections, s.err
}
func (s *stubStore) CreateSection(_ context.Context, _ uint64, input domain.SectionInput, now time.Time) (*domain.CampusCircleSection, error) {
	s.called, s.sectionInput, s.now = "create_section", input, now
	return &domain.CampusCircleSection{}, s.err
}
func (s *stubStore) UpdateSection(_ context.Context, _, _, _ uint64, input domain.SectionInput, now time.Time) (*domain.CampusCircleSection, error) {
	s.called, s.sectionInput, s.now = "update_section", input, now
	return &domain.CampusCircleSection{}, s.err
}
func (s *stubStore) SetSectionStatus(_ context.Context, _, _, _ uint64, status string, now time.Time) (*domain.CampusCircleSection, error) {
	s.called, s.status, s.now = "set_section_status", status, now
	return &domain.CampusCircleSection{}, s.err
}
func (s *stubStore) CreatePost(_ context.Context, _ uint64, input domain.PostInput, now time.Time) (Item, error) {
	s.called, s.postInput, s.now = "create_post", input, now
	return s.item, s.err
}
func (s *stubStore) UpdatePost(_ context.Context, _, _, _ uint64, input domain.PostInput, now time.Time) (Item, error) {
	s.called, s.postInput, s.now = "update_post", input, now
	return s.item, s.err
}
func (s *stubStore) GetPost(context.Context, uint64, uint64, bool) (Item, error) {
	s.called = "get_post"
	return s.item, s.err
}
func (s *stubStore) ListPublicPosts(_ context.Context, search PublicSearch, _ uint64, page, size int) ([]Item, int64, error) {
	s.called, s.publicSearch, s.page, s.size = "list_public", search, page, size
	return []Item{s.item}, 1, s.err
}
func (s *stubStore) ListMyPosts(_ context.Context, _ uint64, search MineSearch, page, size int) ([]Item, int64, error) {
	s.called, s.mineSearch, s.page, s.size = "list_mine", search, page, size
	return []Item{s.item}, 1, s.err
}
func (s *stubStore) ListAdminPosts(_ context.Context, search AdminSearch, page, size int) ([]Item, int64, error) {
	s.called, s.adminSearch, s.page, s.size = "list_admin", search, page, size
	return []Item{s.item}, 1, s.err
}
func (s *stubStore) SubmitPostReview(_ context.Context, _, _, _ uint64, now time.Time) (Item, error) {
	s.called, s.now = "submit", now
	return s.item, s.err
}
func (s *stubStore) WithdrawPost(_ context.Context, _, _, _ uint64, now time.Time) (Item, error) {
	s.called, s.now = "withdraw", now
	return s.item, s.err
}
func (s *stubStore) ReviewPost(_ context.Context, _, _, _ uint64, approved bool, reason string, now time.Time) (Item, error) {
	s.called, s.approved, s.reason, s.now = "review", approved, reason, now
	return s.item, s.err
}
func (s *stubStore) RevokePostReview(_ context.Context, _, _, _ uint64, reason string, now time.Time) (Item, error) {
	s.called, s.reason, s.now = "revoke", reason, now
	return s.item, s.err
}
func (s *stubStore) LikePost(_ context.Context, _, _ uint64, now time.Time) (Item, error) {
	s.called, s.now = "like", now
	return s.item, s.err
}
func (s *stubStore) UnlikePost(_ context.Context, _, _ uint64, now time.Time) (Item, error) {
	s.called, s.now = "unlike", now
	return s.item, s.err
}

func TestSectionUseCases(t *testing.T) {
	parentID := uint64(1)
	store := &stubStore{sections: []domain.CampusCircleSection{
		{ID: 1, Name: "root"},
		{ID: 2, ParentId: &parentID, Name: "child"},
		{ID: 3, ParentId: uint64Pointer(99), Name: "orphan"},
	}}
	manager := fixedManager(store)
	tree, err := manager.ListSections(context.Background(), true)
	if err != nil || len(tree) != 1 || len(tree[0].Children) != 1 {
		t.Fatalf("tree = %+v, err = %v", tree, err)
	}
	store.err = errors.New("list")
	if _, err = manager.ListSections(context.Background(), false); err == nil {
		t.Fatal("expected list error")
	}
	store.err = nil

	create := domain.SectionInput{
		ParentID: &parentID, Slug: " section ", Name: " name ",
		IconURL: "https://example.com/icon",
	}
	if _, err = manager.CreateSection(context.Background(), 7, create); err != nil {
		t.Fatalf("create: %v", err)
	}
	if store.called != "create_section" || store.sectionInput.Slug != "section" ||
		store.sectionInput.Name != "name" || !store.now.Equal(fixedTime()) {
		t.Fatalf("create forwarding mismatch: %+v", store)
	}
	if _, err = manager.CreateSection(context.Background(), 7, domain.SectionInput{}); errorCode(err) != "invalid_campus_circle_section" {
		t.Fatalf("create validation error = %v", err)
	}

	update := domain.SectionInput{Name: " changed "}
	if _, err = manager.UpdateSection(context.Background(), 1, 7, 2, update); err != nil {
		t.Fatalf("update: %v", err)
	}
	if store.called != "update_section" || store.sectionInput.Name != "changed" {
		t.Fatalf("update forwarding mismatch: %+v", store)
	}
	if _, err = manager.UpdateSection(context.Background(), 0, 7, 2, update); errorCode(err) != "invalid_campus_circle_section" {
		t.Fatalf("update validation error = %v", err)
	}
	update.Slug = "immutable"
	if _, err = manager.UpdateSection(context.Background(), 1, 7, 2, update); errorCode(err) != "invalid_campus_circle_section" {
		t.Fatalf("slug validation error = %v", err)
	}

	if _, err = manager.ArchiveSection(context.Background(), 1, 7, 2); err != nil ||
		store.status != domain.SectionStatusArchived {
		t.Fatalf("archive: status=%q err=%v", store.status, err)
	}
	if _, err = manager.ActivateSection(context.Background(), 1, 7, 2); err != nil ||
		store.status != domain.SectionStatusActive {
		t.Fatalf("activate: status=%q err=%v", store.status, err)
	}
	if _, err = manager.ArchiveSection(context.Background(), 0, 7, 2); errorCode(err) != "invalid_campus_circle_section" {
		t.Fatalf("archive validation error = %v", err)
	}
}

func TestPostCreateUpdateAndGet(t *testing.T) {
	store := &stubStore{item: Item{Post: domain.CampusCirclePost{ID: 10}}}
	manager := fixedManager(store)
	input := domain.PostInput{
		SectionID: 2,
		Title:     " title ",
		ImageURLs: []string{" https://example.com/a "},
	}
	item, err := manager.CreatePost(context.Background(), 7, input)
	if err != nil || item.Post.ID != 10 || store.postInput.Title != "title" {
		t.Fatalf("create item=%+v input=%+v err=%v", item, store.postInput, err)
	}
	if _, err = manager.CreatePost(context.Background(), 0, input); errorCode(err) != "invalid_campus_circle_post" {
		t.Fatalf("actor validation error=%v", err)
	}
	if _, err = manager.CreatePost(context.Background(), 7, domain.PostInput{}); errorCode(err) != "invalid_campus_circle_post" {
		t.Fatalf("input validation error=%v", err)
	}
	if _, err = manager.UpdatePost(context.Background(), 10, 7, 2, input); err != nil ||
		store.called != "update_post" {
		t.Fatalf("update called=%s err=%v", store.called, err)
	}
	if _, err = manager.UpdatePost(context.Background(), 10, 7, 0, input); errorCode(err) != "invalid_campus_circle_post" {
		t.Fatalf("version validation error=%v", err)
	}
	if _, err = manager.UpdatePost(context.Background(), 10, 7, 2, domain.PostInput{}); errorCode(err) != "invalid_campus_circle_post" {
		t.Fatalf("update input validation error=%v", err)
	}
	if _, err = manager.GetPost(context.Background(), 10, 7, false); err != nil ||
		store.called != "get_post" {
		t.Fatalf("get called=%s err=%v", store.called, err)
	}
	if _, err = manager.GetPost(context.Background(), 0, 7, false); errorCode(err) != "campus_circle_post_not_found" {
		t.Fatalf("get validation error=%v", err)
	}
}

func TestPostLists(t *testing.T) {
	store := &stubStore{}
	manager := fixedManager(store)
	if _, _, err := manager.ListPublicPosts(context.Background(), PublicSearch{Keyword: " q "}, 7, 0, 101); err != nil {
		t.Fatal(err)
	}
	if store.publicSearch.Keyword != "q" || store.page != 1 || store.size != 20 {
		t.Fatalf("public normalization: %+v", store)
	}
	if _, _, err := manager.ListMyPosts(context.Background(), 7, MineSearch{Keyword: " mine "}, 2, 10); err != nil {
		t.Fatal(err)
	}
	if store.mineSearch.Keyword != "mine" || store.page != 2 || store.size != 10 {
		t.Fatalf("mine normalization: %+v", store)
	}
	from := fixedTime()
	to := from.Add(time.Hour)
	if _, _, err := manager.ListAdminPosts(context.Background(), AdminSearch{
		Keyword: " admin ", CreatedFrom: &from, CreatedTo: &to,
	}, 0, 0); err != nil {
		t.Fatal(err)
	}
	if store.adminSearch.Keyword != "admin" || store.page != 1 || store.size != 20 {
		t.Fatalf("admin normalization: %+v", store)
	}
	if _, _, err := manager.ListAdminPosts(context.Background(), AdminSearch{
		CreatedFrom: &to, CreatedTo: &from,
	}, 1, 20); errorCode(err) != "invalid_campus_circle_time_range" {
		t.Fatalf("time-range error=%v", err)
	}
}

func TestPostLifecycleAndInteractions(t *testing.T) {
	store := &stubStore{item: Item{Post: domain.CampusCirclePost{
		ID: 1, AuthorId: 7, Status: domain.PostStatusApproved,
	}}}
	manager := fixedManager(store)
	ctx := context.Background()

	if _, err := manager.SubmitPostReview(ctx, 1, 7, 2); err != nil || store.called != "submit" {
		t.Fatalf("submit called=%s err=%v", store.called, err)
	}
	if _, err := manager.WithdrawPost(ctx, 1, 7, 2); err != nil || store.called != "withdraw" {
		t.Fatalf("withdraw called=%s err=%v", store.called, err)
	}
	if _, err := manager.SubmitPostReview(ctx, 0, 7, 2); errorCode(err) != "invalid_campus_circle_post" {
		t.Fatalf("submit validation error=%v", err)
	}
	if _, err := manager.WithdrawPost(ctx, 1, 0, 2); errorCode(err) != "invalid_campus_circle_post" {
		t.Fatalf("withdraw validation error=%v", err)
	}

	if _, err := manager.ReviewPost(ctx, 1, 9, 2, true, " ignored "); err != nil ||
		!store.approved || store.reason != "ignored" {
		t.Fatalf("approve state=%+v err=%v", store, err)
	}
	if _, err := manager.ReviewPost(ctx, 1, 9, 2, false, " reason "); err != nil ||
		store.approved || store.reason != "reason" {
		t.Fatalf("reject state=%+v err=%v", store, err)
	}
	if _, err := manager.ReviewPost(ctx, 1, 9, 2, false, " "); errorCode(err) != "rejection_reason_required" {
		t.Fatalf("missing reason error=%v", err)
	}
	if _, err := manager.ReviewPost(ctx, 0, 9, 2, true, ""); errorCode(err) != "invalid_campus_circle_review" {
		t.Fatalf("invalid review error=%v", err)
	}
	if _, err := manager.ReviewPost(ctx, 1, 9, 2, true, string(make([]rune, 501))); errorCode(err) != "invalid_campus_circle_review" {
		t.Fatalf("long review error=%v", err)
	}
	if _, err := manager.RevokePostReview(ctx, 1, 9, 2, " revoke "); err != nil ||
		store.reason != "revoke" || store.called != "revoke" {
		t.Fatalf("revoke state=%+v err=%v", store, err)
	}
	if _, err := manager.RevokePostReview(ctx, 1, 9, 2, ""); errorCode(err) != "revoke_reason_required" {
		t.Fatalf("revoke validation error=%v", err)
	}

	if _, err := manager.LikePost(ctx, 1, 8); err != nil || store.called != "like" {
		t.Fatalf("like called=%s err=%v", store.called, err)
	}
	if _, err := manager.UnlikePost(ctx, 1, 8); err != nil || store.called != "unlike" {
		t.Fatalf("unlike called=%s err=%v", store.called, err)
	}
	if _, err := manager.LikePost(ctx, 0, 8); errorCode(err) != "campus_circle_post_not_found" {
		t.Fatalf("like validation error=%v", err)
	}
	if _, err := manager.UnlikePost(ctx, 1, 0); errorCode(err) != "campus_circle_post_not_found" {
		t.Fatalf("unlike validation error=%v", err)
	}

	relation, actions := manager.ViewerContext(store.item, 8, false)
	if relation != domain.ViewerRelationOther || len(actions) != 2 {
		t.Fatalf("viewer context relation=%q actions=%v", relation, actions)
	}
}

func TestNormalizePageAndEmptyTree(t *testing.T) {
	if page, size := NormalizePage(3, 100); page != 3 || size != 100 {
		t.Fatalf("page=%d size=%d", page, size)
	}
	tree := BuildSectionTree(nil)
	if tree == nil || len(tree) != 0 {
		t.Fatalf("empty tree should be non-nil: %#v", tree)
	}
}

func fixedManager(store Store) *Manager {
	manager := NewManager(store)
	manager.now = fixedTime
	return manager
}

func fixedTime() time.Time {
	return time.Date(2026, 7, 23, 14, 0, 0, 0, time.FixedZone("CST", 8*60*60))
}

func uint64Pointer(value uint64) *uint64 { return &value }

func errorCode(err error) string {
	appErr, ok := apperror.As(err)
	if !ok {
		return ""
	}
	return appErr.Code
}
