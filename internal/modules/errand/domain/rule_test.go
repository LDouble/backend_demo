package domain

import (
	"testing"
	"time"
)

func TestValidateTaskInput(t *testing.T) {
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	valid := TaskInput{Title: "取快递", Description: "帮忙从菜鸟驿站取件", RewardCents: 500, PickupLocation: "菜鸟驿站", DropoffLocation: "3 号宿舍", Deadline: now.Add(time.Hour), Contact: ContactInput{Type: "phone", Value: "13800138000", Provided: true}}
	for _, tc := range []struct {
		name    string
		input   TaskInput
		wantErr bool
	}{
		{name: "valid", input: valid},
		{name: "past deadline", input: TaskInput{Title: valid.Title, Description: valid.Description, RewardCents: valid.RewardCents, PickupLocation: valid.PickupLocation, DropoffLocation: valid.DropoffLocation, Deadline: now}, wantErr: true},
		{name: "zero reward", input: TaskInput{Title: valid.Title, Description: valid.Description, PickupLocation: valid.PickupLocation, DropoffLocation: valid.DropoffLocation, Deadline: valid.Deadline}, wantErr: true},
		{name: "blank location", input: TaskInput{Title: valid.Title, Description: valid.Description, RewardCents: valid.RewardCents, DropoffLocation: valid.DropoffLocation, Deadline: valid.Deadline}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTaskInput(tc.input, now); (err != nil) != tc.wantErr {
				t.Fatalf("ValidateTaskInput() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateContactInput(t *testing.T) {
	if err := ValidateContactInput(ContactInput{Type: "qq", Value: "123456", Provided: true}, true); err != nil {
		t.Fatalf("ValidateContactInput() error = %v", err)
	}
	if err := ValidateContactInput(ContactInput{}, true); err == nil {
		t.Fatal("ValidateContactInput() error = nil, want missing contact error")
	}
	if err := ValidateContactInput(ContactInput{Type: "email", Value: "a", Provided: true}, false); err == nil {
		t.Fatal("ValidateContactInput() error = nil, want invalid type error")
	}
}

func TestCanTransition(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		want     bool
	}{
		{TaskOpen, TaskAccepted, true}, {TaskOpen, TaskCancelled, true}, {TaskAccepted, TaskPickedUp, true}, {TaskAccepted, TaskCancelled, true}, {TaskPickedUp, TaskDelivered, true}, {TaskDelivered, TaskCompleted, true}, {TaskOpen, TaskCompleted, false}, {TaskPickedUp, TaskCancelled, false},
	} {
		if got := CanTransition(tc.from, tc.to); got != tc.want {
			t.Errorf("CanTransition(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}
