package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSuiteNativeAdministrationAccessAndTransportProjection(t *testing.T) {
	f := backendSuiteServer(t)
	for _, key := range []string{"", f.memberKey, f.gateway.ServiceCredential} {
		status, _, raw := f.call(t, "GET", "/api/admin/suite", nil, key, nil)
		if status != 401 && status != 403 {
			t.Fatal("admin view accepted wrong authority", status, string(raw))
		}
	}
	status, headers, raw := f.call(t, "GET", "/api/admin/suite", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	if headers.Get("Cache-Control") != "no-store" {
		t.Fatal("administration cache leak")
	}
	var state struct {
		Configuration struct {
			CanManage bool `json:"canManage"`
			CanEnroll bool `json:"canEnroll"`
		}
		State struct {
			Products []any `json:"products"`
			Services []any `json:"services"`
		}
	}
	if err := json.Unmarshal(raw, &state); err != nil || !state.Configuration.CanManage || len(state.State.Products) != 1 || len(state.State.Services) != 2 {
		t.Fatal("incomplete native administration", err, string(raw))
	}
	status, _, policyRaw := f.call(t, "GET", "/api/admin/suite", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, policyRaw)
	if err := json.Unmarshal(policyRaw, &state); err != nil || !state.Configuration.CanManage || !state.Configuration.CanEnroll {
		t.Fatal("local channel did not enable administration", err)
	}
	if strings.Contains(string(raw), f.gateway.ServiceCredential) || strings.Contains(string(raw), f.adminKey) {
		t.Fatal("administration exposed raw credentials")
	}
	admin, authErr := f.s.apiUser(context.Background(), f.adminKey)
	if authErr != nil {
		t.Fatal(authErr)
	}
	session, err := fixtureWebSession(t, context.Background(), f.store, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "GET", "/api/admin/suite", nil, "", map[string]string{"Cookie": sessionCookieName + "=" + session})
	suiteHTTPStatus(t, status, 200, raw)
	insecure := httptest.NewServer(f.s.Routes())
	defer insecure.Close()
	req, _ := http.NewRequest("GET", insecure.URL+"/api/admin/suite", nil)
	req.Header.Set("Authorization", "Bearer "+f.adminKey)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Nox-Suite-Transport", "https")
	res, err := insecure.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if err = json.NewDecoder(res.Body).Decode(&state); err != nil || !state.Configuration.CanManage {
		t.Fatal("native administrator could not manage local integration", err)
	}
	status, _, raw = f.call(t, "POST", "/api/admin/suite", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 405, raw)
}
func TestDashboardPreservesNativeControlsWhenSuiteStateFails(t *testing.T) {
	f := suiteHTTPServer(t)
	ctx := context.Background()
	admin, authErr := f.s.apiUser(ctx, f.adminKey)
	if authErr != nil {
		t.Fatal(authErr)
	}
	if _, err := f.store.SaveFoundationConnection(ctx, "gateway", "https://gateway.example.com", ""); err != nil {
		t.Fatal(err)
	}
	vault, err := f.store.CreateVault(ctx, admin.ID, "native survives")
	if err != nil {
		t.Fatal(err)
	}
	session, err := fixtureWebSession(t, ctx, f.store, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", f.dir+"/nox-backend.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER fixture_unavailable_suite BEFORE UPDATE ON suite_authority BEGIN SELECT RAISE(ABORT,'private injected Suite failure'); END`); err != nil {
		t.Fatal(err)
	}
	status, _, raw := f.call(t, "GET", "/api/dashboard", nil, "", map[string]string{"Cookie": sessionCookieName + "=" + session})
	suiteHTTPStatus(t, status, 200, raw)
	var data dashboardPageData
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if !data.Authenticated || !data.IsAdmin || data.APIKey != f.adminKey || len(data.Vaults) != 1 || data.Vaults[0].ID != vault.ID || data.IntegrationError == "" {
		t.Fatal("Suite outage broke native dashboard", string(raw))
	}
	status, _, raw = f.call(t, "GET", "/api/admin/suite", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 503, raw)
	if strings.Contains(string(raw), "private injected") {
		t.Fatal("private storage error exposed")
	}
	// Ordinary native writes remain available while Suite fails closed.
	status, _, raw = f.call(t, "POST", "/v1/vaults", map[string]string{"name": "native still writes"}, f.adminKey, nil)
	suiteHTTPStatus(t, status, 201, raw)
}

func TestBrowserSuiteAdapterNeverReturnsParticipantSecrets(t *testing.T) {
	f := suiteHTTPServer(t)
	auth := map[string]string{"Authorization": "Bearer " + f.adminKey}
	for _, tc := range []struct{ method, operation string }{
		{"POST", "pairings"}, {"GET", "pairings/existing"},
		{"POST", "pairings/existing/complete"}, {"POST", "pairings/existing/ack"},
		{"POST", "services/existing/credentials"},
		{"POST", "services/existing/credentials/generation/ack"},
		{"POST", "authorizations"}, {"POST", "service-authorizations"},
	} {
		t.Run(tc.method+" "+tc.operation, func(t *testing.T) {
			res := administrationRequest(t, f.s, tc.method, "/api/admin/suite/operations/"+tc.operation, `{}`, auth)
			if res.Code != 403 || strings.Contains(res.Body.String(), "noxsvc_") {
				t.Fatal("browser adapter admitted a participant operation", res.Code, res.Body.String())
			}
		})
	}
	res := administrationRequest(t, f.s, "GET", "/api/admin/suite/operations/keys", "", auth)
	administrationStatus(t, res, 200)
	keys, err := f.store.SuiteKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	auth["If-Match"] = `"` + keys.Revision + `"`
	auth["Idempotency-Key"] = "browser-non-secret-key-prepare"
	res = administrationRequest(t, f.s, "POST", "/api/admin/suite/operations/keys", "", auth)
	administrationStatus(t, res, 201)
}
