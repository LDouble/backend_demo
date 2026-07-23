package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	carpoolapp "github.com/weouc-plus/campus-platform/internal/modules/carpool/application"
	carpooldomain "github.com/weouc-plus/campus-platform/internal/modules/carpool/domain"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type handlerFixture struct {
	router       http.Handler
	handler      *httpapi.Handler
	users        *user.Service
	permissions  *permission.Service
	adminToken   string
	userPassword string
}

type accountFailureLimiter struct {
	failures int
	loginIPs []string
}

type alwaysVerified struct{}

func (alwaysVerified) IsVerified(context.Context, uint64) (bool, error) { return true, nil }

type neverVerified struct{}

func (neverVerified) IsVerified(context.Context, uint64) (bool, error) { return false, nil }

func (l *accountFailureLimiter) AllowLoginIP(_ context.Context, ip string) (bool, error) {
	l.loginIPs = append(l.loginIPs, ip)
	return true, nil
}

func (*accountFailureLimiter) AllowWeChatLogin(context.Context, string) (bool, error) {
	return true, nil
}

func (l *accountFailureLimiter) RecordLoginFailure(context.Context, string) (bool, error) {
	l.failures++
	return l.failures <= 5, nil
}

func (l *accountFailureLimiter) ClearLoginFailures(context.Context, string) error {
	l.failures = 0
	return nil
}

func (*accountFailureLimiter) AllowRefresh(context.Context, string, string) (bool, error) {
	return true, nil
}

type failingCarpoolStore struct{ err error }

type mineCarpoolStore struct {
	failingCarpoolStore
	userID uint64
	search carpooldomain.AdminSearch
}

func (s *mineCarpoolStore) ListMine(_ context.Context, userID uint64, search carpooldomain.AdminSearch, _, _ int) ([]carpooldomain.Trip, int64, error) {
	s.userID, s.search = userID, search
	return []carpooldomain.Trip{{
		ID: 42, Title: "我的待审拼车", Status: carpooldomain.TripOpen,
		ReviewStatus: carpooldomain.ReviewPending, OrganizerId: userID,
	}}, 1, nil
}

func (*mineCarpoolStore) RevealContact(*carpooldomain.Trip) (string, error) {
	return "carpool_owner", nil
}

func (s failingCarpoolStore) CreateTrip(context.Context, uint64, carpooldomain.TripInput, time.Time) (*carpooldomain.Trip, error) {
	return nil, s.err
}

func (failingCarpoolStore) UpdateTrip(context.Context, uint64, uint64, uint64, carpooldomain.TripInput, time.Time) (*carpooldomain.Trip, error) {
	return nil, errors.New("not implemented")
}

func (failingCarpoolStore) GetTrip(context.Context, uint64, uint64) (*carpooldomain.Trip, bool, error) {
	return nil, false, errors.New("not implemented")
}

func (failingCarpoolStore) JoinedTrips(context.Context, uint64, []uint64) (map[uint64]bool, error) {
	return map[uint64]bool{}, nil
}

func (failingCarpoolStore) SearchTrips(context.Context, carpooldomain.Search, int, int, time.Time) ([]carpooldomain.Trip, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (failingCarpoolStore) ListAdmin(context.Context, carpooldomain.AdminSearch, int, int) ([]carpooldomain.Trip, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (failingCarpoolStore) ListMine(context.Context, uint64, carpooldomain.AdminSearch, int, int) ([]carpooldomain.Trip, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (failingCarpoolStore) SubmitReview(context.Context, uint64, uint64, uint64) (*carpooldomain.Trip, error) {
	return nil, errors.New("not implemented")
}

func (failingCarpoolStore) Review(context.Context, uint64, uint64, uint64, bool, string, time.Time) (*carpooldomain.Trip, error) {
	return nil, errors.New("not implemented")
}

func (failingCarpoolStore) RevokeReview(context.Context, uint64, uint64, uint64, string, time.Time) (*carpooldomain.Trip, error) {
	return nil, errors.New("not implemented")
}

func (failingCarpoolStore) Join(context.Context, uint64, uint64, uint64, time.Time) (*carpooldomain.Trip, error) {
	return nil, errors.New("not implemented")
}

func (failingCarpoolStore) Leave(context.Context, uint64, uint64, uint64, time.Time) (*carpooldomain.Trip, error) {
	return nil, errors.New("not implemented")
}

func (failingCarpoolStore) Cancel(context.Context, uint64, uint64, uint64, time.Time) (*carpooldomain.Trip, error) {
	return nil, errors.New("not implemented")
}

func (failingCarpoolStore) CompleteDue(context.Context, time.Time) (int64, error) {
	return 0, errors.New("not implemented")
}

func (failingCarpoolStore) RevealContact(*carpooldomain.Trip) (string, error) {
	return "", errors.New("not implemented")
}

func TestLoginFailuresDoNotLockOutCorrectPassword(t *testing.T) {
	limiter := &accountFailureLimiter{}
	fixture := newHandlerFixtureWithLimiter(t, limiter)
	for attempt := 0; attempt < 6; attempt++ {
		response := performJSON(t, fixture.router, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
			"username": "admin",
			"password": "wrong-password",
		})
		want := http.StatusUnauthorized
		if attempt == 5 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("attempt=%d status=%d body=%s", attempt+1, response.Code, response.Body.String())
		}
	}
	response := performJSON(t, fixture.router, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin",
		"password": fixture.userPassword,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("correct password remained locked: status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Fatalf("cache control=%q", got)
	}
}

func TestWechatLoginDoesNotRequireIdempotencyKey(t *testing.T) {
	fixture := newHandlerFixture(t)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/wechat/login",
		strings.NewReader(`{"app_id":"wxapp-1","code":"code"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	fixture.router.ServeHTTP(response, request)

	// The fixture has no WeChat provider, so reaching the handler must report
	// its normal availability error rather than an idempotency-header error.
	assertErrorEnvelope(t, response, http.StatusServiceUnavailable, "wechat_disabled")
}

func TestInfrastructureErrorsAreNotExposed(t *testing.T) {
	fixture := newHandlerFixture(t)
	fixture.handler.WithCarpools(carpoolapp.NewManager(failingCarpoolStore{
		err: errors.New("mysql users password=secret"),
	}))
	response := performJSON(t, fixture.router, http.MethodPost, "/api/v1/carpool/trips", fixture.adminToken, map[string]any{
		"title": "Campus ride", "origin": "Station", "destination": "Campus",
		"departure_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		"total_seats":  2, "contact_type": "phone", "contact": "13800138000",
	})
	assertErrorEnvelope(t, response, http.StatusInternalServerError, "internal_error")
	if strings.Contains(response.Body.String(), "mysql") || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("internal error leaked: %s", response.Body.String())
	}
}

func TestAcademicVerificationGateCannotBeBypassedBySuperAdmin(t *testing.T) {
	fixture := newHandlerFixture(t)
	fixture.handler.WithAcademicVerificationGate(neverVerified{})
	router, err := fixture.handler.Router()
	if err != nil {
		t.Fatal(err)
	}
	response := performJSON(
		t,
		router,
		http.MethodPost,
		"/api/v1/carpool/trips",
		fixture.adminToken,
		map[string]any{
			"title": "Campus ride", "origin": "Station", "destination": "Campus",
			"departure_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
			"total_seats":  2, "contact_type": "phone", "contact": "13800138000",
		},
	)
	assertErrorEnvelope(t, response, http.StatusForbidden, "academic_verification_required")
}

func TestMyCarpoolTripsIncludesPendingTrips(t *testing.T) {
	fixture := newHandlerFixture(t)
	store := &mineCarpoolStore{}
	fixture.handler.WithCarpools(carpoolapp.NewManager(store))

	response := perform(
		t,
		fixture.router,
		http.MethodGet,
		"/api/v1/carpool/trips/mine?status=open&review_status=pending_review&keyword=%E5%BE%85%E5%AE%A1&page=1&page_size=10",
		fixture.adminToken,
		nil,
	)
	assertStatusCode(t, response, http.StatusOK)
	var page pageEnvelope[struct {
		ID           uint64 `json:"id"`
		ReviewStatus string `json:"review_status"`
		Contact      string `json:"contact"`
	}]
	decodeDataRecorder(t, response, &page)
	if len(page.Items) != 1 ||
		page.Items[0].ID != 42 ||
		page.Items[0].ReviewStatus != carpooldomain.ReviewPending ||
		page.Items[0].Contact != "carpool_owner" {
		t.Fatalf("my carpool page=%+v", page.Items)
	}
	if store.userID == 0 ||
		store.search.Status != carpooldomain.TripOpen ||
		store.search.ReviewStatus != carpooldomain.ReviewPending ||
		store.search.Keyword != "待审" {
		t.Fatalf("delegated user=%d search=%+v", store.userID, store.search)
	}
}

func TestChangeMyPasswordRevokesAllSessions(t *testing.T) {
	fixture := newHandlerFixture(t)
	newPassword := "replacement-password-123"
	response := performJSON(t, fixture.router, http.MethodPatch, "/api/v1/auth/me/password", fixture.adminToken, map[string]string{
		"current_password": fixture.userPassword,
		"new_password":     newPassword,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("change password status=%d body=%s", response.Code, response.Body.String())
	}
	response = perform(t, fixture.router, http.MethodGet, "/api/v1/auth/me", fixture.adminToken, nil)
	assertErrorEnvelope(t, response, http.StatusUnauthorized, "session_expired")
	response = performJSON(t, fixture.router, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": fixture.userPassword,
	})
	assertErrorEnvelope(t, response, http.StatusUnauthorized, "invalid_credentials")
	response = performJSON(t, fixture.router, http.MethodPost, "/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": newPassword,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("new password login status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSecurityHeaders(t *testing.T) {
	fixture := newHandlerFixture(t)
	response := perform(t, fixture.router, http.MethodGet, "/health/live", "", nil)
	for name, want := range map[string]string{
		"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
		"Referrer-Policy":         "no-referrer",
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
	} {
		if got := response.Header().Get(name); got != want {
			t.Fatalf("%s=%q want=%q", name, got, want)
		}
	}
}

func TestTrustedProxyClientIPAndHTTPSBoundary(t *testing.T) {
	limiter := &accountFailureLimiter{}
	fixture := newHandlerFixtureWithLimiter(t, limiter)
	fixture.handler.WithProxyPolicy([]string{"10.0.0.0/8"}, false)
	router, err := fixture.handler.Router()
	if err != nil {
		t.Fatal(err)
	}
	direct := loginRequest(t, "203.0.113.9:1234", "198.51.100.10", "")
	router.ServeHTTP(httptest.NewRecorder(), direct)
	proxied := loginRequest(t, "10.1.0.2:1234", "198.51.100.20, 10.2.0.3", "")
	router.ServeHTTP(httptest.NewRecorder(), proxied)
	if len(limiter.loginIPs) < 2 || limiter.loginIPs[len(limiter.loginIPs)-2] != "203.0.113.9" || limiter.loginIPs[len(limiter.loginIPs)-1] != "198.51.100.20" {
		t.Fatalf("login IPs=%v", limiter.loginIPs)
	}

	fixture.handler.WithProxyPolicy([]string{"10.0.0.0/8"}, true)
	secureRouter, err := fixture.handler.Router()
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*http.Request{
		loginRequest(t, "10.1.0.2:1234", "198.51.100.20", ""),
		loginRequest(t, "203.0.113.9:1234", "198.51.100.20", "https"),
	} {
		response := httptest.NewRecorder()
		secureRouter.ServeHTTP(response, request)
		assertErrorEnvelope(t, response, http.StatusForbidden, "secure_proxy_required")
	}
	request := loginRequest(t, "10.1.0.2:1234", "198.51.100.20", "https")
	response := httptest.NewRecorder()
	secureRouter.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("Strict-Transport-Security") == "" {
		t.Fatalf("trusted HTTPS status=%d HSTS=%q body=%s", response.Code, response.Header().Get("Strict-Transport-Security"), response.Body.String())
	}
}

func loginRequest(t *testing.T, remoteAddress, forwardedFor, forwardedProto string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": "admin", "password": "wrong-password"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	request.RemoteAddr = remoteAddress
	request.Header.Set("Content-Type", "application/json")
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	if forwardedProto != "" {
		request.Header.Set("X-Forwarded-Proto", forwardedProto)
	}
	return request
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
			wantStatus: http.StatusUnauthorized,
			wantCode:   "session_expired",
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

func TestAdminIdentityAndPermissionCatalog(t *testing.T) {
	fixture := newHandlerFixture(t)

	meResponse := perform(t, fixture.router, http.MethodGet, "/api/v1/auth/me", fixture.adminToken, nil)
	if meResponse.Code != http.StatusOK {
		t.Fatalf("me status=%d body=%s", meResponse.Code, meResponse.Body.String())
	}
	var meEnvelope struct {
		Data struct {
			User struct {
				Username string `json:"username"`
			} `json:"user"`
			Roles       []string `json:"roles"`
			Permissions []string `json:"permissions"`
		} `json:"data"`
		RequestID string `json:"request_id"`
	}
	decodeRecorder(t, meResponse, &meEnvelope)
	if meEnvelope.Data.User.Username != "admin" || !containsTestString(meEnvelope.Data.Roles, model.SuperAdminRole) {
		t.Fatalf("current user=%+v", meEnvelope.Data)
	}
	if !containsTestString(meEnvelope.Data.Permissions, "core:listpermissioncatalog") || meEnvelope.RequestID == "" {
		t.Fatalf("current permissions=%v request_id=%q", meEnvelope.Data.Permissions, meEnvelope.RequestID)
	}

	catalogResponse := perform(t, fixture.router, http.MethodGet, "/api/v1/permissions", fixture.adminToken, nil)
	if catalogResponse.Code != http.StatusOK {
		t.Fatalf("catalog status=%d body=%s", catalogResponse.Code, catalogResponse.Body.String())
	}
	var catalogEnvelope struct {
		Data struct {
			Permissions []struct {
				Code        string   `json:"code"`
				Module      string   `json:"module"`
				PathPattern string   `json:"path_pattern"`
				Methods     []string `json:"methods"`
			} `json:"permissions"`
		} `json:"data"`
	}
	decodeRecorder(t, catalogResponse, &catalogEnvelope)
	if len(catalogEnvelope.Data.Permissions) == 0 {
		t.Fatal("permission catalog is empty")
	}
	for index, entry := range catalogEnvelope.Data.Permissions {
		if entry.Code == "" || entry.Module == "" || entry.PathPattern == "" || len(entry.Methods) == 0 {
			t.Fatalf("catalog entry %d=%+v", index, entry)
		}
	}

	created, err := fixture.users.Create(context.Background(), "catalog_denied", fixture.userPassword)
	if err != nil {
		t.Fatal(err)
	}
	ordinaryToken := loginToken(t, fixture.router, created.Username, fixture.userPassword)
	denied := perform(t, fixture.router, http.MethodGet, "/api/v1/permissions", ordinaryToken, nil)
	assertErrorEnvelope(t, denied, http.StatusForbidden, "forbidden")
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

	myList := perform(t, fixture.router, http.MethodGet, "/api/v1/activities/mine?status=draft&review_status=draft&keyword=training&page=1&page_size=10", ownerToken, nil)
	assertStatusCode(t, myList, http.StatusOK)
	var myPage pageEnvelope[activityView]
	decodeDataRecorder(t, myList, &myPage)
	if len(myPage.Items) != 1 || myPage.Items[0].ID != created.ID || myPage.Items[0].Contact != "owner_wechat_42" {
		t.Fatalf("my activity page items=%+v", myPage.Items)
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
	outsiderToken, _, _ := fixture.createUserWithPermissions(t, "activity_outsider", activityUserPermissions())

	startDate := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	created := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/activities", ownerToken, map[string]any{
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
	draftPath := "/api/v1/activities/" + strconv.FormatUint(created.ID, 10)
	assertErrorEnvelope(t, perform(t, fixture.router, http.MethodGet, draftPath, outsiderToken, nil), http.StatusNotFound, "activity_not_found")
	assertErrorEnvelope(t, performJSON(t, fixture.router, http.MethodPost, draftPath+"/submit-review", outsiderToken, map[string]any{
		"expected_version": created.Version,
	}), http.StatusForbidden, "not_activity_owner")

	submitted := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, draftPath+"/submit-review", ownerToken, map[string]any{
		"expected_version": created.Version,
	}))
	ownerPendingResponse := perform(t, fixture.router, http.MethodGet, draftPath, ownerToken, nil)
	assertStatusCode(t, ownerPendingResponse, http.StatusOK)
	ownerPending := decodeActivityView(t, ownerPendingResponse)
	if ownerPending.ReviewStatus != activitydomain.ReviewStatusPendingReview {
		t.Fatalf("owner pending activity=%+v", ownerPending)
	}
	assertErrorEnvelope(t, perform(t, fixture.router, http.MethodGet, draftPath, outsiderToken, nil), http.StatusNotFound, "activity_not_found")
	rejected := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(created.ID, 10)+"/reject", fixture.adminToken, map[string]any{
		"expected_version": submitted.Version,
		"review_comment":   "need clearer agenda",
	}))
	if rejected.ReviewStatus != activitydomain.ReviewStatusRejected || rejected.ReviewComment == nil || *rejected.ReviewComment == "" {
		t.Fatalf("rejected activity=%+v", rejected)
	}

	updated := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPatch, "/api/v1/activities/"+strconv.FormatUint(created.ID, 10), ownerToken, map[string]any{
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

	submittedAgain := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/activities/"+strconv.FormatUint(created.ID, 10)+"/submit-review", ownerToken, map[string]any{
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
	earlyCancelled := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/activities/"+strconv.FormatUint(created.ID, 10)+"/cancel", ownerToken, map[string]any{
		"expected_version": published.Version,
	}))
	if earlyCancelled.Status != activitydomain.ActivityStatusCancelled || earlyCancelled.Contact == "99887766" {
		t.Fatalf("cancelled activity=%+v", earlyCancelled)
	}

	second := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/activities", ownerToken, map[string]any{
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
	second = decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/activities/"+strconv.FormatUint(second.ID, 10)+"/submit-review", ownerToken, map[string]any{
		"expected_version": second.Version,
	}))
	second = decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(second.ID, 10)+"/approve", fixture.adminToken, map[string]any{
		"expected_version": second.Version,
	}))
	second = decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/admin/activities/"+strconv.FormatUint(second.ID, 10)+"/publish", ownerToken, map[string]any{
		"expected_version": second.Version,
	}))
	cancelled := decodeActivityView(t, performJSON(t, fixture.router, http.MethodPost, "/api/v1/activities/"+strconv.FormatUint(second.ID, 10)+"/cancel", ownerToken, map[string]any{
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
	return newHandlerFixtureWithLimiter(t, nil)
}

func newHandlerFixtureWithLimiter(t *testing.T, limiter httpapi.AuthLimiter) *handlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &model.Role{}, &model.Config{}, &casbinRule{}, &permissionPolicyOutbox{}, &idempotency.Record{}, &activitydomain.Activity{}, &activitydomain.ActivityRegistration{}, &domainevent.Event{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	permissions, err := permission.NewService(context.Background(), db, platformmysql.NewRoleRepository(db))
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
	authService := auth.NewService(platformmysql.NewUserRepository(db), sessions, "test", []byte("0123456789abcdef0123456789abcdef"), time.Minute, time.Hour, nil, nil)
	users.WithSessionRevoker(authService)
	activities := activityapp.NewManager(activityinfra.NewStore(db, cipher))
	handler := httpapi.New(authService, users, permissions, configcenter.NewService(platformmysql.NewConfigRepository(db), cipher), func(context.Context) error { return nil }, func(context.Context) error { return nil }, zap.NewNop()).
		WithDatabase(db).
		WithActivities(activities).
		WithAcademicVerificationGate(alwaysVerified{})
	if limiter != nil {
		handler.WithAuthLimiter(limiter)
	}
	router, err := handler.Router()
	if err != nil {
		t.Fatal(err)
	}
	return &handlerFixture{
		router: router, handler: handler, users: users, permissions: permissions,
		adminToken: loginToken(t, router, admin.Username, password), userPassword: password,
	}
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

func containsTestString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
		{PathPattern: "/api/v1/activities", Methods: []string{"GET", "POST"}},
		{PathPattern: "/api/v1/activities/mine", Methods: []string{"GET"}},
		{PathPattern: "/api/v1/activities/:id", Methods: []string{"GET", "PATCH"}},
		{PathPattern: "/api/v1/activities/:id/submit-review", Methods: []string{"POST"}},
		{PathPattern: "/api/v1/activities/:id/cancel", Methods: []string{"POST"}},
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
