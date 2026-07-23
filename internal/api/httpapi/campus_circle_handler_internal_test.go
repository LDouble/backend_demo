package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	campuscircleapp "github.com/weouc-plus/campus-platform/internal/modules/campus_circle/application"
	campuscircledomain "github.com/weouc-plus/campus-platform/internal/modules/campus_circle/domain"
)

func TestCampusCircleSectionViewsKeepEmptyChildren(t *testing.T) {
	parentID := uint64(1)
	nodes := []campuscircleapp.SectionNode{{
		Section: campuscircledomain.CampusCircleSection{
			ID: 1, Slug: "life", Name: "校园生活",
		},
		Children: []campuscircleapp.SectionNode{{
			Section: campuscircledomain.CampusCircleSection{
				ID: 2, ParentId: &parentID, Slug: "daily", Name: "日常分享",
			},
			Children: nil,
		}},
	}}

	views := campusCircleSectionViews(nodes)
	if len(views) != 1 || len(views[0].Children) != 1 {
		t.Fatalf("section views = %+v", views)
	}
	child := views[0].Children[0]
	if child.ParentID == nil || *child.ParentID != parentID {
		t.Fatalf("child parent = %v, want %d", child.ParentID, parentID)
	}
	if child.Children == nil || len(child.Children) != 0 {
		t.Fatalf("leaf children = %#v, want empty array", child.Children)
	}
}

func TestCampusCirclePostViewActionsAndArrays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		viewerID  uint64
		authorID  uint64
		status    string
		liked     bool
		verified  bool
		want      []string
		wantCalls int
	}{
		{
			name:      "unverified other user receives verification action",
			viewerID:  7,
			authorID:  42,
			status:    campuscircledomain.PostStatusApproved,
			want:      []string{actionVerifyAcademic},
			wantCalls: 1,
		},
		{
			name:      "verified other user receives interaction actions",
			viewerID:  7,
			authorID:  42,
			status:    campuscircledomain.PostStatusApproved,
			liked:     true,
			verified:  true,
			want:      []string{campuscircledomain.ActionUnlike, campuscircledomain.ActionComment},
			wantCalls: 1,
		},
		{
			name:      "unverified pending owner keeps withdraw and verification",
			viewerID:  42,
			authorID:  42,
			status:    campuscircledomain.PostStatusPendingReview,
			want:      []string{campuscircledomain.ActionWithdraw, actionVerifyAcademic},
			wantCalls: 1,
		},
		{
			name:      "unverified approved owner keeps withdraw and verification",
			viewerID:  42,
			authorID:  42,
			status:    campuscircledomain.PostStatusApproved,
			want:      []string{campuscircledomain.ActionWithdraw, actionVerifyAcademic},
			wantCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			context.Set(userIDKey, test.viewerID)
			gate := &academicActionGate{verified: test.verified}
			handler := &Handler{
				campusCircle: campuscircleapp.NewManager(nil),
				academicGate: gate,
			}
			view, err := handler.campusCirclePostView(context, campuscircleapp.Item{
				Post: campuscircledomain.CampusCirclePost{
					ID: 3, AuthorId: test.authorID, Status: test.status,
					CreatedAt: now, UpdatedAt: now,
				},
				Images: nil,
				Liked:  test.liked,
			}, false)
			if err != nil {
				t.Fatal(err)
			}
			if view.Images == nil {
				t.Fatal("images must be an empty array")
			}
			if len(view.AvailableActions) != len(test.want) {
				t.Fatalf("actions = %v, want %v", view.AvailableActions, test.want)
			}
			for index := range test.want {
				if string(view.AvailableActions[index]) != test.want[index] {
					t.Fatalf("actions = %v, want %v", view.AvailableActions, test.want)
				}
			}
			if gate.calls != test.wantCalls {
				t.Fatalf("verification calls = %d, want %d", gate.calls, test.wantCalls)
			}
		})
	}
}

func TestCampusCircleStringsAlwaysReturnsSlice(t *testing.T) {
	if values := campusCircleStrings(nil); values == nil || len(values) != 0 {
		t.Fatalf("nil input = %#v, want empty slice", values)
	}
	input := []string{"https://example.com/image.png"}
	values := campusCircleStrings(&input)
	values[0] = "changed"
	if input[0] == values[0] {
		t.Fatal("input slice was not copied")
	}
}
