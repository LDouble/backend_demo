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
		{name: "past departure", input: func() TripInput { v := valid; v.DepartureAt = now; return v }(), wantErr: true},
		{name: "unsupported contact", input: func() TripInput { v := valid; v.ContactType = "email"; return v }(), wantErr: true},
		{name: "no seats", input: func() TripInput { v := valid; v.TotalSeats = 0; return v }(), wantErr: true},
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
