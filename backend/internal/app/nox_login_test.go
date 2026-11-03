package app

import (
	"context"
	"encoding/json"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNoXBrowserRoundTripUsesExistingGoogleOwnerAndPreservesKeys(t *testing.T) {
	server, data, user, vault := newTestServer(t)
	user, err := data.CompleteGoogleLogin(context.Background(), storage.OAuthProfile{Sub: "fixture-google-subject", Email: user.Email, DisplayName: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	// The controlled provider returns this verified email for the OIDC flow.
	user, err = data.CompleteGoogleLogin(context.Background(), storage.OAuthProfile{Sub: user.GoogleSub, Email: "owner@example.test", DisplayName: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := data.CurrentAPIKey(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(server.Routes())
	defer public.Close()
	server.cfg.PublicURL = public.URL
	setFixtureOrigin(t, server)
	fixture := authFixture(t, data)
	provider := httptest.NewServer(fixture.provider.Handler())
	defer provider.Close()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start, err := browser.Get(public.URL + "/auth/nox/start?return_to=%2Fvault-dashboard%3Fview%3Dexisting")
	if err != nil {
		t.Fatal(err)
	}
	start.Body.Close()
	if start.StatusCode != 303 {
		t.Fatal("login did not start", start.StatusCode)
	}
	authorization, _ := url.Parse(start.Header.Get("Location"))
	authorization.Scheme = "http"
	authorization.Host = strings.TrimPrefix(provider.URL, "http://")
	approved, err := browser.Get(authorization.String())
	if err != nil {
		t.Fatal(err)
	}
	approved.Body.Close()
	callback := approved.Header.Get("Location")
	result, err := browser.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	result.Body.Close()
	if result.StatusCode != 303 || result.Header.Get("Location") != "/vault-dashboard?view=existing" {
		t.Fatal("OIDC callback failed", result.StatusCode, result.Header)
	}
	response, err := browser.Get(public.URL + "/api/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var view dashboardPageData
	if err = json.NewDecoder(response.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if !view.Authenticated || view.User.ID != user.ID || view.APIKey != key || len(view.Vaults) != 1 || view.Vaults[0].ID != vault.ID {
		t.Fatal("NoX login did not preserve data/keys", view.Authenticated)
	}
	replay, err := browser.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	replay.Body.Close()
	if replay.StatusCode != 400 {
		t.Fatal("callback replay admitted")
	}
}
func TestNoXAdmissionRemovalAndOutageDoNotExtendKeyAccess(t *testing.T) {
	server, data, user, _ := newTestServer(t)
	key, err := data.CurrentAPIKey(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, failure := server.apiUser(context.Background(), key); failure != nil {
		t.Fatal(failure)
	}
	fixture := authFixture(t, data)
	fixture.provider.Deny()
	server.noxLogin.mu.Lock()
	for _, cached := range server.noxLogin.accounts {
		cached.until = time.Now().Add(-time.Second)
	}
	server.noxLogin.mu.Unlock()
	if _, failure := server.apiUser(context.Background(), key); failure == nil || failure.status != 401 {
		t.Fatal("removed account kept API key admission", failure)
	}
	stored, err := data.CurrentAPIKey(context.Background(), user.ID)
	if err != nil || stored != key {
		t.Fatal("account admission rewrote independent API key", err)
	}
}
func TestNoXLoginRejectsLegacyCookieAndExternalReturn(t *testing.T) {
	server, data, user, _ := newTestServer(t)
	token, err := data.CreateWebSession(context.Background(), user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://backend.test/api/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	response := httptest.NewRecorder()
	server.Routes().ServeHTTP(response, request)
	var view dashboardPageData
	_ = json.Unmarshal(response.Body.Bytes(), &view)
	if view.Authenticated {
		t.Fatal("legacy Google session admitted")
	}
	request = httptest.NewRequest("GET", "http://backend.test/auth/nox/start?return_to=https://attacker.example/", nil)
	response = httptest.NewRecorder()
	server.Routes().ServeHTTP(response, request)
	if response.Code != 400 {
		t.Fatal("external return accepted", response.Code)
	}
}

func TestNoXLogoutScopesAndIndependentKeyAdmission(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "suite"}[all], func(t *testing.T) {
			s, data, user, _ := newTestServer(t)
			token, err := fixtureWebSession(t, context.Background(), data, user.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			path, scope := "/api/session/logout", "app"
			if all {
				path, scope = "/api/session/logout-all", "suite"
			}
			response := administrationRequest(t, s, "POST", path, "", map[string]string{"Cookie": sessionCookieName + "=" + token, "X-Nox-Request": "1"})
			administrationStatus(t, response, 200)
			if authFixture(t, data).provider.LogoutScope() != scope {
				t.Fatal("wrong logout scope")
			}
			if _, _, found, err := data.NoXSession(context.Background(), token); err != nil || found {
				t.Fatal("local session survived", err)
			}
			key, err := data.CurrentAPIKey(context.Background(), user.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.apiUser(context.Background(), key); err != nil {
				t.Fatal("browser logout invalidated independent key", err)
			}
		})
	}
}
func TestNoXLocalLogoutWorksDuringAuthOutageAndSuiteLogoutReportsFailure(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "suite"}[all], func(t *testing.T) {
			s, data, user, _ := newTestServer(t)
			token, err := fixtureWebSession(t, context.Background(), data, user.ID, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			// Populate browser validation before simulating an outage.
			headers := map[string]string{"Cookie": sessionCookieName + "=" + token, "X-Nox-Request": "1"}
			administrationStatus(t, administrationRequest(t, s, "GET", "/api/account", "", headers), 200)
			authFixture(t, data).provider.Unavailable()
			path, status := "/api/session/logout", 200
			if all {
				path, status = "/api/session/logout-all", 503
			}
			administrationStatus(t, administrationRequest(t, s, "POST", path, "", headers), status)
			_, _, found, err := data.NoXSession(context.Background(), token)
			if err != nil || found != all {
				t.Fatal("wrong persistence after logout/outage", found, err)
			}
		})
	}
}
func TestNoXKeyAdmissionFailsClosedOnAuthOutage(t *testing.T) {
	s, data, user, _ := newTestServer(t)
	key, err := data.CurrentAPIKey(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.apiUser(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	authFixture(t, data).provider.Unavailable()
	s.noxLogin.mu.Lock()
	for _, cached := range s.noxLogin.accounts {
		cached.until = time.Now().Add(-time.Second)
	}
	s.noxLogin.mu.Unlock()
	if _, failure := s.apiUser(context.Background(), key); failure == nil || failure.status != 503 {
		t.Fatal("outage extended stale admission", failure)
	}
}
func TestNoXCallbackRequiresBrowserBinding(t *testing.T) {
	s, _, _, _ := newTestServer(t)
	start := httptest.NewRecorder()
	s.Routes().ServeHTTP(start, httptest.NewRequest("GET", "http://backend.test/auth/nox/start", nil))
	if start.Code != 303 {
		t.Fatal(start.Code)
	}
	location, _ := url.Parse(start.Header().Get("Location"))
	for _, cookie := range []*http.Cookie{nil, {Name: start.Result().Cookies()[0].Name, Value: "wrong-browser"}} {
		request := httptest.NewRequest("GET", "http://backend.test/auth/nox/callback?state="+location.Query().Get("state")+"&code=any", nil)
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		s.Routes().ServeHTTP(response, request)
		if response.Code != 400 {
			t.Fatal("unbound callback admitted", response.Code)
		}
	}
}
