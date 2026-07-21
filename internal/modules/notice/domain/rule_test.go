package domain

import "testing"

func TestValidateDraft(t *testing.T) {
	valid := DraftInput{Title: "停水通知", Body: "正文", Category: "campus", Priority: PriorityImportant, Channels: []string{ChannelInApp}, Audience: Audience{All: true}}
	tests := []struct {
		name string
		edit func(*DraftInput)
		want bool
	}{
		{name: "valid", want: true},
		{name: "empty audience", edit: func(v *DraftInput) { v.Audience = Audience{} }},
		{name: "missing in app", edit: func(v *DraftInput) { v.Channels = []string{ChannelPush} }},
		{name: "invalid category", edit: func(v *DraftInput) { v.Category = "Campus News" }},
		{name: "invalid action", edit: func(v *DraftInput) { v.ActionPath = "pages/notices" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			if test.edit != nil {
				test.edit(&value)
			}
			if got := ValidateDraft(value) == nil; got != test.want {
				t.Fatalf("ValidateDraft() valid = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCanTransition(t *testing.T) {
	for _, test := range []struct {
		from, to string
		want     bool
	}{
		{StatusDraft, StatusScheduled, true},
		{StatusDraft, StatusPublishing, true},
		{StatusPublished, StatusRevoked, true},
		{StatusPublished, StatusDraft, false},
		{StatusRevoked, StatusPublished, false},
	} {
		if got := CanTransition(test.from, test.to); got != test.want {
			t.Fatalf("CanTransition(%q, %q) = %v, want %v", test.from, test.to, got, test.want)
		}
	}
}

func TestGeneratedEntityTableNames(t *testing.T) {
	if (Notice{}).TableName() != "notices" || (NoticeAudience{}).TableName() != "notice_audiences" || (NoticeRecipient{}).TableName() != "notice_recipients" || (NoticeDelivery{}).TableName() != "notice_deliveries" || (OutboxEvent{}).TableName() != "outbox_events" {
		t.Fatal("generated table names changed")
	}
}
