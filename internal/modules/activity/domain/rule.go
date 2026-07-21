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

// CanCancel reports whether an activity may be cancelled.
func CanCancel(status string) bool {
	return status == ActivityStatusPublished
}

// CanFinish reports whether an activity may be finished.
func CanFinish(status string) bool {
	return status == ActivityStatusPublished
}

// IsPubliclyVisible reports whether an activity is visible on user endpoints.
func IsPubliclyVisible(status, reviewStatus string) bool {
	return status == ActivityStatusPublished && reviewStatus == ReviewStatusApproved
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
