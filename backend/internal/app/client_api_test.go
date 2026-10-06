package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/mapherez/nox-backend/backend/internal/storage"
)

func TestInfoContract(t *testing.T) {
	// Info must work without a database and ignore credentials entirely.
	server := NewServer(Config{Version: "contract-test"}, nil)
	for _, key := range []string{"", "invalid"} {
		res := readRequest(server, key, http.MethodGet, "/v1/info", nil)
		assertClientResponse(t, res, http.StatusOK, "")
		var got map[string]any
		decodeReadJSON(t, res, &got)
		want := map[string]any{
			"service": "nox-backend", "version": "contract-test", "apiVersion": "v1",
			"capabilities": []any{"auth", "vaults", "files", "vault-status"},
		}
		for field, value := range want {
			if gotValue, exists := got[field]; !exists || !reflect.DeepEqual(gotValue, value) {
				t.Fatalf("info field %q: %#v; want %#v", field, got[field], value)
			}
		}
	}
}

func TestHealthContractAndSharedVersion(t *testing.T) {
	server, store, _, _ := newTestServer(t)
	server.cfg.Version = "runtime-test"
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		res := readRequest(server, "invalid", method, "/v1/health", nil)
		assertClientResponse(t, res, http.StatusOK, "")
		var got map[string]any
		decodeReadJSON(t, res, &got)
		want := map[string]any{
			"status": "ready", "version": "runtime-test",
			"dataDirInitialized": true, "databasePath": store.DBPath(),
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("health contract for %s: %#v", method, got)
		}
		var info map[string]any
		decodeReadJSON(t, readRequest(server, "", http.MethodGet, "/v1/info", nil), &info)
		if info["version"] != got["version"] {
			t.Fatalf("info and health versions differ: %#v, %#v", info, got)
		}
	}
}

func TestClientAPIMethods(t *testing.T) {
	server, store, user, _ := newTestServer(t)
	key := readTestKey(t, store, user.ID)
	for _, tc := range []struct{ target, allow string }{
		{"/v1/info", "GET"}, {"/v1/auth/check", "GET"},
		{"/v1/vaults", "GET, POST, DELETE"},
		{"/v1/vaults/restore", "POST"}, {"/v1/vaults/purge", "POST"},
		{"/v1/status", "GET"}, {"/v1/files", "GET"}, {"/v1/files/download", "GET"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
			if strings.Contains(", "+tc.allow+", ", ", "+method+", ") {
				continue
			}
			t.Run(method+tc.target, func(t *testing.T) {
				res := readRequest(server, key, method, tc.target, nil)
				assertClientResponse(t, res, http.StatusMethodNotAllowed, "BAD_REQUEST")
				if res.Header().Get("Allow") != tc.allow {
					t.Fatalf("Allow = %q; want %q", res.Header().Get("Allow"), tc.allow)
				}
			})
		}
	}
	// Preserve legacy authentication-before-method ordering on /v1/vaults.
	assertClientResponse(t, readRequest(server, "", http.MethodPut, "/v1/vaults", nil), http.StatusUnauthorized, "AUTH_REQUIRED")
}

func TestClientAPIAuthentication(t *testing.T) {
	server, store, _, vault := newTestServer(t)
	ctx := context.Background()
	user, err := store.UpsertAllowedUser(ctx, "client@example.com", storage.UserRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	key := readTestKey(t, store, user.ID)
	targets := []struct{ method, target string }{
		{"GET", "/v1/auth/check"}, {"GET", "/v1/vaults"}, {"POST", "/v1/vaults"},
		{"DELETE", "/v1/vaults?vaultId=" + vault.ID},
		{"POST", "/v1/vaults/restore?vaultId=" + vault.ID}, {"POST", "/v1/vaults/purge?vaultId=" + vault.ID},
		{"GET", "/v1/status?vaultId=" + vault.ID}, {"GET", "/v1/files?vaultId=" + vault.ID},
		{"GET", "/v1/files/download?vaultId=" + vault.ID + "&path=note.md"},
	}
	check := func(key, code string) {
		t.Helper()
		for _, target := range targets {
			assertClientResponse(t, readRequest(server, key, target.method, target.target, nil), http.StatusUnauthorized, code)
		}
	}
	check("", "AUTH_REQUIRED")
	check("invalid", "AUTH_FAILED")
	newKey, err := store.RotateAPIKey(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	check(key, "AUTH_FAILED")
	assertClientResponse(t, readRequest(server, newKey, "GET", "/v1/auth/check", nil), http.StatusOK, "")
	if err := store.SetUserStatus(ctx, user.ID, storage.UserStatusDisabled); err != nil {
		t.Fatal(err)
	}
	check(newKey, "AUTH_FAILED")
}

func TestClientIdentityAndLegacyQueryKey(t *testing.T) {
	server, store, user, _ := newTestServer(t)
	key := readTestKey(t, store, user.ID)
	for _, tc := range []struct{ key, target string }{
		{key, "/v1/auth/check"}, {"", "/v1/auth/check?api_key=" + url.QueryEscape(key)},
		{key, "/v1/auth/check?api_key=invalid"},
	} {
		res := readRequest(server, tc.key, "GET", tc.target, nil)
		assertClientResponse(t, res, http.StatusOK, "")
		var got map[string]any
		decodeReadJSON(t, res, &got)
		want := map[string]any{"ok": true, "user": user.Email, "role": "ADMIN", "vault": "selected-in-settings"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("identity contract: %#v", got)
		}
	}
	assertClientResponse(t, readRequest(server, "invalid", "GET", "/v1/auth/check?api_key="+url.QueryEscape(key), nil), http.StatusUnauthorized, "AUTH_FAILED")
}

func TestClientVaultLifecycleContract(t *testing.T) {
	server, store, user, _ := newTestServer(t)
	key := readTestKey(t, store, user.ID)
	created := readRequest(server, key, "POST", "/v1/vaults", strings.NewReader(`{"name":"Client Vault"}`))
	assertClientResponse(t, created, http.StatusCreated, "")
	var vault map[string]any
	decodeReadJSON(t, created, &vault)
	if len(vault) != 6 || vault["name"] != "Client Vault" || vault["revision"] != float64(0) || vault["status"] != "ACTIVE" || vault["sizeBytes"] != float64(0) {
		t.Fatalf("created vault contract: %#v", vault)
	}
	id, ok := vault["vaultId"].(string)
	if !ok || !strings.HasPrefix(id, "vault_") {
		t.Fatalf("vault ID: %#v", vault)
	}
	if updated, ok := vault["updatedAt"].(string); !ok || updated == "" {
		t.Fatalf("updatedAt: %#v", vault)
	}
	for _, action := range []struct{ method, target string }{
		{"DELETE", "/v1/vaults"}, {"POST", "/v1/vaults/restore"},
		{"DELETE", "/v1/vaults"}, {"POST", "/v1/vaults/purge"},
	} {
		res := readRequest(server, key, action.method, action.target+"?vaultId="+id, nil)
		assertClientResponse(t, res, http.StatusOK, "")
		var got map[string]any
		decodeReadJSON(t, res, &got)
		if !reflect.DeepEqual(got, map[string]any{"ok": true}) {
			t.Fatalf("lifecycle response: %#v", got)
		}
		list := readRequest(server, key, "GET", "/v1/vaults", nil)
		assertClientResponse(t, list, http.StatusOK, "")
		var payload map[string]any
		decodeReadJSON(t, list, &payload)
		if action.method == "DELETE" {
			deleted, ok := payload["deletedVaults"].([]any)
			if !ok || len(deleted) != 1 {
				t.Fatalf("deleted vault list: %#v", payload)
			}
			item := deleted[0].(map[string]any)
			deletedAt, ok := item["deletedAt"].(string)
			if len(item) != 7 || item["vaultId"] != id || item["status"] != "DELETED" || !ok || deletedAt == "" {
				t.Fatalf("deleted vault contract: %#v", payload)
			}
			assertClientResponse(t, readRequest(server, key, "GET", "/v1/status?vaultId="+id, nil), http.StatusNotFound, "NOT_FOUND")
		} else if _, exists := payload["deletedVaults"]; exists {
			t.Fatalf("empty deletedVaults should be omitted: %#v", payload)
		}
	}
	assertClientResponse(t, readRequest(server, key, "GET", "/v1/status?vaultId="+id, nil), http.StatusNotFound, "NOT_FOUND")
	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"ok","extra":1}`, `{"name":"ok"} {}`, `invalid`} {
		assertClientResponse(t, readRequest(server, key, "POST", "/v1/vaults", strings.NewReader(body)), http.StatusBadRequest, "BAD_REQUEST")
	}
	for _, target := range []struct{ method, path string }{
		{"DELETE", "/v1/vaults"}, {"POST", "/v1/vaults/restore"}, {"POST", "/v1/vaults/purge"}, {"GET", "/v1/status"},
	} {
		assertClientResponse(t, readRequest(server, key, target.method, target.path, nil), http.StatusBadRequest, "BAD_REQUEST")
		assertClientResponse(t, readRequest(server, key, target.method, target.path+"?vaultId=vault_missing", nil), http.StatusNotFound, "NOT_FOUND")
	}
}

func TestClientEmptyVaultList(t *testing.T) {
	server, store, _, _ := newTestServer(t)
	user, err := store.UpsertAllowedUser(context.Background(), "empty-client@example.com", storage.UserRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	key := readTestKey(t, store, user.ID)
	res := readRequest(server, key, "GET", "/v1/vaults", nil)
	assertClientResponse(t, res, http.StatusOK, "")
	var got map[string]any
	decodeReadJSON(t, res, &got)
	if !reflect.DeepEqual(got, map[string]any{"vaults": []any{}}) {
		t.Fatalf("empty vault list contract: %#v", got)
	}
}

func TestClientVaultOwnershipAndStatus(t *testing.T) {
	server, store, admin, adminVault := newTestServer(t)
	ctx := context.Background()
	other, err := store.UpsertAllowedUser(ctx, "other-client@example.com", storage.UserRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	otherVault, err := store.CreateVault(ctx, other.ID, "Other")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user         storage.User
		own, foreign storage.Vault
	}{{admin, adminVault, otherVault}, {other, otherVault, adminVault}} {
		key := readTestKey(t, store, tc.user.ID)
		res := readRequest(server, key, "GET", "/v1/status?vaultId="+tc.own.ID, nil)
		assertClientResponse(t, res, http.StatusOK, "")
		var got map[string]any
		decodeReadJSON(t, res, &got)
		want := map[string]any{
			"vaultId": tc.own.ID, "serverRevision": float64(0),
			"sync": map[string]any{"state": "IDLE", "sessionId": "", "clientId": "", "clientName": "", "startedAt": ""},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("status contract: %#v", got)
		}
		assertClientResponse(t, readRequest(server, key, "GET", "/v1/status?vaultId="+tc.foreign.ID, nil), http.StatusNotFound, "NOT_FOUND")
		assertClientResponse(t, readRequest(server, key, "DELETE", "/v1/vaults?vaultId="+tc.foreign.ID, nil), http.StatusNotFound, "NOT_FOUND")
		if err := store.SoftDeleteVault(ctx, tc.foreign.UserID, tc.foreign.ID); err != nil {
			t.Fatal(err)
		}
		for _, target := range []string{"/v1/vaults/restore", "/v1/vaults/purge"} {
			assertClientResponse(t, readRequest(server, key, "POST", target+"?vaultId="+tc.foreign.ID, nil), http.StatusNotFound, "NOT_FOUND")
		}
		var listed storage.VaultListResponse
		listRes := readRequest(server, key, "GET", "/v1/vaults", nil)
		assertClientResponse(t, listRes, http.StatusOK, "")
		decodeReadJSON(t, listRes, &listed)
		if len(listed.Vaults) != 1 || listed.Vaults[0].ID != tc.own.ID || len(listed.DeletedVaults) != 0 {
			t.Fatalf("vault list ownership: %#v", listed)
		}
		if err := store.RestoreVault(ctx, tc.foreign.UserID, tc.foreign.ID); err != nil {
			t.Fatal(err)
		}
	}
	key := readTestKey(t, store, admin.ID)
	session, err := store.BeginSync(ctx, admin.ID, storage.BeginSyncRequest{VaultID: adminVault.ID, ClientID: "client-test", ClientName: "Existing Sync"})
	if err != nil {
		t.Fatal(err)
	}
	res := readRequest(server, key, "GET", "/v1/status?vaultId="+adminVault.ID, nil)
	assertClientResponse(t, res, http.StatusOK, "")
	var payload struct {
		Sync map[string]any `json:"sync"`
	}
	decodeReadJSON(t, res, &payload)
	startedAt, ok := payload.Sync["startedAt"].(string)
	if len(payload.Sync) != 5 || payload.Sync["state"] != "SYNCING" || payload.Sync["sessionId"] != session.SessionID || payload.Sync["clientId"] != "client-test" || payload.Sync["clientName"] != "Existing Sync" || !ok || startedAt == "" {
		t.Fatalf("active status contract: %#v", payload.Sync)
	}
}

func assertClientResponse(t *testing.T, res *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if res.Code != status || res.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response: %d %v %s; want %d JSON", res.Code, res.Header(), res.Body, status)
	}
	if code != "" {
		var payload map[string]any
		decodeReadJSON(t, res, &payload)
		message, ok := payload["message"].(string)
		if len(payload) != 2 || payload["code"] != code || !ok || message == "" {
			t.Fatalf("error contract: %#v; want code %s and message", payload, code)
		}
	}
}
