package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/api/httpapi"
	"github.com/weouc-plus/campus-platform/internal/core/auth"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	platformmysql "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql"
	activityapp "github.com/weouc-plus/campus-platform/internal/modules/activity/application"
	activitydomain "github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
	activityinfra "github.com/weouc-plus/campus-platform/internal/modules/activity/infrastructure"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type handlerFixture struct {
	router       http.Handler
	users        *user.Service
	permissions  *permission.Service
	adminToken   string
	userPassword string
}

type casbinRule struct {
	ID                     uint64 `gorm:"primaryKey;autoIncrement"`
	Ptype                  string
	V0, V1, V2, V3, V4, V5 string
	Managed                bool
}

func (casbinRule) TableName() string { return "casbin_rule" }

type permissionPolicyOutbox struct {
	ID           uint64 `gorm:"primaryKey;autoIncrement"`
	Version      string `gorm:"uniqueIndex"`
	Attempts     int64
	DispatchedAt *time.Time
	LockedAt     *time.Time
	LockedBy     string
	CreatedAt    time.Time
}

func (permissionPolicyOutbox) TableName() string { return "permission_policy_outbox" }

type memorySessionStore struct {
	mu      sync.Mutex
	session map[string]string
}

func (s *memorySessionStore) Create(_ context.Context, sid string, _ uint64, hash string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session[sid] = hash
	return nil
}

func (s *memorySessionStore) Exists(_ context.Context, sid string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.session[sid]
	return ok, nil
}

func (s *memorySessionStore) Rotate(_ context.Context, sid, oldHash, newHash string, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session[sid] != oldHash {
		return false, nil
	}
	s.session[sid] = newHash
	return true, nil
}

func (s *memorySessionStore) Delete(_ context.Context, sid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.session, sid)
	return nil
}

func (s *memorySessionStore) DeleteUser(_ context.Context, _ uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = make(map[string]string)
	return nil
}

func TestHandlerFailureScenarios(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(*testing.T, *handlerFixture) *httptest.ResponseRecorder
		wantStatus int
		wantCode   string
	}{
		{
			name: "绑定错误",
			prepare: func(t *testing.T, f *handlerFixture) *httptest.ResponseRecorder {
				return perform(t, f.router, http.MethodPost, "/api/v1/auth/login", "", []byte("{"))
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
		},
		{
			name: "未认证",
			prepare: func(t *testing.T, f *handlerFixture) *httptest.ResponseRecorder {
				return perform(t, f.router, http.MethodGet, "/api/v1/users", "", nil)
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "missing_token",
		},
		{
			name: "无权限",
			prepare: func(t *testing.T, f *handlerFixture) *httptest.ResponseRecorder {
				created, err := f.users.Create(context.Background(), "ordinary", f.userPassword)
				if err != nil {
					t.Fatal(err)
				}
				_ = created
				token := loginToken(t, f.router, "ordinary", f.userPassword)
				return perform(t, f.router, http.MethodGet, "/api/v1/users", token, nil)
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "forbidden",
		},
		{
			name: "禁用用户",
			prepare: func(t *testing.T, f *handlerFixture) *httptest.ResponseRecorder {
				created, err := f.users.Create(context.Background(), "disabled_user", f.userPassword)
				if err != nil {
					t.Fatal(err)
				}
				token := loginToken(t, f.router, created.Username, f.userPassword)
				if _, err = f.users.SetStatus(context.Background(), created.ID, model.UserDisabled); err != nil {
					t.Fatal(err)
				}
				return perform(t, f.router, http.MethodGet, "/api/v1/auth/me", token, nil)
			},
			wantStatus: http.StatusForbidden,
			wantCode:   "user_disabled",
		},
		{
			name: "重复用户名",
			prepare: func(t *testing.T, f *handlerFixture) *httptest.ResponseRecorder {
				if _, err := f.users.Create(context.Background(), "duplicate", f.userPassword); err != nil {
					t.Fatal(err)
				}
				return performJSON(t, f.router, http.MethodPost, "/api/v1/users", f.adminToken, map[string]any{"username": "duplicate", "password": f.userPassword})
			},
			wantStatus: http.StatusConflict,
			wantCode:   "username_exists",
		},
		{
			name: "配置版本冲突",
			prepare: func(t *testing.T, f *handlerFixture) *httptest.ResponseRecorder {
				created := performJSON(t, f.router, http.MethodPost, "/api/v1/configs", f.adminToken, map[string]any{"group": "test", "key": "conflict", "value": "one"})
				var envelope struct {
					Data struct {
						ID      uint64 `json:"id"`
						Version uint64 `json:"version"`
					} `json:"data"`
				}
				decodeRecorder(t, created, &envelope)
				return performJSON(t, f.router, http.MethodPut, "/api/v1/configs/"+strconv.FormatUint(envelope.Data.ID, 10), f.adminToken, map[string]any{"expected_version": envelope.Data.Version + 1, "value": "two"})
			},
			wantStatus: http.StatusConflict,
			wantCode:   "version_conflict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newHandlerFixture(t)
			response := tt.prepare(t, fixture)
			assertErrorEnvelope(t, response, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestActivityHTTPFlow(t *testing.T) {
	fixture := newHandlerFixture(t)
	ownerToken, _, ownerID := fixture.createUserWithPermissions(t, "activity_owner", activityAdminPermissions(), activityUserPermissions())
	participantToken, _, _ := fixture.createUserWithPermissions(t, "activity_participant", activityUserPermissions())
	bystanderToken, _, _ := fixture.createUserWithPermissions(t, "activity_bystander", activityUserPermissions())

	now := time.Now().UTC()
	startDate := now.Add(2 * time.Hour)
	createResponse := performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities", ownerToken, map[string]any{
		"title":           "Campus Run",
		"summary":         "Morning training event",
		"body":            "Gather at the east gate",
		"location":        "East Gate Plaza",
		"signup_start_at": now.Add(-time.Hour).Format(time.RFC3339),
		"signup_end_at":   now.Add(time.Hour).Format(time.RFC3339),
		"start_at":        startDate.Format(time.RFC3339),
		"end_at":          startDate.Add(2 * time.Hour).Format(time.RFC3339),
		"capacity":        2,
		"contact_type":    "wechat",
		"contact":         "owner_wechat_42",
	})
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create activity status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	created := decodeActivityView(t, createResponse)
	if created.CreatedBy != ownerID || created.Contact != "owner_wechat_42" {
		t.Fatalf("created activity = %+v", created)
	}

	adminList := perform(t, fixture.router, http.MethodGet, "/api/v1/admin/activities?keyword=training&status=draft&review_status=draft&page=1&page_size=10", ownerToken, nil)
	assertStatusCode(t, adminList, http.StatusOK)
	var adminPage pageEnvelope[activityView]
	decodeDataRecorder(t, adminList, &adminPage)
	if len(adminPage.Items) != 1 || adminPage.Items[0].ID != created.ID {
		t.Fatalf("admin page items=%+v", adminPage.Items)
	}

	assertStatusCode(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/submit-review", ownerToken, map[string]any{
		"expected_version": created.Version,
	}), http.StatusOK)
	submitted := decodeActivityView(t, perform(t, fixture.router, http.MethodGet, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10), ownerToken, nil))
	if submitted.ReviewStatus != activitydomain.ReviewStatusPendingReview {
		t.Fatalf("submitted review status=%s", submitted.ReviewStatus)
	}

	assertStatusCode(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/approve", fixture.adminToken, map[string]any{
		"expected_version": submitted.Version,
		"review_comment":   "looks good",
	}), http.StatusOK)
	approved := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/publish", ownerToken, map[string]any{
		"expected_version": submitted.Version + 1,
	}))
	if approved.Status != activitydomain.ActivityStatusPublished || approved.ReviewStatus != activitydomain.ReviewStatusApproved {
		t.Fatalf("approved activity=%+v", approved)
	}

	publicList := perform(t, fixture.router, http.MethodGet, "/api/v1/activities?keyword=East%20Gate&page=1&page_size=10", participantToken, nil)
	assertStatusCode(t, publicList, http.StatusOK)
	var publicPage pageEnvelope[activityView]
	decodeDataRecorder(t, publicList, &publicPage)
	if len(publicPage.Items) != 1 || publicPage.Items[0].ID != approved.ID {
		t.Fatalf("public page=%+v", publicPage.Items)
	}

	bystanderDetail := decodeActivityView(t, perform(t, fixture.router, http.MethodGet, "/api/v1/activities/"+strconv.FormatUint(approved.ID, 10), bystanderToken, nil))
	if bystanderDetail.Contact == "owner_wechat_42" || bystanderDetail.Contact == "" {
		t.Fatalf("bystander detail contact=%q", bystanderDetail.Contact)
	}

	registrationKey := "activity-registration-flow"
	registerResponse := perform(
		t,
		fixture.router,
		http.MethodPost,
		"/api/v1/activities/"+strconv.FormatUint(approved.ID, 10)+"/registrations",
		participantToken,
		nil,
		registrationKey,
	)
	assertStatusCode(t, registerResponse, http.StatusCreated)
	registration := decodeRegistrationResult(t, registerResponse)
	if registration.Registration.Status != activitydomain.RegistrationStatusActive || registration.Activity.RegisteredCount != 1 {
		t.Fatalf("registration result=%+v", registration)
	}

	participantDetail := decodeActivityView(t, perform(t, fixture.router, http.MethodGet, "/api/v1/activities/"+strconv.FormatUint(approved.ID, 10), participantToken, nil))
	if participantDetail.Contact != "owner_wechat_42" {
		t.Fatalf("participant contact=%q", participantDetail.Contact)
	}

	myRegistrations := perform(t, fixture.router, http.MethodGet, "/api/v1/activities/registrations/mine?page=1&page_size=10", participantToken, nil)
	assertStatusCode(t, myRegistrations, http.StatusOK)
	var registrationsPage pageEnvelope[myActivityRegistrationView]
	decodeDataRecorder(t, myRegistrations, &registrationsPage)
	if len(registrationsPage.Items) != 1 || registrationsPage.Items[0].ActivityID != approved.ID || registrationsPage.Items[0].Activity.CreatedBy != ownerID {
		t.Fatalf("mine page=%+v", registrationsPage.Items)
	}

	cancelResponse := performJSON(t, fixture.router, http.MethodDelete, "/api/v1/activities/"+strconv.FormatUint(approved.ID, 10)+"/registrations/me", participantToken, map[string]any{
		"expected_version": registration.Registration.RegistrationVersion,
	})
	assertStatusCode(t, cancelResponse, http.StatusOK)
	cancelled := decodeRegistrationResult(t, cancelResponse)
	if cancelled.Registration.Status != activitydomain.RegistrationStatusCancelled || cancelled.Activity.RegisteredCount != 0 {
		t.Fatalf("cancelled result=%+v", cancelled)
	}

	afterCancelDetail := decodeActivityView(t, perform(t, fixture.router, http.MethodGet, "/api/v1/activities/"+strconv.FormatUint(approved.ID, 10), participantToken, nil))
	if afterCancelDetail.Contact == "owner_wechat_42" || afterCancelDetail.Contact == "" {
		t.Fatalf("after cancel contact=%q", afterCancelDetail.Contact)
	}

	replayKey := "activity-registration-replay"
	reactivateResponse := perform(
		t,
		fixture.router,
		http.MethodPost,
		"/api/v1/activities/"+strconv.FormatUint(approved.ID, 10)+"/registrations",
		participantToken,
		nil,
		replayKey,
	)
	assertStatusCode(t, reactivateResponse, http.StatusCreated)
	reactivated := decodeRegistrationResult(t, reactivateResponse)
	if reactivated.Registration.Status != activitydomain.RegistrationStatusActive || reactivated.Activity.RegisteredCount != 1 {
		t.Fatalf("reactivated registration result=%+v", reactivated)
	}

	cancelActivityResponse := performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(approved.ID, 10)+"/cancel", ownerToken, map[string]any{
		"expected_version": reactivated.Activity.Version,
	})
	assertStatusCode(t, cancelActivityResponse, http.StatusOK)

	replayResponse := perform(
		t,
		fixture.router,
		http.MethodPost,
		"/api/v1/activities/"+strconv.FormatUint(approved.ID, 10)+"/registrations",
		participantToken,
		nil,
		replayKey,
	)
	assertStatusCode(t, replayResponse, http.StatusCreated)
	replayed := decodeRegistrationResult(t, replayResponse)
	if replayed.Registration.ID != reactivated.Registration.ID || replayed.Activity.RegisteredCount != 1 {
		t.Fatalf("replayed registration result=%+v", replayed)
	}
}

func TestActivityIdempotencyReplayPrecedesCurrentState(t *testing.T) {
	fixture := newHandlerFixture(t)
	ownerToken, _, _ := fixture.createUserWithPermissions(t, "idempotent_owner", activityAdminPermissions(), activityUserPermissions())
	now := time.Now().UTC()
	body := map[string]any{
		"title": "Stable replay", "summary": "summary", "body": "body", "location": "field",
		"signup_start_at": now.Add(-time.Hour).Format(time.RFC3339),
		"signup_end_at":   now.Add(time.Hour).Format(time.RFC3339),
		"start_at":        now.Add(2 * time.Hour).Format(time.RFC3339),
		"end_at":          now.Add(3 * time.Hour).Format(time.RFC3339),
		"capacity":        2, "contact_type": "wechat", "contact": "stable",
	}
	created := decodeActivityView(t, perform(t, fixture.router, http.MethodPost, "/api/v1/admin/activities", ownerToken, mustJSON(t, body), "create-stable"))
	submitBody := map[string]any{"expected_version": created.Version}
	path := "/api/v1/admin/activities/" + strconv.FormatUint(created.ID, 10) + "/submit-review"
	first := perform(t, fixture.router, http.MethodPost, path, ownerToken, mustJSON(t, submitBody), "submit-stable")
	assertStatusCode(t, first, http.StatusOK)
	second := perform(t, fixture.router, http.MethodPost, path, ownerToken, mustJSON(t, submitBody), "submit-stable")
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay differs: first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	submitBody["expected_version"] = created.Version + 1
	conflict := perform(t, fixture.router, http.MethodPost, path, ownerToken, mustJSON(t, submitBody), "submit-stable")
	assertErrorEnvelope(t, conflict, http.StatusConflict, "idempotency_key_reused")
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestActivityReviewEditAndTerminalMasking(t *testing.T) {
	fixture := newHandlerFixture(t)
	ownerToken, _, _ := fixture.createUserWithPermissions(t, "activity_editor", activityAdminPermissions(), activityUserPermissions())

	startDate := time.Date(2026, 7, 23, 14, 0, 0, 0, time.UTC)
	created := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities", ownerToken, map[string]any{
		"title":           "Rejected Draft",
		"summary":         "Needs review",
		"body":            "Initial copy",
		"location":        "Library Hall",
		"signup_start_at": startDate.Add(-48 * time.Hour).Format(time.RFC3339),
		"signup_end_at":   startDate.Add(-24 * time.Hour).Format(time.RFC3339),
		"start_at":        startDate.Format(time.RFC3339),
		"end_at":          startDate.Add(2 * time.Hour).Format(time.RFC3339),
		"capacity":        5,
		"contact_type":    "qq",
		"contact":         "998877",
	}))

	submitted := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/submit-review", ownerToken, map[string]any{
		"expected_version": created.Version,
	}))
	rejected := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/reject", fixture.adminToken, map[string]any{
		"expected_version": submitted.Version,
		"review_comment":   "need clearer agenda",
	}))
	if rejected.ReviewStatus != activitydomain.ReviewStatusRejected || rejected.ReviewComment == nil || *rejected.ReviewComment == "" {
		t.Fatalf("rejected activity=%+v", rejected)
	}

	updated := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPatch, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10), ownerToken, map[string]any{
		"title":            "Rejected Draft Revised",
		"summary":          "Updated summary",
		"body":             "Rewritten agenda",
		"location":         "Gym Annex",
		"signup_start_at":  startDate.Add(-48 * time.Hour).Format(time.RFC3339),
		"signup_end_at":    startDate.Add(-25 * time.Hour).Format(time.RFC3339),
		"start_at":         startDate.Format(time.RFC3339),
		"end_at":           startDate.Add(3 * time.Hour).Format(time.RFC3339),
		"capacity":         6,
		"expected_version": rejected.Version,
		"contact_type":     "qq",
		"contact":          "99887766",
	}))
	if updated.ReviewStatus != activitydomain.ReviewStatusDraft || updated.ReviewComment != nil {
		t.Fatalf("updated review state=%+v", updated)
	}

	submittedAgain := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/submit-review", ownerToken, map[string]any{
		"expected_version": updated.Version,
	}))
	approved := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/approve", fixture.adminToken, map[string]any{
		"expected_version": submittedAgain.Version,
	}))
	published := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/publish", ownerToken, map[string]any{
		"expected_version": approved.Version,
	}))
	earlyFinish := performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/finish", ownerToken, map[string]any{
		"expected_version": published.Version,
	})
	assertStatusCode(t, earlyFinish, http.StatusConflict)
	earlyCancelled := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/cancel", ownerToken, map[string]any{
		"expected_version": published.Version,
	}))
	if earlyCancelled.Status != activitydomain.ActivityStatusCancelled || earlyCancelled.Contact == "99887766" {
		t.Fatalf("cancelled activity=%+v", earlyCancelled)
	}

	second := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities", ownerToken, map[string]any{
		"title":           "Cancel Flow",
		"summary":         "Cancel summary",
		"body":            "Cancel body",
		"location":        "South Court",
		"signup_start_at": startDate.Add(-72 * time.Hour).Format(time.RFC3339),
		"signup_end_at":   startDate.Add(-48 * time.Hour).Format(time.RFC3339),
		"start_at":        startDate.Add(24 * time.Hour).Format(time.RFC3339),
		"end_at":          startDate.Add(26 * time.Hour).Format(time.RFC3339),
		"capacity":        3,
		"contact_type":    "phone",
		"contact":         "13800138000",
	}))
	second = decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(second.ID, 10)+"/submit-review", ownerToken, map[string]any{
		"expected_version": second.Version,
	}))
	second = decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(second.ID, 10)+"/approve", fixture.adminToken, map[string]any{
		"expected_version": second.Version,
	}))
	second = decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(second.ID, 10)+"/publish", ownerToken, map[string]any{
		"expected_version": second.Version,
	}))
	cancelled := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(second.ID, 10)+"/cancel", ownerToken, map[string]any{
		"expected_version": second.Version,
	}))
	if cancelled.Status != activitydomain.ActivityStatusCancelled || cancelled.Contact == "13800138000" {
		t.Fatalf("cancelled activity=%+v", cancelled)
	}
}

func TestCoreManagementWriteIdempotency(t *testing.T) {
	fixture := newHandlerFixture(t)
	key := "core-role-replay"
	body := []byte(`{"name":"idempotent_role","description":"first"}`)
	first := perform(t, fixture.router, http.MethodPost, "/api/v1/roles", fixture.adminToken, body, key)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := perform(t, fixture.router, http.MethodPost, "/api/v1/roles", fixture.adminToken, body, key)
	if second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("replay status=%d body=%s", second.Code, second.Body.String())
	}
	if first.Header().Get("Content-Type") != "application/json; charset=utf-8" || second.Header().Get("Content-Type") != first.Header().Get("Content-Type") {
		t.Fatalf("replay content types first=%q second=%q", first.Header().Get("Content-Type"), second.Header().Get("Content-Type"))
	}
	conflict := perform(t, fixture.router, http.MethodPost, "/api/v1/roles", fixture.adminToken, []byte(`{"name":"different_role"}`), key)
	assertErrorEnvelope(t, conflict, http.StatusConflict, "idempotency_key_reused")

	request := httptest.NewRequest(http.MethodPost, "/api/v1/roles", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+fixture.adminToken)
	response := httptest.NewRecorder()
	fixture.router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency key status=%d body=%s", response.Code, response.Body.String())
	}
}

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &model.Role{}, &model.Config{}, &casbinRule{}, &permissionPolicyOutbox{}, &idempotency.Record{}, &activitydomain.Activity{}, &activitydomain.ActivityRegistration{}, &domainevent.Event{}); err != nil {
		t.Fatal(err)
	}
	permissions, err := permission.NewService(db, platformmysql.NewRoleRepository(db))
	if err != nil {
		t.Fatal(err)
	}
	users := user.NewService(platformmysql.NewUserRepository(db), permissions)
	password := "fixture-password"
	admin, err := users.Create(context.Background(), "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	if err = permissions.Bootstrap(context.Background(), admin.ID); err != nil {
		t.Fatal(err)
	}
	cipher, err := configcenter.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	sessions := &memorySessionStore{session: make(map[string]string)}
	authService := auth.NewService(platformmysql.NewUserRepository(db), sessions, "test", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour)
	activities := activityapp.NewManager(activityinfra.NewStore(db, cipher))
	handler := httpapi.New(authService, users, permissions, configcenter.NewService(platformmysql.NewConfigRepository(db), cipher), func(context.Context) error { return nil }, func(context.Context) error { return nil }, zap.NewNop()).
		WithDatabase(db).
		WithActivities(activities)
	router, err := handler.Router()
	if err != nil {
		t.Fatal(err)
	}
	return &handlerFixture{router: router, users: users, permissions: permissions, adminToken: loginToken(t, router, admin.Username, password), userPassword: password}
}

func loginToken(t *testing.T, router http.Handler, username, password string) string {
	t.Helper()
	response := performJSON(t, router, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": username, "password": password})
	if response.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data auth.TokenPair `json:"data"`
	}
	decodeRecorder(t, response, &envelope)
	return envelope.Data.AccessToken
}

func performJSON(t *testing.T, router http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return perform(t, router, method, path, token, raw)
}

var testIdempotencySequence atomic.Uint64

func perform(t *testing.T, router http.Handler, method, path, token string, body []byte, idempotencyKeys ...string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		key := "test-" + strconv.FormatUint(testIdempotencySequence.Add(1), 10)
		if len(idempotencyKeys) > 0 {
			key = idempotencyKeys[0]
		}
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func assertErrorEnvelope(t *testing.T, response *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status=%d want=%d body=%s", response.Code, wantStatus, response.Body.String())
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		RequestID string `json:"request_id"`
	}
	decodeRecorder(t, response, &envelope)
	if envelope.Error.Code != wantCode || envelope.Error.Message == "" || envelope.RequestID == "" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
}

func decodeRecorder(t *testing.T, response *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response: %v body=%s", err, response.Body.String())
	}
}

type activityView struct {
	ID              uint64  `json:"id"`
	Title           string  `json:"title"`
	Summary         string  `json:"summary"`
	Location        string  `json:"location"`
	Status          string  `json:"status"`
	ReviewStatus    string  `json:"review_status"`
	ReviewComment   *string `json:"review_comment"`
	CreatedBy       uint64  `json:"created_by"`
	Contact         string  `json:"contact"`
	Version         uint64  `json:"version"`
	RegisteredCount int64   `json:"registered_count"`
}

type pageEnvelope[T any] struct {
	Items []T `json:"items"`
}

type registrationEnvelope struct {
	Registration struct {
		ID                  uint64     `json:"id"`
		ActivityID          uint64     `json:"activity_id"`
		Status              string     `json:"status"`
		CancelledAt         *time.Time `json:"cancelled_at"`
		RegistrationVersion uint64     `json:"registration_version"`
	} `json:"registration"`
	Activity activityView `json:"activity"`
}

type myActivityRegistrationView struct {
	RegistrationID      uint64       `json:"registration_id"`
	ActivityID          uint64       `json:"activity_id"`
	Status              string       `json:"status"`
	RegistrationVersion uint64       `json:"registration_version"`
	Activity            activityView `json:"activity"`
}

func (f *handlerFixture) createUserWithPermissions(t *testing.T, prefix string, permissionGroups ...[]permission.Permission) (string, string, uint64) {
	t.Helper()
	username := sanitizeRoleName(prefix)
	if len(username) > 18 {
		username = username[:18]
	}
	username += strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10)
	created, err := f.users.Create(context.Background(), username, f.userPassword)
	if err != nil {
		t.Fatal(err)
	}
	roleName := sanitizeRoleName(prefix) + "_role"
	role, err := f.permissions.CreateRole(context.Background(), roleName, "test role", false)
	if err != nil {
		t.Fatal(err)
	}
	var permissions []permission.Permission
	for _, group := range permissionGroups {
		permissions = append(permissions, group...)
	}
	if err := f.permissions.SetPermissions(context.Background(), role.ID, permissions); err != nil {
		t.Fatal(err)
	}
	if err := f.permissions.SetUserRoles(context.Background(), created.ID, []string{role.Name}); err != nil {
		t.Fatal(err)
	}
	return loginToken(t, f.router, created.Username, f.userPassword), created.Username, created.ID
}

func activityAdminPermissions() []permission.Permission {
	return []permission.Permission{
		{PathPattern: "/api/v1/admin/activities", Methods: []string{"GET", "POST"}},
		{PathPattern: "/api/v1/admin/activities/:id", Methods: []string{"GET", "PATCH"}},
		{PathPattern: "/api/v1/admin/activities/:id/submit-review", Methods: []string{"POST"}},
		{PathPattern: "/api/v1/admin/activities/:id/publish", Methods: []string{"POST"}},
		{PathPattern: "/api/v1/admin/activities/:id/cancel", Methods: []string{"POST"}},
		{PathPattern: "/api/v1/admin/activities/:id/finish", Methods: []string{"POST"}},
	}
}

func activityUserPermissions() []permission.Permission {
	return []permission.Permission{
		{PathPattern: "/api/v1/activities", Methods: []string{"GET"}},
		{PathPattern: "/api/v1/activities/:id", Methods: []string{"GET"}},
		{PathPattern: "/api/v1/activities/:id/registrations", Methods: []string{"POST"}},
		{PathPattern: "/api/v1/activities/:id/registrations/me", Methods: []string{"DELETE"}},
		{PathPattern: "/api/v1/activities/registrations/mine", Methods: []string{"GET"}},
	}
}

// decodeActivityView decodes a 2xx envelope carrying a single activityView.
// The caller is responsible for asserting the status code up front; this
// helper deliberately does NOT auto-assert, because callers depend on this
// behaviour to test partial decode paths. (Previous versions had a self-
// comparing `assertStatusCode(t, response, response.Code)` line which was a
// no-op and masked regressions where the body was a non-2xx error envelope.)
func decodeActivityView(t *testing.T, response *httptest.ResponseRecorder) activityView {
	t.Helper()
	var envelope struct {
		Data activityView `json:"data"`
	}
	decodeRecorder(t, response, &envelope)
	return envelope.Data
}

func decodeRegistrationResult(t *testing.T, response *httptest.ResponseRecorder) registrationEnvelope {
	t.Helper()
	var envelope struct {
		Data registrationEnvelope `json:"data"`
	}
	decodeRecorder(t, response, &envelope)
	return envelope.Data
}

func decodeDataRecorder(t *testing.T, response *httptest.ResponseRecorder, dst any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	decodeRecorder(t, response, &envelope)
	if err := json.Unmarshal(envelope.Data, dst); err != nil {
		t.Fatalf("decode data: %v body=%s", err, response.Body.String())
	}
}

func assertStatusCode(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body.String())
	}
}

func sanitizeRoleName(prefix string) string {
	prefix = strings.ToLower(prefix)
	replacer := strings.NewReplacer(" ", "_", "-", "_", "/", "_")
	prefix = replacer.Replace(prefix)
	if len(prefix) < 3 {
		prefix += "xxx"
	}
	return prefix
}
