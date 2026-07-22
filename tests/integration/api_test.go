//go:build integration

package integration

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type envelope struct {
	Data json.RawMessage `json:"data"`
}
type tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type resource struct {
	ID      uint64 `json:"id"`
	Version uint64 `json:"version"`
}

var (
	adminLoginOnce             sync.Once
	adminLoginBase             string
	adminLoginToken            string
	adminLoginErr              error
	integrationRequestSequence atomic.Uint64
	integrationStudentSequence atomic.Uint64
)

func TestAuthenticationLifecycle(t *testing.T) {
	base := os.Getenv("CAMPUS_INTEGRATION_BASE_URL")
	if base == "" {
		t.Skip("CAMPUS_INTEGRATION_BASE_URL is not set")
	}
	username := os.Getenv("CAMPUS_ADMIN_USERNAME")
	password := os.Getenv("CAMPUS_ADMIN_PASSWORD")
	if username == "" || password == "" {
		t.Skip("administrator credentials are not set")
	}
	client := http.Client{}
	health := request(t, client, http.MethodGet, base+"/health/ready", "", nil)
	if health.StatusCode != 200 {
		t.Fatalf("ready status=%d", health.StatusCode)
	}
	login := request(t, client, http.MethodPost, base+"/api/v1/auth/login", "", map[string]string{"username": username, "password": password})
	var wrapper envelope
	decode(t, login, &wrapper)
	var pair tokens
	if err := json.Unmarshal(wrapper.Data, &pair); err != nil {
		t.Fatal(err)
	}
	me := request(t, client, http.MethodGet, base+"/api/v1/auth/me", pair.AccessToken, nil)
	if me.StatusCode != 200 {
		t.Fatalf("me status=%d", me.StatusCode)
	}
	refresh := request(t, client, http.MethodPost, base+"/api/v1/auth/refresh", "", map[string]string{"refresh_token": pair.RefreshToken})
	decode(t, refresh, &wrapper)
	var rotated tokens
	if err := json.Unmarshal(wrapper.Data, &rotated); err != nil {
		t.Fatal(err)
	}
	reused := request(t, client, http.MethodPost, base+"/api/v1/auth/refresh", "", map[string]string{"refresh_token": pair.RefreshToken})
	if reused.StatusCode != 401 {
		t.Fatalf("reused refresh status=%d", reused.StatusCode)
	}
	logout := request(t, client, http.MethodPost, base+"/api/v1/auth/logout", rotated.AccessToken, nil)
	if logout.StatusCode != http.StatusUnauthorized {
		t.Fatalf("logout after family reuse status=%d", logout.StatusCode)
	}
	after := request(t, client, http.MethodGet, base+"/api/v1/auth/me", rotated.AccessToken, nil)
	if after.StatusCode != 401 {
		t.Fatalf("after logout status=%d", after.StatusCode)
	}
}

func TestGeneratedQueryManagementLifecycle(t *testing.T) {
	base, token := integrationAdmin(t)
	client := http.Client{}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	username := "user_" + suffix
	roleName := "role_" + suffix

	createdUser := resource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/users", token, map[string]any{
		"username": username,
		"password": "integration-password",
	}), &createdUser)
	assertStatus(t, request(t, client, http.MethodGet, fmt.Sprintf("%s/api/v1/users/%d", base, createdUser.ID), token, nil), http.StatusOK)
	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/users?page=1&page_size=10", token, nil), http.StatusOK)

	createdRole := resource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/roles", token, map[string]any{
		"name": roleName, "description": "集成测试角色",
	}), &createdRole)
	rolesURL := fmt.Sprintf("%s/api/v1/users/%d/roles", base, createdUser.ID)
	assertStatus(t, request(t, client, http.MethodPut, rolesURL, token, map[string]any{"roles": []string{roleName}}), http.StatusOK)
	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/roles?page=1&page_size=10", token, nil), http.StatusOK)

	createdConfig := resource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/configs", token, map[string]any{
		"group": "integration", "key": "generated_query_" + suffix, "value": "initial", "encrypted": true,
	}), &createdConfig)
	assertEncryptedAtRest(t, createdConfig.ID, "initial")
	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/configs?group=integration&page=1&page_size=10", token, nil), http.StatusOK)
	updatedConfig := resource{}
	decodeData(t, request(t, client, http.MethodPut, fmt.Sprintf("%s/api/v1/configs/%d", base, createdConfig.ID), token, map[string]any{
		"expected_version": createdConfig.Version, "value": "updated",
	}), &updatedConfig)
	if updatedConfig.Version != createdConfig.Version+1 {
		t.Fatalf("config version=%d want=%d", updatedConfig.Version, createdConfig.Version+1)
	}
	assertStatus(t, request(t, client, http.MethodDelete, fmt.Sprintf("%s/api/v1/configs/%d", base, createdConfig.ID), token, nil), http.StatusOK)
	assertStatus(t, request(t, client, http.MethodPut, rolesURL, token, map[string]any{"roles": []string{}}), http.StatusOK)
	assertStatus(t, request(t, client, http.MethodDelete, fmt.Sprintf("%s/api/v1/roles/%d", base, createdRole.ID), token, nil), http.StatusOK)
}

func TestGuestBecomesMemberAfterAcademicVerification(t *testing.T) {
	base, adminToken := integrationAdmin(t)
	client := http.Client{Timeout: 10 * time.Second}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	username := "academic_" + suffix
	created := resource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/users", adminToken, map[string]any{
		"username": username,
		"password": integrationPassword,
	}), &created)
	token := loginWithCredentials(t, base, username, integrationPassword)

	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/activities?page=1&page_size=10", "", nil), http.StatusUnauthorized)
	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/activities?page=1&page_size=10", token, nil), http.StatusOK)
	guestWrite := request(t, client, http.MethodPost, base+"/api/v1/errands", token, map[string]any{
		"title": "访客写入", "description": "认证前必须拒绝", "reward_cents": 300,
		"pickup_location": "东门", "dropoff_location": "图书馆",
		"deadline":     time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"contact_type": "wechat", "contact": "guest-test",
	})
	assertStatus(t, guestWrite, http.StatusForbidden)

	verifyAcademicCredentials(t, client, base, token)
	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/academic-verification", token, nil), http.StatusOK)
	memberWrite := request(t, client, http.MethodPost, base+"/api/v1/errands", token, map[string]any{
		"title": "成员写入", "description": "认证后允许", "reward_cents": 300,
		"pickup_location": "东门", "dropoff_location": "图书馆",
		"deadline":     time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"contact_type": "wechat", "contact": "member-test",
	})
	assertStatus(t, memberWrite, http.StatusCreated)
}

func TestNoticeLifecycleThroughWorker(t *testing.T) {
	base, adminToken := integrationAdmin(t)
	client := http.Client{}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	username := "notice_user_" + suffix
	password := "integration-password"
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/users", adminToken, map[string]any{"username": username, "password": password}), &resource{})
	userToken := loginWithCredentials(t, base, username, password)
	verifyAcademicCredentials(t, client, base, userToken)
	created := resource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/admin/notices", adminToken, map[string]any{
		"title": "Worker 通知", "summary": "异步闭环", "body": "Markdown 正文", "category": "campus", "priority": "important", "action_path": "/pages/notices/detail", "channels": []string{"in_app", "push"}, "audience": map[string]any{"all": true, "roles": []string{}, "user_ids": []uint64{}},
	}), &created)
	published := resource{}
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/notices/%d/publish", base, created.ID), adminToken, map[string]any{"expected_version": created.Version}), &published)
	deadline := time.Now().Add(15 * time.Second)
	for {
		response := request(t, client, http.MethodGet, base+"/api/v1/notices", userToken, nil)
		var page struct {
			Items []resource `json:"items"`
		}
		decodeData(t, response, &page)
		if len(page.Items) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not publish notice before deadline")
		}
		time.Sleep(250 * time.Millisecond)
	}
	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/notices/unread-count", userToken, nil), http.StatusOK)
	assertStatus(t, request(t, client, http.MethodPut, fmt.Sprintf("%s/api/v1/notices/%d/read", base, created.ID), userToken, nil), http.StatusOK)
	var adminDetail struct {
		Notice resource `json:"notice"`
	}
	decodeData(t, request(t, client, http.MethodGet, fmt.Sprintf("%s/api/v1/admin/notices/%d", base, created.ID), adminToken, nil), &adminDetail)
	assertStatus(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/notices/%d/revoke", base, created.ID), adminToken, map[string]any{"expected_version": adminDetail.Notice.Version}), http.StatusOK)
	assertStatus(t, request(t, client, http.MethodGet, fmt.Sprintf("%s/api/v1/notices/%d", base, created.ID), userToken, nil), http.StatusNotFound)
}

func assertEncryptedAtRest(t *testing.T, id uint64, plaintext string) {
	t.Helper()
	dsn := os.Getenv("CAMPUS_INTEGRATION_MYSQL_DSN")
	if dsn == "" {
		t.Skip("CAMPUS_INTEGRATION_MYSQL_DSN is not set")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close MySQL: %v", err)
		}
	}()
	var stored string
	if err = db.QueryRow("SELECT value FROM configs WHERE id = ?", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == plaintext || strings.Contains(stored, plaintext) {
		t.Fatal("encrypted configuration contains plaintext")
	}
}

func integrationAdmin(t *testing.T) (string, string) {
	t.Helper()
	base := os.Getenv("CAMPUS_INTEGRATION_BASE_URL")
	username := os.Getenv("CAMPUS_ADMIN_USERNAME")
	password := os.Getenv("CAMPUS_ADMIN_PASSWORD")
	if base == "" || username == "" || password == "" {
		t.Skip("integration environment is not configured")
	}
	adminLoginOnce.Do(func() {
		adminLoginBase = base
		login := request(t, http.Client{}, http.MethodPost, base+"/api/v1/auth/login", "", map[string]string{"username": username, "password": password})
		if login.StatusCode >= 300 {
			adminLoginErr = fmt.Errorf("administrator login status=%d", login.StatusCode)
			return
		}
		var pair tokens
		var wrapper envelope
		if err := json.NewDecoder(login.Body).Decode(&wrapper); err != nil {
			adminLoginErr = err
			return
		}
		if err := json.Unmarshal(wrapper.Data, &pair); err != nil {
			adminLoginErr = err
			return
		}
		adminLoginToken = pair.AccessToken
	})
	if adminLoginErr != nil || adminLoginBase != base {
		t.Fatalf("cache administrator login: %v", adminLoginErr)
	}
	return base, adminLoginToken
}

func request(t *testing.T, client http.Client, method, url, token string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete {
			req.Header.Set("Idempotency-Key", fmt.Sprintf("integration-%d", integrationRequestSequence.Add(1)))
		}
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}
func decode(t *testing.T, res *http.Response, dst any) {
	t.Helper()
	if res.StatusCode >= 300 {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("status=%d body=%s", res.StatusCode, raw)
	}
	if err := json.NewDecoder(res.Body).Decode(dst); err != nil {
		t.Fatal(fmt.Errorf("decode response: %w", err))
	}
}

func decodeData(t *testing.T, res *http.Response, dst any) {
	t.Helper()
	var wrapper envelope
	decode(t, res, &wrapper)
	if err := json.Unmarshal(wrapper.Data, dst); err != nil {
		t.Fatalf("decode response data: %v", err)
	}
}

func assertStatus(t *testing.T, res *http.Response, want int) {
	t.Helper()
	if res.StatusCode != want {
		raw, _ := io.ReadAll(res.Body)
		t.Fatalf("status=%d want=%d body=%s", res.StatusCode, want, raw)
	}
}

func verifyAcademicCredentials(t *testing.T, client http.Client, base, token string) string {
	t.Helper()
	sequence := integrationStudentSequence.Add(1)
	studentNo := fmt.Sprintf("2026%04d", sequence)
	assertStatus(t, request(t, client, http.MethodPost, base+"/api/v1/academic-verification/credentials", token, map[string]string{
		"student_no": studentNo,
		"password":   "password",
	}), http.StatusOK)
	return studentNo
}
