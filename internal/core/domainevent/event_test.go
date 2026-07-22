package domainevent

import "testing"

func TestEventTableNameAndStatuses(t *testing.T) {
	if got := (Event{}).TableName(); got != "domain_events" {
		t.Fatalf("TableName() = %q, want domain_events", got)
	}
	statuses := map[string]bool{
		StatusPending:    true,
		StatusLeased:     true,
		StatusDispatched: true,
		StatusFailed:     true,
	}
	if len(statuses) != 4 {
		t.Fatal("domain event statuses must remain distinct")
	}
}
