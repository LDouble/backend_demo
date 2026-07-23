package domain

import (
	"strings"
	"testing"
	"time"
)

func TestValidateTripInput(t *testing.T) {
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	valid := TripInput{Title: "回校", Origin: "火车站", Destination: "校园", DepartureAt: now.Add(time.Hour), TotalSeats: 2, ContactType: "phone", Contact: "13800000000"}
	for _, tc := range []struct {
		name    string
		input   TripInput
		wantErr bool
	}{
		{name: "valid", input: valid},
		{name: "empty title", input: func() TripInput { v := valid; v.Title = " "; return v }(), wantErr: true},
		{name: "long title", input: func() TripInput { v := valid; v.Title = strings.Repeat("行", 201); return v }(), wantErr: true},
		{name: "past departure", input: func() TripInput { v := valid; v.DepartureAt = now; return v }(), wantErr: true},
		{name: "unsupported contact", input: func() TripInput { v := valid; v.ContactType = "email"; return v }(), wantErr: true},
		{name: "empty contact", input: func() TripInput { v := valid; v.Contact = " "; return v }(), wantErr: true},
		{name: "long contact", input: func() TripInput { v := valid; v.Contact = strings.Repeat("1", 201); return v }(), wantErr: true},
		{name: "no seats", input: func() TripInput { v := valid; v.TotalSeats = 0; return v }(), wantErr: true},
		{name: "too many seats", input: func() TripInput { v := valid; v.TotalSeats = 21; return v }(), wantErr: true},
		{name: "long origin", input: func() TripInput { v := valid; v.Origin = strings.Repeat("地", 501); return v }(), wantErr: true},
		{name: "long destination", input: func() TripInput { v := valid; v.Destination = strings.Repeat("地", 501); return v }(), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTripInput(tc.input, now); (err != nil) != tc.wantErr {
				t.Fatalf("ValidateTripInput() error = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}

func TestValidateTripUpdateInputAllowsOmittedContact(t *testing.T) {
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	input := TripInput{
		Title:       "回校",
		Origin:      "火车站",
		Destination: "校园",
		DepartureAt: now.Add(time.Hour),
		TotalSeats:  2,
	}
	if err := ValidateTripUpdateInput(input, now); err != nil {
		t.Fatalf("ValidateTripUpdateInput() error=%v", err)
	}
	input.ContactProvided = true
	if err := ValidateTripUpdateInput(input, now); err == nil {
		t.Fatal("ValidateTripUpdateInput() missing contact error=nil")
	}
}

func TestViewerRelation(t *testing.T) {
	trip := &Trip{OrganizerId: 7}
	tests := []struct {
		name     string
		viewerID uint64
		joined   bool
		want     string
	}{
		{name: "anonymous", want: ViewerRelationNone},
		{name: "organizer takes precedence", viewerID: 7, joined: true, want: ViewerRelationOrganizer},
		{name: "participant", viewerID: 8, joined: true, want: ViewerRelationParticipant},
		{name: "unrelated", viewerID: 8, want: ViewerRelationNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ViewerRelation(trip, test.viewerID, test.joined); got != test.want {
				t.Fatalf("ViewerRelation()=%q want=%q", got, test.want)
			}
		})
	}
}

func TestAvailableActions(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	base := Trip{
		OrganizerId: 7, Status: TripOpen, ReviewStatus: ReviewApproved,
		DepartureAt: now.Add(time.Hour), TotalSeats: 2,
	}
	tests := []struct {
		name     string
		mutate   func(*Trip)
		viewerID uint64
		joined   bool
		want     []string
	}{
		{name: "anonymous", want: []string{}},
		{name: "stranger joins", viewerID: 8, want: []string{ActionJoin}},
		{name: "participant leaves", viewerID: 8, joined: true, want: []string{ActionLeave}},
		{name: "organizer approved", viewerID: 7, want: []string{ActionEdit, ActionCancel}},
		{name: "organizer draft", viewerID: 7, mutate: func(v *Trip) { v.ReviewStatus = ReviewDraft }, want: []string{ActionEdit, ActionSubmitReview, ActionCancel}},
		{name: "organizer rejected", viewerID: 7, mutate: func(v *Trip) { v.ReviewStatus = ReviewRejected }, want: []string{ActionEdit, ActionSubmitReview, ActionCancel}},
		{name: "organizer occupied", viewerID: 7, mutate: func(v *Trip) { v.OccupiedSeats = 1 }, want: []string{ActionCancel}},
		{name: "organizer full", viewerID: 7, mutate: func(v *Trip) { v.Status, v.OccupiedSeats = TripFull, 2 }, want: []string{ActionCancel}},
		{name: "full stranger", viewerID: 8, mutate: func(v *Trip) { v.Status, v.OccupiedSeats = TripFull, 2 }, want: []string{}},
		{name: "unapproved stranger", viewerID: 8, mutate: func(v *Trip) { v.ReviewStatus = ReviewPending }, want: []string{}},
		{name: "cancelled participant", viewerID: 8, joined: true, mutate: func(v *Trip) { v.Status = TripCancelled }, want: []string{}},
		{name: "departed", viewerID: 8, mutate: func(v *Trip) { v.DepartureAt = now }, want: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			trip := base
			if test.mutate != nil {
				test.mutate(&trip)
			}
			got := AvailableActions(&trip, test.viewerID, test.joined, now)
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("AvailableActions()=%v want=%v", got, test.want)
			}
		})
	}
}
