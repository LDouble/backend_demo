package domain

import (
	"errors"
	"strings"
)

const (
	StatusPendingReview = "pending_review"
	StatusApproved      = "approved"
	StatusRejected      = "rejected"
	StatusWithdrawn     = "withdrawn"

	MaxDepth         int64 = 8
	MaxContentLength       = 2000
)

const (
	ActionEdit         = "edit"
	ActionWithdraw     = "withdraw"
	ActionSubmitReview = "submit_review"
	ActionReply        = "reply"
	ActionPin          = "pin_comment"
	ActionUnpin        = "unpin_comment"
)

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

func CanEdit(status string) bool {
	return status == StatusPendingReview || status == StatusApproved || status == StatusRejected
}

func CanWithdraw(status string) bool { return status != StatusWithdrawn }

func CanSubmitReview(status string) bool { return status == StatusRejected }

func CanReview(status string) bool { return status == StatusPendingReview }

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
