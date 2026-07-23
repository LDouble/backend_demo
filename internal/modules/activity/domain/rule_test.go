package domain

import (
	"strings"
	"testing"
	"time"
)

func TestValidateActivityInput(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	valid := ActivityInput{
		Title:         "Campus Marathon",
		Summary:       "A short race",
		Body:          "Detailed agenda",
		Location:      "North Field",
		SignupStartAt: now.Add(time.Hour),
		SignupEndAt:   now.Add(2 * time.Hour),
		StartAt:       now.Add(3 * time.Hour),
		EndAt:         now.Add(4 * time.Hour),
		Capacity:      50,
		Contact:       ContactInput{Type: "phone", Value: "13800138000", Provided: true},
	}
	tests := []struct {
		name    string
		input   ActivityInput
		wantErr bool
	}{
		{name: "valid", input: valid},
		{name: "invalid contact type", input: func() ActivityInput { in := valid; in.Contact.Type = "email"; return in }(), wantErr: true},
		{name: "signup ends after start", input: func() ActivityInput { in := valid; in.SignupEndAt = in.StartAt; return in }(), wantErr: true},
		{name: "ended in past", input: func() ActivityInput { in := valid; in.EndAt = now.Add(-time.Minute); return in }(), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateActivityInput(test.input, true, now); (err != nil) != test.wantErr {
				t.Fatalf("ValidateActivityInput() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestTransitionsAndVisibility(t *testing.T) {
	if !CanEdit(ActivityStatusDraft, ReviewStatusRejected) {
		t.Fatal("CanEdit() = false, want true")
	}
	if CanEdit(ActivityStatusPublished, ReviewStatusApproved) {
		t.Fatal("CanEdit() = true, want false")
	}
	if !CanPublish(ActivityStatusDraft, ReviewStatusApproved) {
		t.Fatal("CanPublish() = false, want true")
	}
	if CanPublish(ActivityStatusDraft, ReviewStatusPendingReview) {
		t.Fatal("CanPublish() = true, want false")
	}
	if !CanCancel(ActivityStatusDraft) || !CanCancel(ActivityStatusPublished) {
		t.Fatal("CanCancel() = false for cancellable activity")
	}
	if CanCancel(ActivityStatusCancelled) || CanCancel(ActivityStatusFinished) {
		t.Fatal("CanCancel() = true for terminal activity")
	}
	if !IsPubliclyVisible(ActivityStatusPublished, ReviewStatusApproved) {
		t.Fatal("IsPubliclyVisible() = false, want true")
	}
}

func TestRegistrationRules(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	activity := &Activity{
		Status:          ActivityStatusPublished,
		ReviewStatus:    ReviewStatusApproved,
		SignupStartAt:   now.Add(-time.Hour),
		SignupEndAt:     now.Add(time.Hour),
		StartAt:         now.Add(2 * time.Hour),
		Capacity:        2,
		RegisteredCount: 2,
	}
	if err := RegistrationAllowed(activity, now); err == nil {
		t.Fatal("RegistrationAllowed() error = nil, want capacity error")
	}
	activity.RegisteredCount = 1
	if err := RegistrationAllowed(activity, now); err != nil {
		t.Fatalf("RegistrationAllowed() error = %v, want nil", err)
	}
	registration := &ActivityRegistration{Status: RegistrationStatusActive}
	if err := CancellationAllowed(registration, activity, activity.StartAt); err == nil {
		t.Fatal("CancellationAllowed() error = nil, want started error")
	}
}

func TestViewerRelation(t *testing.T) {
	activity := &Activity{CreatedBy: 7}
	tests := []struct {
		name       string
		viewerID   uint64
		registered bool
		want       string
	}{
		{name: "anonymous", want: ViewerRelationNone},
		{name: "publisher takes precedence", viewerID: 7, registered: true, want: ViewerRelationPublisher},
		{name: "participant", viewerID: 8, registered: true, want: ViewerRelationParticipant},
		{name: "unrelated", viewerID: 8, want: ViewerRelationNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ViewerRelation(activity, test.viewerID, test.registered); got != test.want {
				t.Fatalf("ViewerRelation()=%q want=%q", got, test.want)
			}
		})
	}
}

func TestAvailableActions(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	base := Activity{
		CreatedBy: 7, Status: ActivityStatusPublished, ReviewStatus: ReviewStatusApproved,
		SignupStartAt: now.Add(-time.Hour), SignupEndAt: now.Add(time.Hour),
		StartAt: now.Add(2 * time.Hour), Capacity: 2,
	}
	tests := []struct {
		name       string
		mutate     func(*Activity)
		viewerID   uint64
		registered bool
		want       []string
	}{
		{name: "anonymous", want: []string{}},
		{name: "stranger registers", viewerID: 8, want: []string{ActionRegister}},
		{name: "participant cancels registration", viewerID: 8, registered: true, want: []string{ActionCancelRegistration}},
		{name: "participant after start", viewerID: 8, registered: true, mutate: func(v *Activity) { v.StartAt = now }, want: []string{}},
		{name: "publisher published", viewerID: 7, want: []string{ActionCancel}},
		{name: "publisher draft", viewerID: 7, mutate: func(v *Activity) { v.Status, v.ReviewStatus = ActivityStatusDraft, ReviewStatusDraft }, want: []string{ActionEdit, ActionSubmitReview, ActionCancel}},
		{name: "publisher rejected", viewerID: 7, mutate: func(v *Activity) { v.Status, v.ReviewStatus = ActivityStatusDraft, ReviewStatusRejected }, want: []string{ActionEdit, ActionCancel}},
		{name: "publisher terminal", viewerID: 7, mutate: func(v *Activity) { v.Status = ActivityStatusFinished }, want: []string{}},
		{name: "registration not open", viewerID: 8, mutate: func(v *Activity) { v.SignupStartAt = now.Add(time.Minute) }, want: []string{}},
		{name: "registration full", viewerID: 8, mutate: func(v *Activity) { v.RegisteredCount = v.Capacity }, want: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			activity := base
			if test.mutate != nil {
				test.mutate(&activity)
			}
			got := AvailableActions(&activity, test.viewerID, test.registered, now)
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("AvailableActions()=%v want=%v", got, test.want)
			}
		})
	}
}
