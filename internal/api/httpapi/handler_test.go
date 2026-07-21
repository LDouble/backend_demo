package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/api/httpapi"
	"github.com/weouc-plus/campus-platform/internal/core/auth"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	platformmysql "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type handlerFixture struct {
	router       http.Handler
	users        *user.Service
	adminToken   string
	userPassword string
}

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

func newHandlerFixture(t *testing.T) *handlerFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &model.Role{}, &model.Config{}); err != nil {
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
	handler := httpapi.New(authService, users, permissions, configcenter.NewService(platformmysql.NewConfigRepository(db), cipher), func(context.Context) error { return nil }, func(context.Context) error { return nil }, zap.NewNop())
	router, err := handler.Router()
	if err != nil {
		t.Fatal(err)
	}
	return &handlerFixture{router: router, users: users, adminToken: loginToken(t, router, admin.Username, password), userPassword: password}
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

func perform(t *testing.T, router http.Handler, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
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
