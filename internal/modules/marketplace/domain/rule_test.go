package domain

import "testing"

func TestValidateListingInput(t *testing.T) {
	tests := []struct {
		name    string
		input   ListingInput
		wantErr bool
	}{
		{name: "valid", input: ListingInput{Title: "Desk", Description: "Good condition", PriceCents: 100, ImageURLs: []string{"https://example.test/a.jpg"}, Contact: ContactInput{Type: "phone", Value: "13800138000", Provided: true}}},
		{name: "zero price", input: ListingInput{Title: "Desk", Description: "Good", PriceCents: 0}, wantErr: true},
		{name: "unsafe image URL", input: ListingInput{Title: "Desk", Description: "Good", PriceCents: 100, ImageURLs: []string{"http://example.test/a.jpg"}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateListingInput(test.input); (err != nil) != test.wantErr {
				t.Fatalf("ValidateListingInput() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateContactInput(t *testing.T) {
	tests := []struct {
		name     string
		input    ContactInput
		required bool
		wantErr  bool
	}{
		{name: "valid", input: ContactInput{Type: "wechat", Value: "campus_user", Provided: true}},
		{name: "missing required", required: true, wantErr: true},
		{name: "invalid type", input: ContactInput{Type: "email", Value: "x", Provided: true}, wantErr: true},
		{name: "blank value", input: ContactInput{Type: "qq", Provided: true}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateContactInput(test.input, test.required); (err != nil) != test.wantErr {
				t.Fatalf("ValidateContactInput() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestStateTransitions(t *testing.T) {
	if !CanListingTransition(ListingPublished, ListingReserved) || CanListingTransition(ListingSold, ListingPublished) {
		t.Fatal("unexpected listing state transition")
	}
}

func TestViewerRelation(t *testing.T) {
	listing := &Listing{OwnerId: 7}
	tests := []struct {
		name           string
		viewerID       uint64
		hasActiveOrder bool
		want           string
	}{
		{name: "anonymous", want: ViewerRelationNone},
		{name: "owner takes precedence", viewerID: 7, hasActiveOrder: true, want: ViewerRelationOwner},
		{name: "buyer", viewerID: 8, hasActiveOrder: true, want: ViewerRelationBuyer},
		{name: "unrelated", viewerID: 8, want: ViewerRelationNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ViewerRelation(listing, test.viewerID, test.hasActiveOrder); got != test.want {
				t.Fatalf("ViewerRelation()=%q want=%q", got, test.want)
			}
		})
	}
}

func TestAvailableActions(t *testing.T) {
	tests := []struct {
		name           string
		status         string
		ownerID        uint64
		viewerID       uint64
		hasActiveOrder bool
		want           []string
	}{
		{name: "anonymous published", status: ListingPublished, ownerID: 7, want: []string{}},
		{name: "stranger published", status: ListingPublished, ownerID: 7, viewerID: 8, want: []string{ActionPurchase}},
		{name: "buyer reserved", status: ListingReserved, ownerID: 7, viewerID: 8, hasActiveOrder: true, want: []string{}},
		{name: "owner draft", status: ListingDraft, ownerID: 7, viewerID: 7, want: []string{ActionEdit, ActionSubmitReview, ActionWithdraw}},
		{name: "owner rejected", status: ListingRejected, ownerID: 7, viewerID: 7, want: []string{ActionEdit, ActionSubmitReview, ActionWithdraw}},
		{name: "owner pending", status: ListingPendingReview, ownerID: 7, viewerID: 7, want: []string{ActionWithdraw}},
		{name: "owner published", status: ListingPublished, ownerID: 7, viewerID: 7, want: []string{ActionWithdraw}},
		{name: "owner terminal", status: ListingSold, ownerID: 7, viewerID: 7, want: []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listing := &Listing{Status: test.status, OwnerId: test.ownerID}
			got := AvailableActions(listing, test.viewerID, test.hasActiveOrder)
			if len(got) != len(test.want) {
				t.Fatalf("AvailableActions()=%v want=%v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("AvailableActions()=%v want=%v", got, test.want)
				}
			}
		})
	}
}
