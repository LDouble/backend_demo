// Package domain contains carpool trip rules.
package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	// TripOpen and the following constants define the trip lifecycle.
	TripOpen = "open"
	// TripFull means the trip has no remaining seats.
	TripFull = "full"
	// TripCompleted means the departure time has passed or the trip was completed.
	TripCompleted = "completed"
	// TripCancelled means the organizer cancelled the trip.
	TripCancelled = "cancelled"
	// ParticipantJoined marks an active participant row.
	ParticipantJoined = "joined"
	// ParticipantLeft marks a participant that has left the trip.
	ParticipantLeft = "left"
)

// TripInput is the user-controlled portion of a trip.
type TripInput struct {
	Title, Origin, Destination, ContactType, Contact string
	DepartureAt                                      time.Time
	TotalSeats                                       int64
}

// Search contains public trip search filters.
type Search struct {
	Origin, Destination string
	DepartureDate       *time.Time
	SeatsNeeded         int64
}

// ValidateTripInput validates a trip before persistence.
func ValidateTripInput(in TripInput, now time.Time) error {
	title := strings.TrimSpace(in.Title)
	origin := strings.TrimSpace(in.Origin)
	destination := strings.TrimSpace(in.Destination)
	if title == "" || origin == "" || destination == "" {
		return fmt.Errorf("标题、出发地和目的地不能为空")
	}
	if len([]rune(title)) > 200 || len([]rune(origin)) > 500 || len([]rune(destination)) > 500 {
		return fmt.Errorf("标题、出发地或目的地长度超出限制")
	}
	if in.TotalSeats < 1 || in.TotalSeats > 20 {
		return fmt.Errorf("座位数必须在 1 到 20 之间")
	}
	if !in.DepartureAt.After(now) {
		return fmt.Errorf("出发时间必须晚于当前时间")
	}
	switch strings.TrimSpace(in.ContactType) {
	case "phone", "wechat", "qq":
	default:
		return fmt.Errorf("不支持的联系方式类型")
	}
	if contact := strings.TrimSpace(in.Contact); contact == "" || len(contact) > 200 {
		return fmt.Errorf("联系方式无效")
	}
	return nil
}
