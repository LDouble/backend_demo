package httpapi

import (
	"context"
	"testing"
	"time"

	activityapp "github.com/weouc-plus/campus-platform/internal/modules/activity/application"
	activitydomain "github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
	carpoolapp "github.com/weouc-plus/campus-platform/internal/modules/carpool/application"
	carpooldomain "github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
)

type carpoolViewStore struct {
	carpoolapp.Store
}

func (carpoolViewStore) RevealContact(*carpooldomain.Trip) (string, error) {
	return "carpool_contact", nil
}

func (carpoolViewStore) JoinedTrips(context.Context, uint64, []uint64) (map[uint64]bool, error) {
	return map[uint64]bool{}, nil
}

func TestCarpoolViewIncludesViewerContext(t *testing.T) {
	handler := &Handler{carpools: carpoolapp.NewManager(carpoolViewStore{})}
	view := handler.carpoolViewOf(
		&carpooldomain.Trip{ID: 3},
		true,
		carpooldomain.ViewerRelationOrganizer,
		[]string{carpooldomain.ActionCancel},
	)
	if view.Contact != "carpool_contact" ||
		view.ViewerRelation != carpooldomain.ViewerRelationOrganizer ||
		len(view.AvailableActions) != 1 ||
		view.AvailableActions[0] != carpooldomain.ActionCancel {
		t.Fatalf("view=%+v", view)
	}
}

func TestActivityViewIncludesViewerContext(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	handler := &Handler{activities: activityapp.NewManager(nil)}
	view := handler.assembleActivityView(
		&activitydomain.Activity{ID: 4, StartAt: now},
		activitydomain.ContactDetails{Value: "activity_contact"},
		activitydomain.ViewerRelationParticipant,
		[]string{activitydomain.ActionCancelRegistration},
	)
	if view.Contact != "activity_contact" ||
		view.ViewerRelation != activitydomain.ViewerRelationParticipant ||
		len(view.AvailableActions) != 1 ||
		view.AvailableActions[0] != activitydomain.ActionCancelRegistration {
		t.Fatalf("view=%+v", view)
	}
}
