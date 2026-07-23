package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/weouc-plus/campus-platform/internal/api/generated"
	errandapp "github.com/weouc-plus/campus-platform/internal/modules/errand/application"
	erranddomain "github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
)

type mineHTTPStore struct {
	errandapp.Store
	search erranddomain.MineSearch
	called bool
	task   erranddomain.Task
}

func (store *mineHTTPStore) ListMine(
	_ context.Context,
	_ uint64,
	search erranddomain.MineSearch,
	_,
	_ int,
) ([]erranddomain.Task, int64, error) {
	store.called = true
	store.search = search
	return []erranddomain.Task{store.task}, 1, nil
}

func (*mineHTTPStore) Contact(
	context.Context,
	*erranddomain.Task,
	uint64,
) (erranddomain.ContactDetails, error) {
	return erranddomain.ContactDetails{Type: "wechat", Value: "owner-contact"}, nil
}

func TestListMyErrandsUsesTypedFiltersAndReturnsViewerActions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	store := &mineHTTPStore{task: erranddomain.Task{
		ID: 12, Title: "代取快递", Status: erranddomain.TaskOpen,
		ReviewStatus: erranddomain.ReviewApproved, RequesterId: 7,
		Deadline: now.Add(time.Hour), Version: 2,
	}}
	handler := &Handler{
		errands:      errandapp.NewManager(store),
		academicGate: &academicActionGate{verified: true},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/errands/mine?page=1&page_size=20", nil)
	c.Set(userIDKey, uint64(7))
	relation := generated.ListMyErrandsParamsRelation(erranddomain.MineRelationPublished)
	status := generated.ListMyErrandsParamsStatus(erranddomain.TaskOpen)
	reviewStatus := generated.ListMyErrandsParamsReviewStatus(erranddomain.ReviewApproved)
	setGeneratedParams(c, "ListMyErrands", generated.ListMyErrandsParams{
		Relation: &relation, Status: &status, ReviewStatus: &reviewStatus,
	})

	handler.listMyErrands(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	wantSearch := erranddomain.MineSearch{
		Relation: erranddomain.MineRelationPublished,
		Status:   erranddomain.TaskOpen, ReviewStatus: erranddomain.ReviewApproved,
	}
	if store.search != wantSearch {
		t.Fatalf("search = %+v, want %+v", store.search, wantSearch)
	}
	var response struct {
		Data struct {
			Items []errandView `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Items) != 1 {
		t.Fatalf("items = %+v", response.Data.Items)
	}
	item := response.Data.Items[0]
	if item.ViewerRelation != erranddomain.ViewerRelationPublisher {
		t.Fatalf("viewer_relation = %q", item.ViewerRelation)
	}
	if len(item.AvailableActions) != 2 ||
		item.AvailableActions[0] != erranddomain.ActionEdit ||
		item.AvailableActions[1] != erranddomain.ActionCancel {
		t.Fatalf("available_actions = %v", item.AvailableActions)
	}
	if item.Contact != "owner-contact" {
		t.Fatalf("contact = %q", item.Contact)
	}
}

func TestListMyErrandsRejectsInvalidTypedFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &mineHTTPStore{}
	handler := &Handler{errands: errandapp.NewManager(store)}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/errands/mine", nil)
	c.Set(userIDKey, uint64(7))
	relation := generated.ListMyErrandsParamsRelation("invalid")
	setGeneratedParams(c, "ListMyErrands", generated.ListMyErrandsParams{Relation: &relation})

	handler.listMyErrands(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if store.called {
		t.Fatal("store called for invalid relation")
	}
}
