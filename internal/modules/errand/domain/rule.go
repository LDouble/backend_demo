// Package domain contains errand task lifecycle rules.
package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	// CurrencyCNY is the only supported errand currency.
	CurrencyCNY = "CNY"
	// TaskOpen and the following constants define the task lifecycle.
	TaskOpen = "open"
	// TaskAccepted means a runner has accepted the task.
	TaskAccepted = "accepted"
	// TaskPickedUp means the item has been picked up.
	TaskPickedUp = "picked_up"
	// TaskDelivered means the item has been delivered.
	TaskDelivered = "delivered"
	// TaskCompleted means the requester confirmed completion.
	TaskCompleted = "completed"
	// TaskCancelled means the workflow has been cancelled.
	TaskCancelled = "cancelled"
	// ReviewPending means a newly published task is waiting for moderation.
	ReviewPending = "pending_review"
	// ReviewApproved means a task may appear in public listings.
	ReviewApproved = "approved"
	// ReviewRejected means the requester must edit and resubmit the task.
	ReviewRejected = "rejected"
	// ReviewDraft means a rejected task has been edited but not resubmitted.
	ReviewDraft = "draft"
	// MineRelationAll includes tasks published or accepted by the viewer.
	MineRelationAll = "all"
	// MineRelationPublished includes tasks published by the viewer.
	MineRelationPublished = "published"
	// MineRelationAccepted includes tasks accepted by the viewer.
	MineRelationAccepted = "accepted"
	// ViewerRelationPublisher identifies the task publisher.
	ViewerRelationPublisher = "publisher"
	// ViewerRelationRunner identifies the active task runner.
	ViewerRelationRunner = "runner"
	// ViewerRelationNone identifies a viewer unrelated to the task.
	ViewerRelationNone = "none"
	// ActionAccept allows an unrelated member to accept an approved open task.
	ActionAccept = "accept"
	// ActionEdit allows the publisher to change editable task content.
	ActionEdit = "edit"
	// ActionSubmitReview allows the publisher to submit a draft or rejected task.
	ActionSubmitReview = "submit_review"
	// ActionPickup allows the runner to confirm pickup.
	ActionPickup = "pickup"
	// ActionDeliver allows the runner to confirm delivery.
	ActionDeliver = "deliver"
	// ActionComplete allows the publisher to confirm completion.
	ActionComplete = "complete"
	// ActionCancel allows an eligible task participant to cancel.
	ActionCancel = "cancel"
)

// AdminSearch contains moderation-list filters.
type AdminSearch struct {
	Status       string
	ReviewStatus string
	Keyword      string
}

// MineSearch contains owner and runner list filters.
type MineSearch struct {
	Relation     string
	Status       string
	ReviewStatus string
}

// TaskInput contains the user-controlled mutable task content.
type TaskInput struct {
	Title, Description, PickupLocation, DropoffLocation string
	RewardCents                                         int64
	Deadline                                            time.Time
	Contact                                             ContactInput
}

// ContactInput is a publisher-supplied contact method. Provided distinguishes an
// omitted update from an invalid empty contact.
type ContactInput struct {
	Type     string
	Value    string
	Provided bool
}

// ContactDetails is a transient, access-controlled contact value for transport mapping.
type ContactDetails struct {
	Type  string
	Value string
}

// ValidateTaskInput validates a new task before persistence.
func ValidateTaskInput(input TaskInput, now time.Time) error {
	if err := validateTaskContent(input, now); err != nil {
		return err
	}
	return ValidateContactInput(input.Contact, true)
}

// ValidateTaskUpdateInput validates task content and an optional contact update.
func ValidateTaskUpdateInput(input TaskInput, now time.Time) error {
	if err := validateTaskContent(input, now); err != nil {
		return err
	}
	return ValidateContactInput(input.Contact, false)
}

func validateTaskContent(input TaskInput, now time.Time) error {
	if n := len([]rune(strings.TrimSpace(input.Title))); n == 0 || n > 200 {
		return fmt.Errorf("任务标题长度必须为 1-200 个字符")
	}
	if n := len([]rune(strings.TrimSpace(input.Description))); n == 0 || n > 20_000 {
		return fmt.Errorf("任务说明长度必须为 1-20000 个字符")
	}
	if n := len([]rune(strings.TrimSpace(input.PickupLocation))); n == 0 || n > 500 {
		return fmt.Errorf("取件地点长度必须为 1-500 个字符")
	}
	if n := len([]rune(strings.TrimSpace(input.DropoffLocation))); n == 0 || n > 500 {
		return fmt.Errorf("送达地点长度必须为 1-500 个字符")
	}
	if input.RewardCents <= 0 {
		return fmt.Errorf("跑腿报酬必须大于 0 分")
	}
	if !input.Deadline.After(now) {
		return fmt.Errorf("截止时间必须晚于当前时间")
	}
	return nil
}

// ValidateContactInput validates a contact supplied by the requester.
func ValidateContactInput(input ContactInput, required bool) error {
	if !input.Provided {
		if required {
			return fmt.Errorf("联系方式不能为空")
		}
		return nil
	}
	typeValue := strings.TrimSpace(input.Type)
	if typeValue != "phone" && typeValue != "wechat" && typeValue != "qq" {
		return fmt.Errorf("联系方式类型必须为 phone、wechat 或 qq")
	}
	if n := len([]rune(strings.TrimSpace(input.Value))); n == 0 || n > 128 {
		return fmt.Errorf("联系方式长度必须为 1-128 个字符")
	}
	return nil
}

// NormalizeMineSearch trims and validates filters from the authenticated list endpoint.
func NormalizeMineSearch(search MineSearch) (MineSearch, error) {
	search.Relation = strings.TrimSpace(search.Relation)
	search.Status = strings.TrimSpace(search.Status)
	search.ReviewStatus = strings.TrimSpace(search.ReviewStatus)
	if search.Relation == "" {
		search.Relation = MineRelationAll
	}
	if !validMineRelation(search.Relation) {
		return MineSearch{}, fmt.Errorf("我的跑腿关系筛选无效")
	}
	if search.Status != "" && !validTaskStatus(search.Status) {
		return MineSearch{}, fmt.Errorf("跑腿任务状态筛选无效")
	}
	if search.ReviewStatus != "" && !validReviewStatus(search.ReviewStatus) {
		return MineSearch{}, fmt.Errorf("跑腿审核状态筛选无效")
	}
	return search, nil
}

// ViewerRelation returns how the viewer participates in a task.
func ViewerRelation(task *Task, viewerID uint64) string {
	if viewerID == 0 {
		return ViewerRelationNone
	}
	if task.RequesterId == viewerID {
		return ViewerRelationPublisher
	}
	if task.RunnerId != nil && *task.RunnerId == viewerID {
		return ViewerRelationRunner
	}
	return ViewerRelationNone
}

// AvailableActions returns lifecycle actions allowed by task state and viewer relation.
func AvailableActions(task *Task, viewerID uint64, now time.Time) []string {
	actions := []string{}
	relation := ViewerRelation(task, viewerID)
	switch task.Status {
	case TaskOpen:
		if relation == ViewerRelationPublisher {
			if CanEdit(task.Status, task.ReviewStatus) {
				actions = append(actions, ActionEdit)
			}
			if task.ReviewStatus == ReviewDraft || task.ReviewStatus == ReviewRejected {
				actions = append(actions, ActionSubmitReview)
			}
			return append(actions, ActionCancel)
		}
		canAccept := relation == ViewerRelationNone &&
			viewerID != 0 &&
			task.ReviewStatus == ReviewApproved &&
			task.Deadline.After(now)
		if canAccept {
			return append(actions, ActionAccept)
		}
	case TaskAccepted:
		if relation == ViewerRelationPublisher {
			return append(actions, ActionCancel)
		}
		if relation == ViewerRelationRunner {
			return append(actions, ActionPickup, ActionCancel)
		}
	case TaskPickedUp:
		if relation == ViewerRelationRunner {
			return append(actions, ActionDeliver)
		}
	case TaskDelivered:
		if relation == ViewerRelationPublisher {
			return append(actions, ActionComplete)
		}
	}
	return actions
}

// CanEdit reports whether publisher-controlled content may be changed.
func CanEdit(status, reviewStatus string) bool {
	if status != TaskOpen {
		return false
	}
	switch reviewStatus {
	case ReviewDraft, ReviewRejected, ReviewApproved:
		return true
	default:
		return false
	}
}

// CanTransition reports whether a task state transition is legal.
func CanTransition(from, to string) bool {
	return map[string]map[string]bool{
		TaskOpen:      {TaskAccepted: true, TaskCancelled: true},
		TaskAccepted:  {TaskPickedUp: true, TaskCancelled: true},
		TaskPickedUp:  {TaskDelivered: true},
		TaskDelivered: {TaskCompleted: true},
	}[from][to]
}

func validMineRelation(relation string) bool {
	switch relation {
	case MineRelationAll, MineRelationPublished, MineRelationAccepted:
		return true
	default:
		return false
	}
}

func validTaskStatus(status string) bool {
	switch status {
	case TaskOpen, TaskAccepted, TaskPickedUp, TaskDelivered, TaskCompleted, TaskCancelled:
		return true
	default:
		return false
	}
}

func validReviewStatus(status string) bool {
	switch status {
	case ReviewDraft, ReviewPending, ReviewApproved, ReviewRejected:
		return true
	default:
		return false
	}
}
