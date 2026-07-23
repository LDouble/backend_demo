package domain

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestValidateTaskInput(t *testing.T) {
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	valid := TaskInput{Title: "取快递", Description: "帮忙从菜鸟驿站取件", RewardCents: 500, PickupLocation: "菜鸟驿站", DropoffLocation: "3 号宿舍", Deadline: now.Add(time.Hour), Contact: ContactInput{Type: "phone", Value: "13800138000", Provided: true}}
	for _, tc := range []struct {
		name    string
		input   TaskInput
		wantErr bool
	}{
		{name: "valid", input: valid},
		{name: "past deadline", input: TaskInput{Title: valid.Title, Description: valid.Description, RewardCents: valid.RewardCents, PickupLocation: valid.PickupLocation, DropoffLocation: valid.DropoffLocation, Deadline: now}, wantErr: true},
		{name: "zero reward", input: TaskInput{Title: valid.Title, Description: valid.Description, PickupLocation: valid.PickupLocation, DropoffLocation: valid.DropoffLocation, Deadline: valid.Deadline}, wantErr: true},
		{name: "blank location", input: TaskInput{Title: valid.Title, Description: valid.Description, RewardCents: valid.RewardCents, DropoffLocation: valid.DropoffLocation, Deadline: valid.Deadline}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTaskInput(tc.input, now); (err != nil) != tc.wantErr {
				t.Fatalf("ValidateTaskInput() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateContactInput(t *testing.T) {
	if err := ValidateContactInput(ContactInput{Type: "qq", Value: "123456", Provided: true}, true); err != nil {
		t.Fatalf("ValidateContactInput() error = %v", err)
	}
	if err := ValidateContactInput(ContactInput{}, true); err == nil {
		t.Fatal("ValidateContactInput() error = nil, want missing contact error")
	}
	if err := ValidateContactInput(ContactInput{Type: "email", Value: "a", Provided: true}, false); err == nil {
		t.Fatal("ValidateContactInput() error = nil, want invalid type error")
	}
}

func TestCanTransition(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		want     bool
	}{
		{TaskOpen, TaskAccepted, true}, {TaskOpen, TaskCancelled, true}, {TaskAccepted, TaskPickedUp, true}, {TaskAccepted, TaskCancelled, true}, {TaskPickedUp, TaskDelivered, true}, {TaskDelivered, TaskCompleted, true}, {TaskOpen, TaskCompleted, false}, {TaskPickedUp, TaskCancelled, false},
	} {
		if got := CanTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("CanTransition(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestNormalizeMineSearch(t *testing.T) {
	for _, test := range []struct {
		name    string
		search  MineSearch
		want    MineSearch
		wantErr bool
	}{
		{
			name:   "defaults relation and trims filters",
			search: MineSearch{Status: " open ", ReviewStatus: " approved "},
			want: MineSearch{
				Relation: MineRelationAll, Status: TaskOpen, ReviewStatus: ReviewApproved,
			},
		},
		{
			name:   "published",
			search: MineSearch{Relation: MineRelationPublished},
			want:   MineSearch{Relation: MineRelationPublished},
		},
		{
			name:   "accepted",
			search: MineSearch{Relation: MineRelationAccepted},
			want:   MineSearch{Relation: MineRelationAccepted},
		},
		{
			name:    "invalid relation",
			search:  MineSearch{Relation: "other"},
			wantErr: true,
		},
		{
			name:    "invalid task status",
			search:  MineSearch{Status: "unknown"},
			wantErr: true,
		},
		{
			name:    "invalid review status",
			search:  MineSearch{ReviewStatus: "unknown"},
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeMineSearch(test.search)
			if (err != nil) != test.wantErr {
				t.Fatalf("NormalizeMineSearch() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("NormalizeMineSearch() = %+v, want %+v", got, test.want)
			}
		})
	}

	for _, status := range []string{
		TaskOpen, TaskAccepted, TaskPickedUp, TaskDelivered, TaskCompleted, TaskCancelled,
	} {
		if _, err := NormalizeMineSearch(MineSearch{Status: status}); err != nil {
			t.Errorf("NormalizeMineSearch() status %q error = %v", status, err)
		}
	}
	for _, status := range []string{ReviewDraft, ReviewPending, ReviewApproved, ReviewRejected} {
		if _, err := NormalizeMineSearch(MineSearch{ReviewStatus: status}); err != nil {
			t.Errorf("NormalizeMineSearch() review status %q error = %v", status, err)
		}
	}
}

func TestViewerRelation(t *testing.T) {
	runnerID := uint64(8)
	task := &Task{RequesterId: 7, RunnerId: &runnerID}
	for _, test := range []struct {
		name     string
		viewerID uint64
		want     string
	}{
		{name: "anonymous", want: ViewerRelationNone},
		{name: "publisher", viewerID: 7, want: ViewerRelationPublisher},
		{name: "runner", viewerID: 8, want: ViewerRelationRunner},
		{name: "unrelated", viewerID: 9, want: ViewerRelationNone},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ViewerRelation(task, test.viewerID); got != test.want {
				t.Fatalf("ViewerRelation() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAvailableActions(t *testing.T) {
	now := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)
	runnerID := uint64(8)
	task := func(status, reviewStatus string) *Task {
		return &Task{
			Status: status, ReviewStatus: reviewStatus, RequesterId: 7,
			RunnerId: &runnerID, Deadline: now.Add(time.Hour),
		}
	}
	for _, test := range []struct {
		name     string
		task     *Task
		viewerID uint64
		want     []string
	}{
		{
			name: "publisher pending review",
			task: task(TaskOpen, ReviewPending), viewerID: 7,
			want: []string{ActionCancel},
		},
		{
			name: "publisher draft",
			task: task(TaskOpen, ReviewDraft), viewerID: 7,
			want: []string{ActionEdit, ActionSubmitReview, ActionCancel},
		},
		{
			name: "publisher rejected",
			task: task(TaskOpen, ReviewRejected), viewerID: 7,
			want: []string{ActionEdit, ActionSubmitReview, ActionCancel},
		},
		{
			name: "publisher approved",
			task: task(TaskOpen, ReviewApproved), viewerID: 7,
			want: []string{ActionEdit, ActionCancel},
		},
		{
			name: "member can accept",
			task: task(TaskOpen, ReviewApproved), viewerID: 9,
			want: []string{ActionAccept},
		},
		{name: "anonymous cannot accept", task: task(TaskOpen, ReviewApproved)},
		{name: "unapproved cannot accept", task: task(TaskOpen, ReviewRejected), viewerID: 9},
		{
			name:     "expired cannot accept",
			task:     task(TaskOpen, ReviewApproved),
			viewerID: 9,
		},
		{
			name: "publisher can cancel accepted task",
			task: task(TaskAccepted, ReviewApproved), viewerID: 7,
			want: []string{ActionCancel},
		},
		{
			name: "runner can pickup or cancel accepted task",
			task: task(TaskAccepted, ReviewApproved), viewerID: 8,
			want: []string{ActionPickup, ActionCancel},
		},
		{name: "unrelated cannot operate accepted task", task: task(TaskAccepted, ReviewApproved), viewerID: 9},
		{
			name: "runner can deliver picked up task",
			task: task(TaskPickedUp, ReviewApproved), viewerID: 8,
			want: []string{ActionDeliver},
		},
		{name: "publisher cannot cancel picked up task", task: task(TaskPickedUp, ReviewApproved), viewerID: 7},
		{
			name: "publisher can complete delivered task",
			task: task(TaskDelivered, ReviewApproved), viewerID: 7,
			want: []string{ActionComplete},
		},
		{name: "runner cannot complete delivered task", task: task(TaskDelivered, ReviewApproved), viewerID: 8},
		{name: "completed task", task: task(TaskCompleted, ReviewApproved), viewerID: 7},
		{name: "cancelled task", task: task(TaskCancelled, ReviewApproved), viewerID: 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "expired cannot accept" {
				test.task.Deadline = now
			}
			got := AvailableActions(test.task, test.viewerID, now)
			if !slices.Equal(got, test.want) {
				t.Fatalf("AvailableActions() = %v, want %v", got, test.want)
			}
			if got == nil {
				t.Fatal("AvailableActions() returned nil slice")
			}
		})
	}
}

func TestCanEdit(t *testing.T) {
	for _, test := range []struct {
		status, reviewStatus string
		want                 bool
	}{
		{TaskOpen, ReviewDraft, true},
		{TaskOpen, ReviewRejected, true},
		{TaskOpen, ReviewApproved, true},
		{TaskOpen, ReviewPending, false},
		{TaskAccepted, ReviewApproved, false},
		{TaskCancelled, ReviewDraft, false},
	} {
		if got := CanEdit(test.status, test.reviewStatus); got != test.want {
			t.Errorf("CanEdit(%q, %q) = %v, want %v", test.status, test.reviewStatus, got, test.want)
		}
	}
}

func TestValidateTaskContentBranches(t *testing.T) {
	now := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	valid := TaskInput{
		Title: "取快递", Description: "送到宿舍", RewardCents: 500,
		PickupLocation: "快递站", DropoffLocation: "宿舍",
		Deadline: now.Add(time.Hour),
		Contact:  ContactInput{Type: "wechat", Value: "campus-user", Provided: true},
	}
	for _, test := range []struct {
		name   string
		mutate func(*TaskInput)
	}{
		{name: "blank title", mutate: func(input *TaskInput) { input.Title = " " }},
		{name: "long title", mutate: func(input *TaskInput) { input.Title = strings.Repeat("题", 201) }},
		{name: "blank description", mutate: func(input *TaskInput) { input.Description = "" }},
		{name: "long description", mutate: func(input *TaskInput) { input.Description = strings.Repeat("说", 20_001) }},
		{name: "blank pickup", mutate: func(input *TaskInput) { input.PickupLocation = "" }},
		{name: "long pickup", mutate: func(input *TaskInput) { input.PickupLocation = strings.Repeat("取", 501) }},
		{name: "blank dropoff", mutate: func(input *TaskInput) { input.DropoffLocation = "" }},
		{name: "long dropoff", mutate: func(input *TaskInput) { input.DropoffLocation = strings.Repeat("送", 501) }},
		{name: "negative reward", mutate: func(input *TaskInput) { input.RewardCents = -1 }},
		{name: "deadline now", mutate: func(input *TaskInput) { input.Deadline = now }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := valid
			test.mutate(&input)
			if err := ValidateTaskInput(input, now); err == nil {
				t.Fatal("ValidateTaskInput() error = nil")
			}
		})
	}
}

func TestValidateContactInputBranches(t *testing.T) {
	if err := ValidateContactInput(ContactInput{}, false); err != nil {
		t.Fatalf("optional omitted contact error = %v", err)
	}
	for _, input := range []ContactInput{
		{Type: "email", Value: "user@example.com", Provided: true},
		{Type: "phone", Value: " ", Provided: true},
		{Type: "phone", Value: strings.Repeat("1", 129), Provided: true},
	} {
		if err := ValidateContactInput(input, false); err == nil {
			t.Fatalf("ValidateContactInput(%+v) error = nil", input)
		}
	}
	for _, contactType := range []string{"phone", "wechat", "qq"} {
		if err := ValidateContactInput(
			ContactInput{Type: contactType, Value: "contact", Provided: true},
			true,
		); err != nil {
			t.Errorf("ValidateContactInput() type %q error = %v", contactType, err)
		}
	}
}
