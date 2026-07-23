package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
)

func TestListMineFiltersAndPagination(t *testing.T) {
	db := newErrandReviewTestDB(t)
	store := NewStore(db)
	now := time.Now().UTC()
	runnerSeven := uint64(7)
	tasks := []domain.Task{
		{
			Title: "发布待审", Description: "待审", RewardCents: 100,
			Currency: domain.CurrencyCNY, PickupLocation: "A", DropoffLocation: "B",
			Deadline: now.Add(time.Hour), Status: domain.TaskOpen,
			ReviewStatus: domain.ReviewPending, RequesterId: 7, ContactType: "phone", Version: 1,
		},
		{
			Title: "发布完成", Description: "完成", RewardCents: 200,
			Currency: domain.CurrencyCNY, PickupLocation: "A", DropoffLocation: "B",
			Deadline: now.Add(time.Hour), Status: domain.TaskCompleted,
			ReviewStatus: domain.ReviewApproved, RequesterId: 7, ContactType: "phone", Version: 1,
		},
		{
			Title: "接单进行中", Description: "进行中", RewardCents: 300,
			Currency: domain.CurrencyCNY, PickupLocation: "A", DropoffLocation: "B",
			Deadline: now.Add(time.Hour), Status: domain.TaskAccepted,
			ReviewStatus: domain.ReviewApproved, RequesterId: 8, RunnerId: &runnerSeven,
			ContactType: "phone", Version: 1,
		},
		{
			Title: "接单已取件", Description: "已取件", RewardCents: 400,
			Currency: domain.CurrencyCNY, PickupLocation: "A", DropoffLocation: "B",
			Deadline: now.Add(time.Hour), Status: domain.TaskPickedUp,
			ReviewStatus: domain.ReviewApproved, RequesterId: 9, RunnerId: &runnerSeven,
			ContactType: "phone", Version: 1,
		},
		{
			Title: "无关任务", Description: "无关", RewardCents: 500,
			Currency: domain.CurrencyCNY, PickupLocation: "A", DropoffLocation: "B",
			Deadline: now.Add(time.Hour), Status: domain.TaskOpen,
			ReviewStatus: domain.ReviewApproved, RequesterId: 10, ContactType: "phone", Version: 1,
		},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name   string
		search domain.MineSearch
		want   int
	}{
		{name: "all", search: domain.MineSearch{Relation: domain.MineRelationAll}, want: 4},
		{
			name:   "published",
			search: domain.MineSearch{Relation: domain.MineRelationPublished},
			want:   2,
		},
		{
			name:   "accepted",
			search: domain.MineSearch{Relation: domain.MineRelationAccepted},
			want:   2,
		},
		{
			name: "published pending open",
			search: domain.MineSearch{
				Relation: domain.MineRelationPublished, Status: domain.TaskOpen,
				ReviewStatus: domain.ReviewPending,
			},
			want: 1,
		},
		{
			name: "accepted approved",
			search: domain.MineSearch{
				Relation: domain.MineRelationAccepted, ReviewStatus: domain.ReviewApproved,
			},
			want: 2,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, total, err := store.ListMine(context.Background(), 7, test.search, 1, 20)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != test.want || total != int64(test.want) {
				t.Fatalf("rows=%d total=%d, want %d", len(rows), total, test.want)
			}
		})
	}

	rows, total, err := store.ListMine(
		context.Background(),
		7,
		domain.MineSearch{Relation: domain.MineRelationAll},
		2,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || total != 4 || rows[0].ID != tasks[2].ID {
		t.Fatalf("paged rows=%+v total=%d", rows, total)
	}
}

func TestListMineReturnsDatabaseErrors(t *testing.T) {
	db := newErrandReviewTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err = sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err = NewStore(db).ListMine(
		context.Background(),
		7,
		domain.MineSearch{Relation: domain.MineRelationAll},
		1,
		20,
	)
	if err == nil {
		t.Fatal("ListMine() error = nil")
	}
}

func TestUpdateRejectsPendingReviewTask(t *testing.T) {
	db := newErrandReviewTestDB(t)
	task := createErrandReviewTask(t, db, domain.ReviewPending)
	_, err := NewStore(db).Update(
		context.Background(),
		task.ID,
		task.RequesterId,
		task.Version,
		domain.TaskInput{
			Title: "新标题", Description: "新说明", RewardCents: 600,
			PickupLocation: "新取件点", DropoffLocation: "新送达点",
			Deadline: task.Deadline.Add(time.Hour),
		},
		time.Now().UTC(),
	)
	if statusOf(err) != 409 {
		t.Fatalf("Update() error = %v, want status 409", err)
	}
}
