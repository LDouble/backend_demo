package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	activityapp "github.com/weouc-plus/campus-platform/internal/modules/activity/application"
	activitydomain "github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
	carpoolapp "github.com/weouc-plus/campus-platform/internal/modules/carpool/application"
	carpooldomain "github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
	errandapp "github.com/weouc-plus/campus-platform/internal/modules/errand/application"
	erraddomain "github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
	marketplaceapp "github.com/weouc-plus/campus-platform/internal/modules/marketplace/application"
	marketplacedomain "github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
)

type academicActionGate struct {
	verified bool
	err      error
	calls    int
}

func (g *academicActionGate) IsVerified(context.Context, uint64) (bool, error) {
	g.calls++
	return g.verified, g.err
}

type carpoolViewStore struct {
	carpoolapp.Store
}

func (carpoolViewStore) RevealContact(*carpooldomain.Trip) (string, error) {
	return "carpool_contact", nil
}

func (carpoolViewStore) JoinedTrips(context.Context, uint64, []uint64) (map[uint64]bool, error) {
	return map[uint64]bool{}, nil
}

type activityActionStore struct {
	activityapp.Store
}

func (activityActionStore) ContactWithAccess(
	context.Context,
	*activitydomain.Activity,
	uint64,
	bool,
) (activitydomain.ContactDetails, error) {
	return activitydomain.ContactDetails{}, nil
}

type errandActionStore struct {
	errandapp.Store
}

func (errandActionStore) Contact(
	context.Context,
	*erraddomain.Task,
	uint64,
) (erraddomain.ContactDetails, error) {
	return erraddomain.ContactDetails{}, nil
}

type marketplaceActionStore struct {
	marketplaceapp.Store
}

func (marketplaceActionStore) Contact(
	context.Context,
	*marketplacedomain.Listing,
	uint64,
) (marketplacedomain.ContactDetails, error) {
	return marketplacedomain.ContactDetails{}, nil
}

func (marketplaceActionStore) ActiveBuyerListings(
	context.Context,
	uint64,
	[]uint64,
) (map[uint64]bool, error) {
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

func TestAvailableActionsForViewer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newContext := func(viewerID uint64) *gin.Context {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		context.Set(userIDKey, viewerID)
		return context
	}

	tests := []struct {
		name       string
		viewerID   uint64
		actions    []string
		restricted string
		verified   bool
		want       []string
		wantCalls  int
	}{
		{
			name:       "empty actions use an empty JSON array",
			actions:    nil,
			restricted: "join",
			want:       []string{},
		},
		{
			name:       "anonymous action remains unchanged",
			actions:    []string{"join"},
			restricted: "join",
			want:       []string{"join"},
		},
		{
			name:       "verified viewer keeps restricted action",
			viewerID:   8,
			actions:    []string{"purchase"},
			restricted: "purchase",
			verified:   true,
			want:       []string{"purchase"},
			wantCalls:  1,
		},
		{
			name:       "unverified viewer is guided to verification",
			viewerID:   8,
			actions:    []string{"register"},
			restricted: "register",
			want:       []string{actionVerifyAcademic},
			wantCalls:  1,
		},
		{
			name:       "publisher action bypasses verification lookup",
			viewerID:   8,
			actions:    []string{"edit", "cancel"},
			restricted: "register",
			want:       []string{"edit", "cancel"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate := &academicActionGate{verified: test.verified}
			handler := &Handler{academicGate: gate}
			got, err := handler.availableActionsForViewer(
				newContext(test.viewerID),
				test.actions,
				test.restricted,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("actions=%v want=%v", got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("actions=%v want=%v", got, test.want)
				}
			}
			if gate.calls != test.wantCalls {
				t.Fatalf("verification calls=%d want=%d", gate.calls, test.wantCalls)
			}
		})
	}
}

func TestAvailableActionsForViewerCachesVerificationAndPropagatesErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	context.Set(userIDKey, uint64(8))

	gate := &academicActionGate{verified: false}
	handler := &Handler{academicGate: gate}
	for _, restrictedAction := range []string{"accept", "join"} {
		actions, err := handler.availableActionsForViewer(
			context,
			[]string{restrictedAction},
			restrictedAction,
		)
		if err != nil || len(actions) != 1 || actions[0] != actionVerifyAcademic {
			t.Fatalf("actions=%v err=%v", actions, err)
		}
	}
	if gate.calls != 1 {
		t.Fatalf("verification calls=%d want=1", gate.calls)
	}

	boom := errors.New("academic unavailable")
	errorRecorder := httptest.NewRecorder()
	errorContext, _ := gin.CreateTestContext(errorRecorder)
	errorContext.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	errorContext.Set(userIDKey, uint64(8))
	_, err := (&Handler{academicGate: &academicActionGate{err: boom}}).availableActionsForViewer(
		errorContext,
		[]string{"accept"},
		"accept",
	)
	if !errors.Is(err, boom) {
		t.Fatalf("error=%v want=%v", err, boom)
	}

	unavailableRecorder := httptest.NewRecorder()
	unavailableContext, _ := gin.CreateTestContext(unavailableRecorder)
	unavailableContext.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	unavailableContext.Set(userIDKey, uint64(8))
	_, err = (&Handler{}).availableActionsForViewer(unavailableContext, []string{"accept"}, "accept")
	appErr, ok := apperror.As(err)
	if !ok || appErr.Status != http.StatusServiceUnavailable || appErr.Code != "academic_verification_unavailable" {
		t.Fatalf("error=%v", err)
	}
}

func TestUnverifiedViewerGetsAcademicVerificationActionAcrossModules(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	newContext := func() *gin.Context {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		context.Set(userIDKey, uint64(8))
		return context
	}

	t.Run("activity registration", func(t *testing.T) {
		handler := &Handler{
			activities:   activityapp.NewManager(activityActionStore{}),
			academicGate: &academicActionGate{},
		}
		view, err := handler.activityViewWithAccess(newContext(), &activitydomain.Activity{
			CreatedBy: 7, Status: activitydomain.ActivityStatusPublished,
			ReviewStatus:  activitydomain.ReviewStatusApproved,
			SignupStartAt: now.Add(-time.Hour), SignupEndAt: now.Add(time.Hour),
			StartAt: now.Add(2 * time.Hour), Capacity: 2,
		}, false)
		if err != nil || len(view.AvailableActions) != 1 || view.AvailableActions[0] != actionVerifyAcademic {
			t.Fatalf("actions=%v err=%v", view.AvailableActions, err)
		}
	})

	t.Run("errand accept", func(t *testing.T) {
		handler := &Handler{
			errands:      errandapp.NewManager(errandActionStore{}),
			academicGate: &academicActionGate{},
		}
		view, err := handler.errandView(newContext(), &erraddomain.Task{
			RequesterId: 7, Status: erraddomain.TaskOpen,
			ReviewStatus: erraddomain.ReviewApproved, Deadline: now.Add(time.Hour),
		})
		if err != nil || len(view.AvailableActions) != 1 || view.AvailableActions[0] != actionVerifyAcademic {
			t.Fatalf("actions=%v err=%v", view.AvailableActions, err)
		}
	})

	t.Run("marketplace purchase", func(t *testing.T) {
		handler := &Handler{
			marketplace:  marketplaceapp.NewManager(marketplaceActionStore{}),
			academicGate: &academicActionGate{},
		}
		view, err := handler.marketplaceListingView(newContext(), marketplacedomain.ListingDetails{
			Listing: marketplacedomain.Listing{OwnerId: 7, Status: marketplacedomain.ListingPublished},
		})
		if err != nil || len(view.AvailableActions) != 1 || view.AvailableActions[0] != actionVerifyAcademic {
			t.Fatalf("actions=%v err=%v", view.AvailableActions, err)
		}
	})

	t.Run("carpool join", func(t *testing.T) {
		handler := &Handler{
			carpools:     carpoolapp.NewManager(carpoolViewStore{}),
			academicGate: &academicActionGate{},
		}
		view, err := handler.carpoolView(newContext(), &carpooldomain.Trip{
			OrganizerId: 7, Status: carpooldomain.TripOpen,
			ReviewStatus: carpooldomain.ReviewApproved,
			DepartureAt:  now.Add(time.Hour), TotalSeats: 2,
		}, false)
		if err != nil || len(view.AvailableActions) != 1 || view.AvailableActions[0] != actionVerifyAcademic {
			t.Fatalf("actions=%v err=%v", view.AvailableActions, err)
		}
	})
}
