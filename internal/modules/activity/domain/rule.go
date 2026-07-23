// Package domain contains activity lifecycle and registration rules.
package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	// ActivityStatusDraft is the editable state before publication.
	ActivityStatusDraft = "draft"
	// ActivityStatusPublished is the public state visible to users.
	ActivityStatusPublished = "published"
	// ActivityStatusCancelled marks an activity cancelled by its owner.
	ActivityStatusCancelled = "cancelled"
	// ActivityStatusFinished marks an activity finished by its owner.
	ActivityStatusFinished = "finished"

	// ReviewStatusDraft is the initial review state.
	ReviewStatusDraft = "draft"
	// ReviewStatusPendingReview means the activity is waiting for moderation.
	ReviewStatusPendingReview = "pending_review"
	// ReviewStatusApproved means the activity passed moderation.
	ReviewStatusApproved = "approved"
	// ReviewStatusRejected means the activity was rejected by moderation.
	ReviewStatusRejected = "rejected"

	// RegistrationStatusActive marks an active registration.
	RegistrationStatusActive = "active"
	// RegistrationStatusCancelled marks a cancelled registration.
	RegistrationStatusCancelled = "cancelled"

	// ViewerRelationPublisher means the viewer created the activity.
	ViewerRelationPublisher = "publisher"
	// ViewerRelationParticipant means the viewer has an active registration.
	ViewerRelationParticipant = "participant"
	// ViewerRelationNone means the viewer has no active activity relationship.
	ViewerRelationNone = "none"

	// ActionEdit updates an editable activity.
	ActionEdit = "edit"
	// ActionSubmitReview submits an activity for moderation.
	ActionSubmitReview = "submit_review"
	// ActionCancel cancels an activity as its publisher.
	ActionCancel = "cancel"
	// ActionRegister creates an activity registration.
	ActionRegister = "register"
	// ActionCancelRegistration cancels the viewer's active registration.
	ActionCancelRegistration = "cancel_registration"
)

// ActivityInput contains the user-controlled mutable activity content.
type ActivityInput struct {
	Title         string
	Summary       string
	Body          string
	Location      string
	SignupStartAt time.Time
	SignupEndAt   time.Time
	StartAt       time.Time
	EndAt         time.Time
	Capacity      int64
	Contact       ContactInput
}

// ContactInput carries a publisher-supplied contact value.
type ContactInput struct {
	Type     string
	Value    string
	Provided bool
}

// ContactDetails is the access-controlled contact payload for transport.
type ContactDetails struct {
	Type  string
	Value string
}

// AdminSearch contains search filters for the admin list.
type AdminSearch struct {
	Keyword      string
	Status       string
	ReviewStatus string
	StartDate    *time.Time
}

// PublicSearch contains search filters for the public list.
type PublicSearch struct {
	Keyword   string
	StartDate *time.Time
}

// MyRegistration combines a registration row with its activity.
type MyRegistration struct {
	Activity     Activity
	Registration ActivityRegistration
}

// ValidateActivityInput validates mutable activity input before persistence.
func ValidateActivityInput(input ActivityInput, requiredContact bool, now time.Time) error {
	if length := len([]rune(strings.TrimSpace(input.Title))); length == 0 || length > 200 {
		return fmt.Errorf("活动标题长度必须为 1-200 个字符")
	}
	if length := len([]rune(strings.TrimSpace(input.Summary))); length == 0 || length > 500 {
		return fmt.Errorf("活动摘要长度必须为 1-500 个字符")
	}
	if length := len([]rune(strings.TrimSpace(input.Body))); length == 0 || length > 20_000 {
		return fmt.Errorf("活动正文长度必须为 1-20000 个字符")
	}
	if length := len([]rune(strings.TrimSpace(input.Location))); length == 0 || length > 500 {
		return fmt.Errorf("活动地点长度必须为 1-500 个字符")
	}
	if input.Capacity < 1 {
		return fmt.Errorf("活动容量必须大于 0")
	}
	if !input.SignupStartAt.Before(input.SignupEndAt) {
		return fmt.Errorf("报名开始时间必须早于报名结束时间")
	}
	if !input.SignupEndAt.Before(input.StartAt) {
		return fmt.Errorf("报名结束时间必须早于活动开始时间")
	}
	if !input.StartAt.Before(input.EndAt) {
		return fmt.Errorf("活动开始时间必须早于结束时间")
	}
	if !input.EndAt.After(now) {
		return fmt.Errorf("活动结束时间必须晚于当前时间")
	}
	return ValidateContactInput(input.Contact, requiredContact)
}

// ValidateContactInput validates a contact payload supplied by the publisher.
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
	if length := len([]rune(strings.TrimSpace(input.Value))); length == 0 || length > 128 {
		return fmt.Errorf("联系方式长度必须为 1-128 个字符")
	}
	return nil
}

// CanEdit reports whether an activity may be edited.
func CanEdit(status, reviewStatus string) bool {
	return status == ActivityStatusDraft && (reviewStatus == ReviewStatusDraft || reviewStatus == ReviewStatusRejected)
}

// CanSubmitReview reports whether an activity may be submitted for review.
func CanSubmitReview(status, reviewStatus string) bool {
	return status == ActivityStatusDraft && reviewStatus == ReviewStatusDraft
}

// CanApprove reports whether an activity may be approved.
func CanApprove(status, reviewStatus string) bool {
	return status == ActivityStatusDraft && reviewStatus == ReviewStatusPendingReview
}

// CanReject reports whether an activity may be rejected.
func CanReject(status, reviewStatus string) bool {
	return status == ActivityStatusDraft && reviewStatus == ReviewStatusPendingReview
}

// CanPublish reports whether an activity may be published.
func CanPublish(status, reviewStatus string) bool {
	return status == ActivityStatusDraft && reviewStatus == ReviewStatusApproved
}

// CanCancel reports whether a draft or published activity may be cancelled.
func CanCancel(status string) bool {
	return status == ActivityStatusDraft || status == ActivityStatusPublished
}

// CanFinish reports whether an activity may be finished.
func CanFinish(status string) bool {
	return status == ActivityStatusPublished
}

// IsPubliclyVisible reports whether an activity is visible on user endpoints.
func IsPubliclyVisible(status, reviewStatus string) bool {
	return status == ActivityStatusPublished && reviewStatus == ReviewStatusApproved
}

// MaxReviewCommentLength matches the VARCHAR(500) column for review_comment.
// MySQL STRICT_ALL_TABLES raises Data too long for column without this guard;
// the AppError code mirrors the GitHub review-comment convention so the rule
// is consistent with errand/marketplace rejection envelopes.
const MaxReviewCommentLength = 500

// VisibleToViewer decides whether `GetPublic` should return the activity to a
// non-anonymous viewer. The activity is reachable to the public when it is
// currently published+approved; it is also reachable to its owner or to anyone
// with an active registration even after the activity enters a terminal state
// (cancelled / finished). Other viewers see 404.
//
// ownerID == 0 means "no owner relation to evaluate", e.g. requests executed on
// behalf of platform staff through a service account.
func VisibleToViewer(status, reviewStatus string, ownerID uint64, viewerID uint64, viewerHasActiveRegistration bool) bool {
	if IsPubliclyVisible(status, reviewStatus) {
		return true
	}
	if viewerID == 0 {
		return false
	}
	if ownerID != 0 && ownerID == viewerID {
		return true
	}
	if viewerHasActiveRegistration {
		return true
	}
	return false
}

// ValidateCapacityUpdate enforces A.3: the new capacity must be at least 1 and
// must not be lower than the number of registrations already accepted,
// otherwise registered_count would exceed capacity on a path that has no
// recovery code.
func ValidateCapacityUpdate(newCapacity, currentRegistered int64) error {
	if newCapacity < 1 {
		return fmt.Errorf("活动容量必须大于 0")
	}
	if newCapacity < currentRegistered {
		return fmt.Errorf("活动容量 %d 不能小于已报名人数 %d", newCapacity, currentRegistered)
	}
	return nil
}

// ValidateReviewComment enforces the size limit (≤ MaxReviewCommentLength) for
// reviewer comments. Empty input is rejected only via explicit validation in
// the caller — approve may legitimately pass "" to clear a previous comment.
func ValidateReviewComment(s string) error {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	if length := len([]rune(trimmed)); length > MaxReviewCommentLength {
		return fmt.Errorf("审核意见长度 %d 不能超过 %d", length, MaxReviewCommentLength)
	}
	return nil
}

// RegistrationAllowed validates whether a new registration may be created now.
func RegistrationAllowed(activity *Activity, now time.Time) error {
	if !IsPubliclyVisible(activity.Status, activity.ReviewStatus) {
		return fmt.Errorf("活动当前不可报名")
	}
	if now.Before(activity.SignupStartAt) || now.After(activity.SignupEndAt) {
		return fmt.Errorf("当前不在报名时间内")
	}
	if activity.RegisteredCount >= activity.Capacity {
		return fmt.Errorf("活动报名人数已满")
	}
	return nil
}

// CancellationAllowed validates whether a registration may be cancelled now.
func CancellationAllowed(registration *ActivityRegistration, activity *Activity, now time.Time) error {
	if registration.Status != RegistrationStatusActive {
		return fmt.Errorf("当前没有有效报名记录")
	}
	if !now.Before(activity.StartAt) {
		return fmt.Errorf("活动开始后不可取消报名")
	}
	return nil
}

// ViewerRelation returns how the viewer participates in an activity.
func ViewerRelation(activity *Activity, viewerID uint64, registered bool) string {
	if viewerID == 0 {
		return ViewerRelationNone
	}
	if activity.CreatedBy == viewerID {
		return ViewerRelationPublisher
	}
	if registered {
		return ViewerRelationParticipant
	}
	return ViewerRelationNone
}

// AvailableActions returns member actions allowed by activity state and relation.
func AvailableActions(activity *Activity, viewerID uint64, registered bool, now time.Time) []string {
	actions := []string{}
	relation := ViewerRelation(activity, viewerID, registered)
	if relation == ViewerRelationPublisher {
		if CanEdit(activity.Status, activity.ReviewStatus) {
			actions = append(actions, ActionEdit)
		}
		if CanSubmitReview(activity.Status, activity.ReviewStatus) {
			actions = append(actions, ActionSubmitReview)
		}
		if CanCancel(activity.Status) {
			actions = append(actions, ActionCancel)
		}
		return actions
	}
	if relation == ViewerRelationParticipant {
		if now.Before(activity.StartAt) {
			return append(actions, ActionCancelRegistration)
		}
		return actions
	}
	if viewerID != 0 && RegistrationAllowed(activity, now) == nil {
		return append(actions, ActionRegister)
	}
	return actions
}
