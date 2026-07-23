// Package domain defines comment entities and lifecycle rules.
package domain

import (
	"errors"
	"strings"
)

// Comment moderation statuses.
const (
	StatusPendingReview = "pending_review"
	StatusApproved      = "approved"
	StatusRejected      = "rejected"
	StatusWithdrawn     = "withdrawn"
)

// MaxDepth is the deepest accepted reply level.
const MaxDepth int64 = 8

// MaxContentLength is the maximum number of Unicode code points in a comment.
const MaxContentLength = 2000

// Comment viewer actions.
const (
	ActionEdit         = "edit"
	ActionWithdraw     = "withdraw"
	ActionSubmitReview = "submit_review"
	ActionReply        = "reply"
	ActionPin          = "pin_comment"
	ActionUnpin        = "unpin_comment"
)

// Comment validation errors.
var (
	ErrContentRequired = errors.New("评论内容不能为空")
	ErrContentTooLong  = errors.New("评论内容不能超过 2000 个字符")
	ErrDepthExceeded   = errors.New("评论回复最多支持 8 层")
	ErrInvalidState    = errors.New("当前评论状态不允许执行该操作")
)

// NormalizeContent validates and trims user-authored comment content.
func NormalizeContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	switch {
	case content == "":
		return "", ErrContentRequired
	case len([]rune(content)) > MaxContentLength:
		return "", ErrContentTooLong
	default:
		return content, nil
	}
}

// ReplyDepth returns the next nesting depth.
func ReplyDepth(parent *Comment) (int64, error) {
	if parent == nil {
		return 0, nil
	}
	depth := parent.Depth + 1
	if depth > MaxDepth {
		return 0, ErrDepthExceeded
	}
	return depth, nil
}

// VisibleTo reports whether a comment may be returned to one viewer.
func VisibleTo(comment *Comment, viewerID uint64, admin bool) bool {
	return comment != nil &&
		(comment.Status == StatusApproved || admin || (viewerID != 0 && comment.AuthorId == viewerID))
}

// CanEdit reports whether a comment may be edited.
func CanEdit(status string) bool {
	return status == StatusPendingReview || status == StatusApproved || status == StatusRejected
}

// CanWithdraw reports whether a comment may be withdrawn.
func CanWithdraw(status string) bool { return status != StatusWithdrawn }

// CanSubmitReview reports whether a rejected comment may be resubmitted.
func CanSubmitReview(status string) bool { return status == StatusRejected }

// CanReview reports whether an administrator may review the comment.
func CanReview(status string) bool { return status == StatusPendingReview }

// CanRevokeReview reports whether an administrator may revoke the decision.
func CanRevokeReview(status string) bool {
	return status == StatusApproved || status == StatusRejected
}

// AvailableActions derives actions without performing authorization itself.
func AvailableActions(comment *Comment, viewerID uint64, targetOwner, pinned bool) []string {
	if comment == nil {
		return []string{}
	}
	actions := make([]string, 0, 4)
	if viewerID != 0 && comment.AuthorId == viewerID {
		if CanEdit(comment.Status) {
			actions = append(actions, ActionEdit)
		}
		if CanWithdraw(comment.Status) {
			actions = append(actions, ActionWithdraw)
		}
		if CanSubmitReview(comment.Status) {
			actions = append(actions, ActionSubmitReview)
		}
	}
	if viewerID != 0 && comment.Status == StatusApproved {
		actions = append(actions, ActionReply)
	}
	if targetOwner && comment.ParentId == nil && comment.Status == StatusApproved {
		if pinned {
			actions = append(actions, ActionUnpin)
		} else {
			actions = append(actions, ActionPin)
		}
	}
	return actions
}
