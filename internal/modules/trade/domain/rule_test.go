package domain

import "testing"

func TestCanTransition(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{name: "complete confirmed", from: StatusConfirmed, to: StatusCompleted, want: true},
		{name: "cancel confirmed", from: StatusConfirmed, to: StatusCancelled, want: true},
		{name: "cannot reopen completed", from: StatusCompleted, to: StatusConfirmed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CanTransition(test.from, test.to); got != test.want {
				t.Fatalf("CanTransition(%q, %q) = %v, want %v", test.from, test.to, got, test.want)
			}
		})
	}
}
