package domain

import "testing"

func TestValidateListingInput(t *testing.T) {
	tests := []struct {
		name    string
		input   ListingInput
		wantErr bool
	}{
		{name: "valid", input: ListingInput{Title: "Desk", Description: "Good condition", PriceCents: 100, ImageURLs: []string{"https://example.test/a.jpg"}}},
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

func TestStateTransitions(t *testing.T) {
	if !CanListingTransition(ListingPublished, ListingReserved) || CanListingTransition(ListingSold, ListingPublished) {
		t.Fatal("unexpected listing state transition")
	}
}
