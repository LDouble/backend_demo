package infrastructure

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
	"gorm.io/gorm"
)

func TestErrandReviewLifecycleControlsPublicVisibility(t *testing.T) {
	db := newErrandReviewTestDB(t)
	store := NewStore(db)
	task := createErrandReviewTask(t, db, domain.ReviewPending)
	now := time.Now().UTC().Truncate(time.Second)

	if rows, total, err := store.ListOpen(context.Background(), 1, 20, now); err != nil ||
		total != 0 || len(rows) != 0 {
		t.Fatalf("pending public rows=%+v total=%d err=%v", rows, total, err)
	}
	approved, err := store.Review(
		context.Background(),
		task.ID,
		99,
		task.Version,
		true,
		"",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if approved.ReviewStatus != domain.ReviewApproved ||
		approved.ReviewReason != nil ||
		approved.Version != task.Version+1 {
		t.Fatalf("approved=%+v", approved)
	}
	if rows, total, listErr := store.ListOpen(context.Background(), 1, 20, now); listErr != nil ||
		total != 1 || len(rows) != 1 || rows[0].ID != task.ID {
		t.Fatalf("approved public rows=%+v total=%d err=%v", rows, total, listErr)
	}

	revoked, err := store.RevokeReview(
		context.Background(),
		task.ID,
		100,
		approved.Version,
		"  收到举报，重新检查  ",
		now.Add(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.ReviewStatus != domain.ReviewPending ||
		revoked.ReviewReason == nil ||
		*revoked.ReviewReason != "收到举报，重新检查" {
		t.Fatalf("revoked=%+v", revoked)
	}
	if rows, total, listErr := store.ListOpen(context.Background(), 1, 20, now); listErr != nil ||
		total != 0 || len(rows) != 0 {
		t.Fatalf("revoked public rows=%+v total=%d err=%v", rows, total, listErr)
	}
}

func TestErrandReviewRejectEditAndResubmit(t *testing.T) {
	db := newErrandReviewTestDB(t)
	store := NewStore(db)
	task := createErrandReviewTask(t, db, domain.ReviewPending)
	now := time.Now().UTC().Truncate(time.Second)

	rejected, err := store.Review(
		context.Background(),
		task.ID,
		99,
		task.Version,
		false,
		"  描述不完整  ",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.ReviewStatus != domain.ReviewRejected ||
		rejected.ReviewReason == nil ||
		*rejected.ReviewReason != "描述不完整" {
		t.Fatalf("rejected=%+v", rejected)
	}

	updated, err := store.Update(
		context.Background(),
		task.ID,
		task.RequesterId,
		rejected.Version,
		domain.TaskInput{
			Title:           "补充后的任务",
			Description:     "描述已经补充完整",
			RewardCents:     task.RewardCents,
			PickupLocation:  task.PickupLocation,
			DropoffLocation: task.DropoffLocation,
			Deadline:        task.Deadline,
		},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ReviewStatus != domain.ReviewDraft ||
		updated.ReviewReason != nil ||
		updated.ReviewedBy != nil ||
		updated.ReviewedAt != nil {
		t.Fatalf("updated=%+v", updated)
	}
	submitted, err := store.SubmitReview(
		context.Background(),
		task.ID,
		task.RequesterId,
		updated.Version,
	)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.ReviewStatus != domain.ReviewPending ||
		submitted.Version != updated.Version+1 {
		t.Fatalf("submitted=%+v", submitted)
	}
}

func TestErrandReviewInvalidBranchesRollBack(t *testing.T) {
	tests := []struct {
		name     string
		version  uint64
		approved bool
		reason   string
	}{
		{name: "rejection reason required", version: 1},
		{name: "stale version", version: 99, approved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newErrandReviewTestDB(t)
			store := NewStore(db)
			task := createErrandReviewTask(t, db, domain.ReviewPending)

			if _, err := store.Review(
				context.Background(),
				task.ID,
				99,
				tt.version,
				tt.approved,
				tt.reason,
				time.Now().UTC(),
			); err == nil {
				t.Fatal("Review() error=nil")
			}
			var persisted domain.Task
			if err := db.First(&persisted, task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if persisted.ReviewStatus != domain.ReviewPending ||
				persisted.Version != task.Version ||
				persisted.ReviewedBy != nil {
				t.Fatalf("persisted=%+v", persisted)
			}
			var count int64
			if err := db.Model(&domainevent.Event{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("events=%d want=0", count)
			}
		})
	}
}

func TestErrandVisibilityAllowsOnlyOwnerBeforeApproval(t *testing.T) {
	db := newErrandReviewTestDB(t)
	store := NewStore(db)
	task := createErrandReviewTask(t, db, domain.ReviewPending)

	if _, err := store.GetVisible(context.Background(), task.ID, task.RequesterId); err != nil {
		t.Fatalf("owner visibility: %v", err)
	}
	if _, err := store.GetVisible(context.Background(), task.ID, task.RequesterId+1); statusOf(err) != 404 {
		t.Fatalf("stranger error=%v", err)
	}
}

func TestCreateErrandStartsPendingReview(t *testing.T) {
	db := newErrandReviewTestDB(t)
	key := sha256.Sum256([]byte("errand review test cipher"))
	cipher, err := configcenter.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, cipher)
	task, err := store.Create(context.Background(), 7, domain.TaskInput{
		Title:           "代取外卖",
		Description:     "从校门送到宿舍",
		RewardCents:     600,
		PickupLocation:  "东门",
		DropoffLocation: "二号宿舍楼",
		Deadline:        time.Now().UTC().Add(time.Hour),
		Contact: domain.ContactInput{
			Type:     "wechat",
			Value:    "review_test",
			Provided: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != domain.TaskOpen ||
		task.ReviewStatus != domain.ReviewPending ||
		task.Version != 1 ||
		task.ContactCiphertext == "" {
		t.Fatalf("created=%+v", task)
	}
}

func TestListAdminErrandsAppliesModerationFilters(t *testing.T) {
	db := newErrandReviewTestDB(t)
	store := NewStore(db)
	target := createErrandReviewTask(t, db, domain.ReviewRejected)
	target.Title = "图书馆资料"
	target.PickupLocation = "图书馆"
	if err := db.Save(target).Error; err != nil {
		t.Fatal(err)
	}
	other := createErrandReviewTask(t, db, domain.ReviewPending)
	other.Status = domain.TaskCancelled
	if err := db.Save(other).Error; err != nil {
		t.Fatal(err)
	}

	rows, total, err := store.ListAdmin(context.Background(), domain.AdminSearch{
		Status:       domain.TaskOpen,
		ReviewStatus: domain.ReviewRejected,
		Keyword:      "图书馆",
	}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].ID != target.ID {
		t.Fatalf("rows=%+v total=%d", rows, total)
	}
}

func TestErrandModerationRejectsInvalidStateTransitions(t *testing.T) {
	db := newErrandReviewTestDB(t)
	store := NewStore(db)
	pending := createErrandReviewTask(t, db, domain.ReviewPending)
	approved := createErrandReviewTask(t, db, domain.ReviewApproved)
	now := time.Now().UTC()

	if _, err := store.SubmitReview(
		context.Background(),
		approved.ID,
		approved.RequesterId,
		approved.Version,
	); err == nil {
		t.Fatal("SubmitReview() approved task error=nil")
	}
	if _, err := store.RevokeReview(
		context.Background(),
		pending.ID,
		99,
		pending.Version,
		"重新审核",
		now,
	); err == nil {
		t.Fatal("RevokeReview() pending task error=nil")
	}
	if _, err := store.RevokeReview(
		context.Background(),
		approved.ID,
		99,
		approved.Version,
		" ",
		now,
	); err == nil {
		t.Fatal("RevokeReview() empty reason error=nil")
	}
}

func TestAcceptErrandRequiresApprovedReview(t *testing.T) {
	db := newErrandReviewTestDB(t)
	store := NewStore(db)
	task := createErrandReviewTask(t, db, domain.ReviewPending)

	if _, _, err := store.Accept(
		context.Background(),
		task.ID,
		task.RequesterId+1,
		task.Version,
		"pending-review",
		time.Now().UTC(),
	); err == nil {
		t.Fatal("Accept() pending task error=nil")
	}
}

func newErrandReviewTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"),
		&gorm.Config{TranslateError: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(
		&domain.Task{},
		&domain.Transition{},
		&tradedomain.Order{},
		&tradedomain.OrderTransition{},
		&domainevent.Event{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func createErrandReviewTask(t *testing.T, db *gorm.DB, reviewStatus string) *domain.Task {
	t.Helper()
	task := &domain.Task{
		Title:           "代取快递",
		Description:     "请从快递站送到宿舍",
		RewardCents:     500,
		Currency:        domain.CurrencyCNY,
		PickupLocation:  "校园快递站",
		DropoffLocation: "一号宿舍楼",
		Deadline:        time.Now().UTC().Add(time.Hour),
		Status:          domain.TaskOpen,
		ReviewStatus:    reviewStatus,
		RequesterId:     7,
		ContactType:     "wechat",
		Version:         1,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

func statusOf(err error) int {
	value, ok := apperror.As(err)
	if !ok {
		return 0
	}
	return value.Status
}
