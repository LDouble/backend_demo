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
