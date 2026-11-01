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
	f.s.cfg.SuiteAllowedCIDRs = nil
	status, _, policyRaw := f.call(t, "GET", "/api/admin/suite", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, policyRaw)
	if err := json.Unmarshal(policyRaw, &state); err != nil || !state.Configuration.CanManage || state.Configuration.CanEnroll {
		t.Fatal("target policy blocked local key/revocation management", err)
	}
	if strings.Contains(string(raw), f.gateway.ServiceCredential) || strings.Contains(string(raw), f.adminKey) {
		t.Fatal("administration exposed raw credentials")
	}
	admin, authErr := f.s.apiUser(context.Background(), f.adminKey)
	if authErr != nil {
		t.Fatal(authErr)
	}
	session, err := f.store.CreateWebSession(context.Background(), admin.ID, time.Hour)
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
	if err = json.NewDecoder(res.Body).Decode(&state); err != nil || state.Configuration.CanManage {
		t.Fatal("forged transport enabled Suite administration", err)
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
	session, err := f.store.CreateWebSession(ctx, admin.ID, time.Hour)
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
