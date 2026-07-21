// Package domain contains the handwritten notice-center rules.
package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Notice states, priorities, audience types and supported delivery channels.
const (
	StatusDraft      = "draft"
	StatusScheduled  = "scheduled"
	StatusPublishing = "publishing"
	StatusPublished  = "published"
	StatusRevoked    = "revoked"

	PriorityNormal    = "normal"
	PriorityImportant = "important"
	PriorityUrgent    = "urgent"

	AudienceAll  = "all"
	AudienceRole = "role"
	AudienceUser = "user"

	ChannelInApp = "in_app"
	ChannelPush  = "push"
)

var categoryPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// Audience is the original target declaration retained for audit.
type Audience struct {
	All     bool     `json:"all"`
	Roles   []string `json:"roles"`
	UserIDs []uint64 `json:"user_ids"`
}

// DraftInput is shared by create and draft update operations.
type DraftInput struct {
	Title      string     `json:"title"`
	Summary    string     `json:"summary"`
	Body       string     `json:"body"`
	Category   string     `json:"category"`
	Priority   string     `json:"priority"`
	ActionPath string     `json:"action_path"`
	Channels   []string   `json:"channels"`
	Audience   Audience   `json:"audience"`
	PublishAt  *time.Time `json:"publish_at,omitempty"`
}

// ValidateDraft validates content, channels and the audience declaration.
func ValidateDraft(in DraftInput) error {
	if n := len([]rune(strings.TrimSpace(in.Title))); n == 0 || n > 200 {
		return fmt.Errorf("标题长度必须为 1-200 个字符")
	}
	if len([]rune(in.Summary)) > 500 || len([]rune(in.Body)) == 0 || len([]rune(in.Body)) > 20000 {
		return fmt.Errorf("摘要或正文长度无效")
	}
	if !categoryPattern.MatchString(in.Category) {
		return fmt.Errorf("分类必须是小写 slug")
	}
	if in.Priority != PriorityNormal && in.Priority != PriorityImportant && in.Priority != PriorityUrgent {
		return fmt.Errorf("通知优先级无效")
	}
	if len([]rune(in.ActionPath)) > 512 || (in.ActionPath != "" && !strings.HasPrefix(in.ActionPath, "/")) {
		return fmt.Errorf("跳转路径无效")
	}
	channels := uniqueStrings(in.Channels)
	if len(channels) == 0 || !containsString(channels, ChannelInApp) {
		return fmt.Errorf("站内通知通道必选")
	}
	for _, channel := range channels {
		if channel != ChannelInApp && channel != ChannelPush {
			return fmt.Errorf("通知通道无效: %s", channel)
		}
	}
	if !in.Audience.All && len(in.Audience.Roles) == 0 && len(in.Audience.UserIDs) == 0 {
		return fmt.Errorf("通知受众不能为空")
	}
	return nil
}

// CanTransition reports whether a state transition is legal.
func CanTransition(from, to string) bool {
	allowed := map[string]map[string]bool{
		StatusDraft:      {StatusScheduled: true, StatusPublishing: true},
		StatusScheduled:  {StatusPublishing: true, StatusRevoked: true},
		StatusPublishing: {StatusPublished: true, StatusRevoked: true},
		StatusPublished:  {StatusRevoked: true},
	}
	return allowed[from][to]
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
