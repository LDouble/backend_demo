//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"os"
	"testing"
)

const persistencePassword = "persistence-password"

func TestComposePersistenceSeed(t *testing.T) {
	if os.Getenv("CAMPUS_INTEGRATION_PERSISTENCE_PHASE") != "seed" {
		t.Skip("persistence seed phase is not enabled")
	}
	runID := requiredEnv(t, "CAMPUS_INTEGRATION_RUN_ID")
	base, adminToken := integrationAdmin(t)
	client := http.Client{}
	username := "persist_" + runID
	roleName := "reader_" + runID

	createdUser := resource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/users", adminToken, map[string]any{
		"username": username, "password": persistencePassword,
	}), &createdUser)
	userToken := loginWithCredentials(t, base, username, persistencePassword)
	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/configs", userToken, nil), http.StatusForbidden)

	createdRole := resource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/roles", adminToken, map[string]any{
		"name": roleName, "description": "重启持久化读取角色",
	}), &createdRole)
	permissionsURL := fmt.Sprintf("%s/api/v1/roles/%d/permissions", base, createdRole.ID)
	assertStatus(t, request(t, client, http.MethodPut, permissionsURL, adminToken, map[string]any{
		"permissions": []map[string]any{{"path_pattern": "/api/v1/configs", "methods": []string{"GET"}}},
	}), http.StatusOK)
	rolesURL := fmt.Sprintf("%s/api/v1/users/%d/roles", base, createdUser.ID)
	assertStatus(t, request(t, client, http.MethodPut, rolesURL, adminToken, map[string]any{"roles": []string{roleName}}), http.StatusOK)

	// 同一 Access Token 在授权变更后立即生效。
	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/configs", userToken, nil), http.StatusOK)
	assertStatus(t, request(t, client, http.MethodPost, base+"/api/v1/configs", userToken, map[string]any{
		"group": "forbidden", "key": "write", "value": "denied",
	}), http.StatusForbidden)
	assertStatus(t, request(t, client, http.MethodPost, base+"/api/v1/configs", adminToken, map[string]any{
		"group": "persistence", "key": "config_" + runID, "value": "persisted", "encrypted": true,
	}), http.StatusCreated)
}

func TestComposePersistenceVerify(t *testing.T) {
	if os.Getenv("CAMPUS_INTEGRATION_PERSISTENCE_PHASE") != "verify" {
		t.Skip("persistence verify phase is not enabled")
	}
	runID := requiredEnv(t, "CAMPUS_INTEGRATION_RUN_ID")
	base := requiredEnv(t, "CAMPUS_INTEGRATION_BASE_URL")
	client := http.Client{}
	userToken := loginWithCredentials(t, base, "persist_"+runID, persistencePassword)

	// API 重启会重建 Casbin Enforcer；仍可读取说明角色和策略已持久化。
	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/configs?group=persistence", userToken, nil), http.StatusOK)
	assertStatus(t, request(t, client, http.MethodPost, base+"/api/v1/configs", userToken, map[string]any{
		"group": "forbidden", "key": "after_restart", "value": "denied",
	}), http.StatusForbidden)

	_, adminToken := integrationAdmin(t)
	response := request(t, client, http.MethodGet, base+"/api/v1/configs?group=persistence", adminToken, nil)
	var page struct {
		Items []struct {
			Key      string `json:"key"`
			HasValue bool   `json:"has_value"`
		} `json:"items"`
	}
	decodeData(t, response, &page)
	if len(page.Items) != 1 || page.Items[0].Key != "config_"+runID || !page.Items[0].HasValue {
		t.Fatalf("persisted configs=%+v", page.Items)
	}
}

func loginWithCredentials(t *testing.T, base, username, password string) string {
	t.Helper()
	response := request(t, http.Client{}, http.MethodPost, base+"/api/v1/auth/login", "", map[string]string{"username": username, "password": password})
	var pair tokens
	decodeData(t, response, &pair)
	return pair.AccessToken
}
