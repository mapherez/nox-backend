package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
)

func administrationRequest(t *testing.T, s *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	s.Routes().ServeHTTP(response, request)
	return response
}
func administrationKey(t *testing.T, store *storage.Store, user storage.User) map[string]string {
	t.Helper()
	key, err := store.CurrentAPIKey(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + key}
}
func administrationStatus(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("expected %d, got %d: %s", status, response.Code, response.Body)
	}
	if response.Header().Get("Location") != "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("API redirected or cached an administration response")
	}
	if response.Header().Get("Content-Type") != "application/json" || !json.Valid(response.Body.Bytes()) {
		t.Fatal("API response is not JSON", response.Body.String())
	}
}

func TestAdministrationAuthenticationAndJSONBoundary(t *testing.T) {
	s, store, admin, _ := newTestServer(t)
	key := administrationKey(t, store, admin)
	for _, path := range []string{"/api/account", "/api/account/api-key", "/api/admin/users", "/api/admin/connections", "/api/vaults"} {
		administrationStatus(t, administrationRequest(t, s, "GET", path, "", nil), 401)
	}
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/admin/users?api_key="+strings.TrimPrefix(key["Authorization"], "Bearer "), "", nil), 401)
	session, err := store.CreateWebSession(context.Background(), admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cookie := map[string]string{"Cookie": sessionCookieName + "=" + session}
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", cookie), 200)
	for _, authorization := range []string{"Bearer invalid", "Basic anything", ""} {
		administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", map[string]string{"Cookie": cookie["Cookie"], "Authorization": authorization}), 401)
	}
	administrationStatus(t, administrationRequest(t, s, "PATCH", "/api/admin/users", "", key), 405)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/unknown", "", key), 404)
	expired, err := store.CreateWebSession(context.Background(), admin.ID, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", map[string]string{"Cookie": sessionCookieName + "=" + expired}), 401)
}

func TestAdministrationSessionMutationRequiresSameOrigin(t *testing.T) {
	s, store, admin, _ := newTestServer(t)
	s.cfg.PublicURL = "https://backend.example.com"
	token, err := store.CreateWebSession(context.Background(), admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, header, origin, site string
		status                     int
	}{
		{"form", "", "", "", 403},
		{"foreign origin", "1", "https://evil.example.com", "", 403},
		{"same site is not same origin", "1", "", "same-site", 403},
		{"null origin", "1", "null", "", 403},
		{"same origin", "1", "https://backend.example.com", "same-origin", 200},
		{"non browser session API", "1", "", "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := administrationRequest(t, s, "POST", "/api/admin/users", `{"email":"added@example.com","role":"USER"}`, map[string]string{"Cookie": sessionCookieName + "=" + token, "X-Nox-Request": tc.header, "Origin": tc.origin, "Sec-Fetch-Site": tc.site})
			administrationStatus(t, response, tc.status)
		})
	}
	// A request claiming a different origin cannot log out or rotate keys either.
	administrationStatus(t, administrationRequest(t, s, "POST", "/api/session/logout", "", map[string]string{"Cookie": sessionCookieName + "=" + token, "X-Nox-Request": "1", "Origin": "https://evil.example.com"}), 403)
}

func TestAdministrationAccountAndUserLifecycle(t *testing.T) {
	s, store, admin, _ := newTestServer(t)
	ctx := context.Background()
	auth := administrationKey(t, store, admin)
	response := administrationRequest(t, s, "POST", "/api/admin/users", `{"email":" Member@example.com ","role":"USER"}`, auth)
	administrationStatus(t, response, 200)
	var member dashboardUser
	if err := json.Unmarshal(response.Body.Bytes(), &member); err != nil {
		t.Fatal(err)
	}
	if member.Email != "member@example.com" || strings.Contains(response.Body.String(), "googleSub") || strings.Contains(response.Body.String(), "noxsync_") {
		t.Fatal("invalid administrative user projection")
	}
	memberUser, err := store.UserByID(ctx, member.ID)
	if err != nil {
		t.Fatal(err)
	}
	memberAuth := administrationKey(t, store, memberUser)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/admin/users", "", memberAuth), 403)
	administrationStatus(t, administrationRequest(t, s, "PUT", "/api/admin/users/"+admin.ID+"/role", `{"role":"USER"}`, memberAuth), 403)
	response = administrationRequest(t, s, "GET", "/api/account?userId="+admin.ID, "", memberAuth)
	administrationStatus(t, response, 200)
	if !strings.Contains(response.Body.String(), member.ID) {
		t.Fatal("account identity was supplied by the caller")
	}
	memberSession, err := store.CreateWebSession(ctx, member.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	administrationStatus(t, administrationRequest(t, s, "PUT", "/api/admin/users/"+member.ID+"/status", `{"status":"DISABLED"}`, auth), 200)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", memberAuth), 401)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", map[string]string{"Cookie": sessionCookieName + "=" + memberSession}), 401)
	administrationStatus(t, administrationRequest(t, s, "PUT", "/api/admin/users/"+member.ID+"/status", `{"status":"ACTIVE"}`, auth), 200)
	administrationStatus(t, administrationRequest(t, s, "PUT", "/api/admin/users/"+member.ID+"/role", `{"role":"ADMIN"}`, auth), 200)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/admin/users", "", memberAuth), 200)
	// The upsert route and case variants obey the same protection as dedicated routes.
	for _, body := range []string{`{"email":"test@example.com","role":"USER"}`, `{"email":"member@example.com","role":"USER"}`} {
		administrationStatus(t, administrationRequest(t, s, "POST", "/api/admin/users", body, auth), 400)
	}
	for _, id := range []string{admin.ID, member.ID} {
		administrationStatus(t, administrationRequest(t, s, "PUT", "/api/admin/users/"+id+"/status", `{"status":" disabled "}`, auth), 400)
		administrationStatus(t, administrationRequest(t, s, "PUT", "/api/admin/users/"+id+"/role", `{"role":" user "}`, auth), 400)
		administrationStatus(t, administrationRequest(t, s, "DELETE", "/api/admin/users/"+id, "", auth), 400)
	}
	target, err := store.UpsertAllowedUser(ctx, "delete@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	targetAuth := administrationKey(t, store, target)
	if _, err := store.CreateVault(ctx, target.ID, "Owned vault"); err != nil {
		t.Fatal(err)
	}
	administrationStatus(t, administrationRequest(t, s, "DELETE", "/api/admin/users/"+target.ID, "", auth), 200)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", targetAuth), 401)
	administrationStatus(t, administrationRequest(t, s, "DELETE", "/api/admin/users/missing", "", auth), 404)
}

func TestAdministrationRejectsMalformedMutationsWithoutChangingState(t *testing.T) {
	s, store, admin, _ := newTestServer(t)
	auth := administrationKey(t, store, admin)
	for _, body := range []string{`null`, `[]`, `{`, `{} {}`, `{"email":"new@example.com","role":"ROOT"}`, `{"email":"new@example.com","role":"USER","isAdmin":true}`} {
		administrationStatus(t, administrationRequest(t, s, "POST", "/api/admin/users", body, auth), 400)
	}
	response := administrationRequest(t, s, "POST", "/api/admin/users", `email=new@example.com`, map[string]string{"Authorization": auth["Authorization"], "Content-Type": "application/x-www-form-urlencoded"})
	administrationStatus(t, response, 415)
	administrationStatus(t, administrationRequest(t, s, "POST", "/api/admin/users", `{"email":"`+strings.Repeat("a", 65536)+`","role":"USER"}`, auth), 413)
	users, err := store.ListUsers(context.Background())
	if err != nil || len(users) != 1 {
		t.Fatal("malformed mutation changed users", err)
	}
}

func TestAdministrationPersonalKeysAndLogout(t *testing.T) {
	s, store, admin, _ := newTestServer(t)
	ctx := context.Background()
	member, err := store.UpsertAllowedUser(ctx, "member@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	memberAuth := administrationKey(t, store, member)
	adminAuth := administrationKey(t, store, admin)
	response := administrationRequest(t, s, "GET", "/api/account/api-key?userId="+admin.ID, "", memberAuth)
	administrationStatus(t, response, 200)
	if !strings.Contains(response.Body.String(), strings.TrimPrefix(memberAuth["Authorization"], "Bearer ")) || strings.Contains(response.Body.String(), strings.TrimPrefix(adminAuth["Authorization"], "Bearer ")) {
		t.Fatal("personal key isolation failed")
	}
	token, err := store.CreateWebSession(ctx, member.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	session := map[string]string{"Cookie": sessionCookieName + "=" + token, "X-Nox-Request": "1"}
	administrationStatus(t, administrationRequest(t, s, "POST", "/api/account/api-key/rotate", "", session), 200)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", memberAuth), 401)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", session), 200)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", adminAuth), 200)
	administrationStatus(t, administrationRequest(t, s, "POST", "/api/session/logout", "", session), 200)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", session), 401)
}

func TestAdministrationVaultOwnershipLifecycleAndDownload(t *testing.T) {
	s, store, admin, vault := newTestServer(t)
	ctx := context.Background()
	member, err := store.UpsertAllowedUser(ctx, "member@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	own, err := store.CreateVault(ctx, member.ID, "Private")
	if err != nil {
		t.Fatal(err)
	}
	auth := administrationKey(t, store, admin)
	memberAuth := administrationKey(t, store, member)
	for _, request := range []struct{ method, path string }{
		{"DELETE", ""}, {"POST", "/restore"}, {"DELETE", "/purge"}, {"GET", "/download"},
	} {
		// Even an administrator has no right to another account's vaults.
		administrationStatus(t, administrationRequest(t, s, request.method, "/api/vaults/"+own.ID+request.path, "", auth), 404)
		administrationStatus(t, administrationRequest(t, s, request.method, "/api/vaults/"+vault.ID+request.path, "", memberAuth), 404)
	}
	response := administrationRequest(t, s, "GET", "/api/vaults/"+own.ID+"/download", "", memberAuth)
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/zip" || response.Header().Get("Location") != "" {
		t.Fatal("download did not return a ZIP")
	}
	if _, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len())); err != nil {
		t.Fatal(err)
	}
	response = administrationRequest(t, s, "GET", "/api/vaults", "", memberAuth)
	administrationStatus(t, response, 200)
	if strings.Contains(response.Body.String(), vault.ID) || !strings.Contains(response.Body.String(), `"revision":"0"`) {
		t.Fatal("vault list leaked ownership or lost decimal representation")
	}
	administrationStatus(t, administrationRequest(t, s, "DELETE", "/api/vaults/"+own.ID, "", memberAuth), 200)
	administrationStatus(t, administrationRequest(t, s, "POST", "/api/vaults/"+own.ID+"/restore", "", memberAuth), 200)
	administrationStatus(t, administrationRequest(t, s, "DELETE", "/api/vaults/"+own.ID, "", memberAuth), 200)
	administrationStatus(t, administrationRequest(t, s, "DELETE", "/api/vaults/"+own.ID+"/purge", "", memberAuth), 200)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/vaults/"+own.ID+"/download", "", memberAuth), 404)
}

func TestFoundationConnectionConfigurationIsNotTrust(t *testing.T) {
	s, store, admin, _ := newTestServer(t)
	ctx := context.Background()
	auth := administrationKey(t, store, admin)
	member, err := store.UpsertAllowedUser(ctx, "member@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	memberAuth := administrationKey(t, store, member)
	administrationStatus(t, administrationRequest(t, s, "GET", "/api/admin/connections", "", memberAuth), 403)
	response := administrationRequest(t, s, "GET", "/api/admin/connections", "", auth)
	administrationStatus(t, response, 200)
	if strings.Count(response.Body.String(), "not_configured") != 2 {
		t.Fatal("wrong initial state")
	}
	path := "/api/admin/connections/gateway"
	administrationStatus(t, administrationRequest(t, s, "PUT", path, `{"origin":"https://gateway.example.com"}`, auth), 428)
	create := map[string]string{"Authorization": auth["Authorization"], "If-None-Match": "*"}
	for _, origin := range []string{"http://gateway.example.com", "https://gateway.example.com/apps/gw", "https://gateway.example.com?api_key=secret", "https://user:password@gateway.example.com", "https://gateway.example.com#fragment", "https://gateway.example.com:0", "https://gateway.example.com/%2e%2e"} {
		administrationStatus(t, administrationRequest(t, s, "PUT", path, fmt.Sprintf(`{"origin":%q}`, origin), create), 400)
	}
	memberCreate := map[string]string{"Authorization": memberAuth["Authorization"], "If-None-Match": "*"}
	administrationStatus(t, administrationRequest(t, s, "PUT", path, `{"origin":"https://gateway.example.com"}`, memberCreate), 403)
	response = administrationRequest(t, s, "PUT", path, `{"origin":"https://GATEWAY.example.com:443/"}`, create)
	administrationStatus(t, response, 200)
	var saved connectionView
	if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Status != "not_paired" || saved.Origin != "https://gateway.example.com" || saved.Revision == "" {
		t.Fatal("saving a URL must not establish trust", saved)
	}
	revision := response.Header().Get("ETag")
	administrationStatus(t, administrationRequest(t, s, "PUT", path, `{"origin":"https://replacement.example.com"}`, create), 412)
	update := map[string]string{"Authorization": auth["Authorization"], "If-Match": revision}
	response = administrationRequest(t, s, "PUT", path, `{"origin":"https://192.168.10.10:8443"}`, update)
	administrationStatus(t, response, 200)
	administrationStatus(t, administrationRequest(t, s, "DELETE", path, "", update), 412)
	update["If-Match"] = response.Header().Get("ETag")
	administrationStatus(t, administrationRequest(t, s, "DELETE", path, "", update), 200)
	administrationStatus(t, administrationRequest(t, s, "DELETE", path, "", update), 412)
	// There are no dummy registry, pairing, or authorization implementations in point 3.
	for _, path := range []string{"/api/nox/pairings", "/api/nox/authorizations", "/api/nox/registry"} {
		administrationStatus(t, administrationRequest(t, s, "POST", path, `{}`, auth), 404)
	}
}

func TestLegacyBrowserFormsKeepRoutesAndRejectForeignOrigin(t *testing.T) {
	s, store, admin, _ := newTestServer(t)
	s.cfg.PublicURL = "https://backend.example.com:443"
	token, err := store.CreateWebSession(context.Background(), admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		origin string
		status int
	}{{"https://foreign.example.com", 403}, {"https://backend.example.com", 303}} {
		response := administrationRequest(t, s, "POST", "/vault-dashboard/users/add", "email=legacy%40example.com&role=USER", map[string]string{"Cookie": sessionCookieName + "=" + token, "Content-Type": "application/x-www-form-urlencoded", "Origin": tc.origin})
		if response.Code != tc.status {
			t.Fatalf("legacy form: %d %s", response.Code, response.Body)
		}
	}
}
func TestAdministrationLogoutCannotUseBearerToDeleteAnotherSession(t *testing.T) {
	s, store, admin, _ := newTestServer(t)
	auth := administrationKey(t, store, admin)
	token, err := store.CreateWebSession(context.Background(), admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	auth["Cookie"] = sessionCookieName + "=" + token
	administrationStatus(t, administrationRequest(t, s, "POST", "/api/session/logout", "", auth), 400)
	if _, valid, err := store.UserBySessionToken(context.Background(), token); err != nil || !valid {
		t.Fatal("Bearer endpoint deleted a session", err)
	}
}
