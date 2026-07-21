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
	if logout.StatusCode != 200 {
		t.Fatalf("logout status=%d", logout.StatusCode)
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
	login := request(t, http.Client{}, http.MethodPost, base+"/api/v1/auth/login", "", map[string]string{"username": username, "password": password})
	var pair tokens
	decodeData(t, login, &pair)
	return base, pair.AccessToken
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
