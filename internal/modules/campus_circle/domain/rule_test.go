package domain

import (
	"strings"
	"testing"
)

func TestSectionInputRules(t *testing.T) {
	parentID := uint64(2)
	input := NormalizeSectionInput(SectionInput{
		ParentID:    &parentID,
		Slug:        "  Campus_Life ",
		Name:        " 校园生活 ",
		Description: " 交流 ",
		IconURL:     " https://example.com/icon.png ",
		CoverURL:    " https://example.com/cover.png ",
	})
	if input.Slug != "campus_life" || input.Name != "校园生活" ||
		input.Description != "交流" {
		t.Fatalf("unexpected normalized input: %+v", input)
	}
	if err := ValidateSectionInput(input, true); err != nil {
		t.Fatalf("valid create rejected: %v", err)
	}
	update := input
	update.Slug = ""
	update.ParentID = nil
	if err := ValidateSectionInput(update, false); err != nil {
		t.Fatalf("valid update rejected: %v", err)
	}

	zero := uint64(0)
	cases := []SectionInput{
		{ParentID: &zero, Slug: "valid", Name: "name"},
		{Slug: "-bad", Name: "name"},
		{Slug: "valid", Name: ""},
		{Slug: "valid", Name: strings.Repeat("界", 65)},
		{Slug: "valid", Name: "name", Description: strings.Repeat("界", 501)},
		{Slug: "valid", Name: "name", IconURL: "javascript:bad"},
		{Slug: "valid", Name: "name", CoverURL: "ftp://example.com/a"},
	}
	for i, value := range cases {
		if err := ValidateSectionInput(value, true); err == nil {
			t.Errorf("case %d unexpectedly valid: %+v", i, value)
		}
	}
	if err := ValidateSectionInput(SectionInput{Slug: "immutable", Name: "name"}, false); err == nil {
		t.Error("update unexpectedly accepted slug")
	}
	if err := ValidateSectionInput(SectionInput{ParentID: &parentID, Name: "name"}, false); err == nil {
		t.Error("update unexpectedly accepted parent")
	}
}

func TestSectionHierarchyRules(t *testing.T) {
	rootID := uint64(1)
	root := &CampusCircleSection{ID: rootID, Status: SectionStatusActive}
	child := &CampusCircleSection{ID: 2, ParentId: &rootID, Status: SectionStatusActive}
	if err := ValidateSectionParent(0, nil, nil); err != nil {
		t.Fatalf("root rejected: %v", err)
	}
	if err := ValidateSectionParent(2, &rootID, root); err != nil {
		t.Fatalf("child rejected: %v", err)
	}
	if !CanAcceptPosts(child, root) {
		t.Error("active child under active root should accept posts")
	}

	archivedRoot := *root
	archivedRoot.Status = SectionStatusArchived
	cases := []struct {
		id      uint64
		request *uint64
		parent  *CampusCircleSection
	}{
		{0, nil, root},
		{1, &rootID, root},
		{2, &rootID, nil},
		{2, &rootID, child},
		{2, &rootID, &archivedRoot},
	}
	for i, test := range cases {
		if err := ValidateSectionParent(test.id, test.request, test.parent); err == nil {
			t.Errorf("case %d unexpectedly accepted", i)
		}
	}
	if CanAcceptPosts(nil, root) || CanAcceptPosts(root, nil) ||
		CanAcceptPosts(child, &archivedRoot) {
		t.Error("invalid hierarchy accepted posts")
	}
	if !CanArchiveSection(SectionStatusActive) || CanArchiveSection(SectionStatusArchived) {
		t.Error("archive transition rules are incorrect")
	}
	if !CanActivateSection(SectionStatusArchived) || CanActivateSection(SectionStatusActive) {
		t.Error("activate transition rules are incorrect")
	}
}

func TestPostInputRules(t *testing.T) {
	input := NormalizePostInput(PostInput{
		SectionID: 1,
		Title:     " 标题 ",
		Content:   " 正文 ",
		ImageURLs: []string{" https://example.com/1.png "},
	})
	if input.Title != "标题" || input.Content != "正文" ||
		input.ImageURLs[0] != "https://example.com/1.png" {
		t.Fatalf("unexpected normalization: %+v", input)
	}
	if err := ValidatePostInput(input); err != nil {
		t.Fatalf("valid post rejected: %v", err)
	}
	if err := ValidatePostInput(PostInput{SectionID: 1, ImageURLs: []string{"https://example.com/a"}}); err != nil {
		t.Fatalf("image-only post rejected: %v", err)
	}
	cases := []PostInput{
		{},
		{SectionID: 1},
		{SectionID: 1, Title: strings.Repeat("界", 101)},
		{SectionID: 1, Content: strings.Repeat("界", 5001)},
		{SectionID: 1, ImageURLs: make([]string, 10)},
		{SectionID: 1, ImageURLs: []string{""}},
		{SectionID: 1, ImageURLs: []string{"relative/path"}},
		{SectionID: 1, ImageURLs: []string{"https://" + strings.Repeat("a", 2048)}},
	}
	for i, value := range cases {
		if err := ValidatePostInput(value); err == nil {
			t.Errorf("case %d unexpectedly valid", i)
		}
	}
}

func TestPostVisibilityRelationAndActions(t *testing.T) {
	post := &CampusCirclePost{AuthorId: 7, Status: PostStatusPendingReview}
	if VisibleTo(nil, 7, false) || VisibleTo(post, 0, false) ||
		VisibleTo(post, 8, false) || !VisibleTo(post, 7, false) ||
		!VisibleTo(post, 0, true) {
		t.Error("pending visibility is incorrect")
	}
	post.Status = PostStatusApproved
	if !VisibleTo(post, 0, false) || !VisibleTo(post, 8, false) {
		t.Error("approved post should be public")
	}
	if ViewerRelation(post, 0, false) != ViewerRelationAnonymous ||
		ViewerRelation(post, 7, false) != ViewerRelationOwner ||
		ViewerRelation(post, 8, false) != ViewerRelationOther ||
		ViewerRelation(post, 7, true) != ViewerRelationAdmin {
		t.Error("viewer relation is incorrect")
	}

	if actions := AvailableActions(nil, 7, false, false); len(actions) != 0 {
		t.Errorf("nil post actions: %v", actions)
	}
	if actions := AvailableActions(post, 7, false, true); len(actions) != 0 {
		t.Errorf("admin actions: %v", actions)
	}
	ownerActions := AvailableActions(post, 7, false, false)
	assertActions(t, ownerActions, ActionEdit, ActionWithdraw)
	assertActions(t, AvailableActions(post, 8, false, false), ActionLike, ActionComment)
	assertActions(t, AvailableActions(post, 8, true, false), ActionUnlike, ActionComment)
	if actions := AvailableActions(post, 0, false, false); len(actions) != 0 {
		t.Errorf("anonymous actions: %v", actions)
	}
	post.Status = PostStatusRejected
	assertActions(t, AvailableActions(post, 7, false, false), ActionEdit, ActionWithdraw, ActionSubmitReview)
	if actions := AvailableActions(post, 8, false, false); len(actions) != 0 {
		t.Errorf("other user got rejected-post actions: %v", actions)
	}
}

func TestPostStateRules(t *testing.T) {
	statuses := []string{
		PostStatusPendingReview,
		PostStatusApproved,
		PostStatusRejected,
		PostStatusWithdrawn,
		"unknown",
	}
	for _, status := range statuses {
		wantMutable := status == PostStatusPendingReview ||
			status == PostStatusApproved ||
			status == PostStatusRejected
		if CanEditPost(status) != wantMutable || CanWithdrawPost(status) != wantMutable {
			t.Errorf("mutable rule mismatch for %q", status)
		}
		if CanSubmitPostReview(status) != (status == PostStatusRejected) {
			t.Errorf("submit rule mismatch for %q", status)
		}
		if CanReviewPost(status) != (status == PostStatusPendingReview) {
			t.Errorf("review rule mismatch for %q", status)
		}
		wantRevoke := status == PostStatusApproved || status == PostStatusRejected
		if CanRevokePostReview(status) != wantRevoke {
			t.Errorf("revoke rule mismatch for %q", status)
		}
	}
}

func assertActions(t *testing.T, actual []string, expected ...string) {
	t.Helper()
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("actions = %v, want %v", actual, expected)
	}
}
