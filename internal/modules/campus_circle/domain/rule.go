// Package domain contains the campus-circle entities and business rules.
package domain

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// SectionStatusActive accepts new posts when its parent is active too.
	SectionStatusActive = "active"
	// SectionStatusArchived preserves historical content but rejects new posts.
	SectionStatusArchived = "archived"

	// PostStatusPendingReview is visible only to its author and administrators.
	PostStatusPendingReview = "pending_review"
	// PostStatusApproved is publicly visible.
	PostStatusApproved = "approved"
	// PostStatusRejected is visible only to its author and administrators.
	PostStatusRejected = "rejected"
	// PostStatusWithdrawn is visible only to its author and administrators.
	PostStatusWithdrawn = "withdrawn"

	// ViewerRelationAnonymous identifies a viewer without an authenticated account.
	ViewerRelationAnonymous = "anonymous"
	// ViewerRelationOwner identifies the post author.
	ViewerRelationOwner = "owner"
	// ViewerRelationOther identifies an authenticated non-author.
	ViewerRelationOther = "other"
	// ViewerRelationAdmin identifies an administrator.
	ViewerRelationAdmin = "admin"

	// ActionEdit allows the author to edit a post.
	ActionEdit = "edit"
	// ActionWithdraw allows the author to withdraw a post.
	ActionWithdraw = "withdraw"
	// ActionSubmitReview allows the author to resubmit a rejected post.
	ActionSubmitReview = "submit_review"
	// ActionLike allows another verified user to like a post.
	ActionLike = "like"
	// ActionUnlike allows another verified user to remove their like.
	ActionUnlike = "unlike"
	// ActionComment allows another verified user to comment on a post.
	ActionComment = "comment"
	// ActionVerify directs an unverified user to complete academic verification.
	ActionVerify = "verify_academic"
)

var (
	errInvalidSection = errors.New("校园圈子模块参数无效")
	errInvalidPost    = errors.New("校园圈帖子参数无效")
	slugPattern       = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

// SectionInput contains administrator-controlled section properties.
// Slug is used only when creating a section and remains immutable afterwards.
type SectionInput struct {
	ParentID    *uint64
	Slug        string
	Name        string
	Description string
	IconURL     string
	CoverURL    string
	SortOrder   int64
}

// PostInput contains user-controlled post content.
type PostInput struct {
	SectionID uint64
	Title     string
	Content   string
	ImageURLs []string
}

// NormalizeSectionInput trims textual section fields without changing identity.
func NormalizeSectionInput(input SectionInput) SectionInput {
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.IconURL = strings.TrimSpace(input.IconURL)
	input.CoverURL = strings.TrimSpace(input.CoverURL)
	return input
}

// ValidateSectionInput validates a create or update payload.
func ValidateSectionInput(input SectionInput, creating bool) error {
	input = NormalizeSectionInput(input)
	if input.ParentID != nil && *input.ParentID == 0 {
		return errInvalidSection
	}
	if creating && !slugPattern.MatchString(input.Slug) {
		return errInvalidSection
	}
	if !creating && (input.Slug != "" || input.ParentID != nil) {
		return errInvalidSection
	}
	if input.Name == "" || runeLengthAbove(input.Name, 64) ||
		runeLengthAbove(input.Description, 500) ||
		!validOptionalURL(input.IconURL) ||
		!validOptionalURL(input.CoverURL) {
		return errInvalidSection
	}
	return nil
}

// ValidateSectionParent enforces the fixed two-level hierarchy. A nil parent
// creates a root; a non-nil parent must itself be an active root.
func ValidateSectionParent(sectionID uint64, requestedParentID *uint64, parent *CampusCircleSection) error {
	if requestedParentID == nil {
		if parent != nil {
			return errInvalidSection
		}
		return nil
	}
	if *requestedParentID == 0 || *requestedParentID == sectionID || parent == nil ||
		parent.ID != *requestedParentID || parent.ParentId != nil ||
		parent.Status != SectionStatusActive {
		return errInvalidSection
	}
	return nil
}

// CanAcceptPosts reports whether a child section and its root are both active.
func CanAcceptPosts(section, parent *CampusCircleSection) bool {
	return section != nil &&
		section.ParentId != nil &&
		section.Status == SectionStatusActive &&
		parent != nil &&
		parent.ID == *section.ParentId &&
		parent.ParentId == nil &&
		parent.Status == SectionStatusActive
}

// CanArchiveSection reports whether the section can transition to archived.
func CanArchiveSection(status string) bool { return status == SectionStatusActive }

// CanActivateSection reports whether the section can transition to active.
func CanActivateSection(status string) bool { return status == SectionStatusArchived }

// NormalizePostInput trims content and returns a fresh image slice.
func NormalizePostInput(input PostInput) PostInput {
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	images := make([]string, 0, len(input.ImageURLs))
	for _, imageURL := range input.ImageURLs {
		images = append(images, strings.TrimSpace(imageURL))
	}
	input.ImageURLs = images
	return input
}

// ValidatePostInput enforces the campus-circle text and image limits.
func ValidatePostInput(input PostInput) error {
	input = NormalizePostInput(input)
	if input.SectionID == 0 || runeLengthAbove(input.Title, 100) ||
		runeLengthAbove(input.Content, 5000) || len(input.ImageURLs) > 9 {
		return errInvalidPost
	}
	for _, imageURL := range input.ImageURLs {
		if imageURL == "" || !validURL(imageURL) {
			return errInvalidPost
		}
	}
	if input.Title == "" && input.Content == "" && len(input.ImageURLs) == 0 {
		return errInvalidPost
	}
	return nil
}

// VisibleTo reports whether a post can be returned to a viewer.
func VisibleTo(post *CampusCirclePost, viewerID uint64, admin bool) bool {
	if post == nil {
		return false
	}
	return admin || post.Status == PostStatusApproved ||
		(viewerID != 0 && post.AuthorId == viewerID)
}

// ViewerRelation returns the viewer's stable relationship to a post.
func ViewerRelation(post *CampusCirclePost, viewerID uint64, admin bool) string {
	if admin {
		return ViewerRelationAdmin
	}
	if post != nil && viewerID != 0 && post.AuthorId == viewerID {
		return ViewerRelationOwner
	}
	if viewerID == 0 {
		return ViewerRelationAnonymous
	}
	return ViewerRelationOther
}

// AvailableActions derives actions before the transport applies the academic
// verification gate. Administrative operations use separate endpoints.
func AvailableActions(post *CampusCirclePost, viewerID uint64, liked bool, admin bool) []string {
	if post == nil || admin {
		return []string{}
	}
	if viewerID != 0 && post.AuthorId == viewerID {
		actions := make([]string, 0, 3)
		if CanEditPost(post.Status) {
			actions = append(actions, ActionEdit)
		}
		if CanWithdrawPost(post.Status) {
			actions = append(actions, ActionWithdraw)
		}
		if CanSubmitPostReview(post.Status) {
			actions = append(actions, ActionSubmitReview)
		}
		return actions
	}
	if viewerID == 0 || post.Status != PostStatusApproved {
		return []string{}
	}
	likeAction := ActionLike
	if liked {
		likeAction = ActionUnlike
	}
	return []string{likeAction, ActionComment}
}

// CanEditPost reports whether the author may edit a post.
func CanEditPost(status string) bool {
	return status == PostStatusPendingReview ||
		status == PostStatusApproved ||
		status == PostStatusRejected
}

// CanWithdrawPost reports whether the author may hide a post.
func CanWithdrawPost(status string) bool {
	return status == PostStatusPendingReview ||
		status == PostStatusApproved ||
		status == PostStatusRejected
}

// CanSubmitPostReview reports whether a rejected post may be resubmitted.
func CanSubmitPostReview(status string) bool { return status == PostStatusRejected }

// CanReviewPost reports whether an administrator may decide a post.
func CanReviewPost(status string) bool { return status == PostStatusPendingReview }

// CanRevokePostReview reports whether an administrator may undo a decision.
func CanRevokePostReview(status string) bool {
	return status == PostStatusApproved || status == PostStatusRejected
}

func runeLengthAbove(value string, maximum int) bool {
	return utf8.RuneCountInString(value) > maximum
}

func validOptionalURL(value string) bool {
	return value == "" || validURL(value)
}

func validURL(value string) bool {
	if runeLengthAbove(value, 2048) {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}
