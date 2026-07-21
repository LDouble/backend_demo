package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	TripOpen          = "open"
	TripFull          = "full"
	TripCompleted     = "completed"
	TripCancelled     = "cancelled"
	ParticipantJoined = "joined"
	ParticipantLeft   = "left"
)

type TripInput struct {
	Title, Origin, Destination, ContactType, Contact string
	DepartureAt                                      time.Time
	TotalSeats                                       int64
}
type Search struct {
	Origin, Destination string
	DepartureDate       *time.Time
	SeatsNeeded         int64
}

func ValidateTripInput(in TripInput, now time.Time) error {
	if strings.TrimSpace(in.Title) == "" || len(in.Title) > 200 || strings.TrimSpace(in.Origin) == "" || strings.TrimSpace(in.Destination) == "" {
		return fmt.Errorf("标题、出发地和目的地不能为空")
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
