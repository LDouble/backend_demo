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
)

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

// CanTransition reports whether a task state transition is legal.
func CanTransition(from, to string) bool {
	return map[string]map[string]bool{
		TaskOpen:      {TaskAccepted: true, TaskCancelled: true},
		TaskAccepted:  {TaskPickedUp: true, TaskCancelled: true},
		TaskPickedUp:  {TaskDelivered: true},
		TaskDelivered: {TaskCompleted: true},
	}[from][to]
}
