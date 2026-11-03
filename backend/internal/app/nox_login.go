package app

import (
	"context"
	"errors"
	noxauth "github.com/mapherez/nox-auth/client"
	"github.com/mapherez/nox-backend/backend/internal/noxlogin"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	"net/http"
	"strings"
	"sync"
	"time"
)

type noxCachedSession struct {
	mu      sync.Mutex
	session *noxauth.Session
}
type noxCachedAccount struct {
	mu     sync.Mutex
	until  time.Time
	denied bool
}
type noxLoginState struct {
	mu       sync.Mutex
	client   *noxauth.Client
	config   storage.NoXLoginConfig
	sessions map[string]*noxCachedSession
	accounts map[string]*noxCachedAccount
}

func (s *Server) noXClient(ctx context.Context) (*noxauth.Client, storage.NoXLoginConfig, error) {
	if s.store == nil {
		return nil, storage.NoXLoginConfig{}, errors.New("NoX Login is not configured")
	}
	s.noxLogin.mu.Lock()
	defer s.noxLogin.mu.Unlock()
	if s.noxLogin.client != nil {
		return s.noxLogin.client, s.noxLogin.config, nil
	}
	cfg, found, err := s.store.NoXLoginConfiguration(ctx)
	if err != nil {
		return nil, cfg, err
	}
	if !found {
		return nil, cfg, errors.New("NoX Login is not configured")
	}
	if s.cfg.AuthPublicURL != "" && strings.TrimRight(s.cfg.AuthPublicURL, "/") != cfg.Issuer {
		return nil, cfg, errors.New("Auth issuer differs from enrolled identity")
	}
	if s.cfg.PublicURL != "" && strings.TrimRight(s.cfg.PublicURL, "/") != cfg.Origin {
		return nil, cfg, errors.New("Backend origin differs from enrolled callback")
	}
	client, err := noxlogin.Client(cfg)
	if err != nil {
		return nil, cfg, err
	}
	s.noxLogin.client = client
	s.noxLogin.config = cfg
	s.noxLogin.sessions = map[string]*noxCachedSession{}
	s.noxLogin.accounts = map[string]*noxCachedAccount{}
	return client, cfg, nil
}
func (s *Server) noXWebUser(r *http.Request) (storage.User, bool, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return storage.User{}, false, nil
	}
	user, credentials, found, err := s.store.NoXSession(r.Context(), cookie.Value)
	if err != nil || !found {
		return user, false, err
	}
	client, _, err := s.noXClient(r.Context())
	if err != nil {
		return user, false, err
	}
	// Cache partitions are credential hashes, never raw session secrets.
	key := noXHash(cookie.Value)
	s.noxLogin.mu.Lock()
	cached := s.noxLogin.sessions[key]
	if cached == nil {
		if len(s.noxLogin.sessions) >= 1024 {
			clear(s.noxLogin.sessions)
		}
		cached = &noxCachedSession{session: noxauth.NewSession(credentials)}
		s.noxLogin.sessions[key] = cached
	}
	s.noxLogin.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cached.mu.Lock()
	_, err = cached.session.Authenticate(ctx, client)
	fresh := cached.session.Snapshot()
	cached.mu.Unlock()
	if errors.Is(err, noxauth.ErrDenied) {
		_ = s.store.DeleteNoXSession(ctx, cookie.Value)
		return storage.User{}, false, nil
	}
	if err != nil {
		return storage.User{}, false, err
	}
	if fresh.ExpiresAt != credentials.ExpiresAt || fresh.Identity != credentials.Identity {
		if err = s.store.RefreshNoXSession(ctx, cookie.Value, fresh); err != nil {
			return storage.User{}, false, err
		}
	}
	return user, true, nil
}
func (s *Server) accountAdmission(ctx context.Context, user storage.User) (time.Time, error) {
	_, cfg, err := s.noXClient(ctx)
	if err != nil {
		return time.Time{}, err
	}
	key := user.ID + "\x00" + user.Email + "\x00" + user.GoogleSub
	s.noxLogin.mu.Lock()
	cached := s.noxLogin.accounts[key]
	if cached == nil {
		if len(s.noxLogin.accounts) >= 1024 {
			clear(s.noxLogin.accounts)
		}
		cached = &noxCachedAccount{}
		s.noxLogin.accounts[key] = cached
	}
	s.noxLogin.mu.Unlock()
	cached.mu.Lock()
	defer cached.mu.Unlock()
	if time.Now().Before(cached.until.Add(-30 * time.Second)) {
		if cached.denied {
			return time.Time{}, noxauth.ErrDenied
		}
		return cached.until, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	until, err := noxlogin.Admission(bounded, cfg, user.Email, user.GoogleSub)
	if err != nil {
		cached.until = time.Time{}
		if errors.Is(err, noxauth.ErrDenied) && !cached.denied {
			if eventErr := s.store.InvalidateNoXAdmission(ctx, user.ID); eventErr != nil {
				return time.Time{}, eventErr
			}
			cached.denied = true
		}
		return time.Time{}, err
	}
	cached.until = until
	cached.denied = false
	return until, nil
}
func (s *Server) noXStart(w http.ResponseWriter, r *http.Request) {
	if !managementMethod(w, r, "GET") {
		return
	}
	if !s.loginStarts.Allow() {
		w.Header().Set("Retry-After", "1")
		writeJSONError(w, 429, "RATE_LIMITED", "Try login again shortly.")
		return
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" || !s.sameManagementOrigin(r) {
		writeJSONError(w, 403, "FORBIDDEN", "Start NoX Login from Backend.")
		return
	}
	client, cfg, err := s.noXClient(r.Context())
	if err != nil {
		writeJSONError(w, 503, "AUTH_NOT_CONFIGURED", "Apply Backend's NoX Auth setup configuration.")
		return
	}
	target := r.URL.Query().Get("return_to")
	if target == "" {
		target = r.URL.Query().Get("redirect")
	}
	if target == "" {
		target = "/vault-dashboard"
	}
	if len(target) > 2048 || strings.HasPrefix(target, "/auth/") {
		writeJSONError(w, 400, "BAD_REQUEST", "Invalid return path.")
		return
	}
	tx, location, err := client.Begin(cfg.Origin+noxlogin.CallbackPath, target)
	if err != nil {
		writeJSONError(w, 400, "BAD_REQUEST", "Invalid return path.")
		return
	}
	browser := noxauth.GenerateSecret()
	if err = s.store.SaveNoXTransaction(r.Context(), browser, tx); err != nil {
		writeJSONError(w, 503, "AUTH_UNAVAILABLE", "Unable to save login transaction.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.noXTransactionCookie(tx.State, cfg), Value: browser, Path: "/", MaxAge: 600, HttpOnly: true, Secure: strings.HasPrefix(cfg.Origin, "https://"), SameSite: http.SameSiteLaxMode})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, location, 303)
}
func (s *Server) noXTransactionCookie(state string, cfg storage.NoXLoginConfig) string {
	name := "nox_backend_login_" + state[:12]
	if strings.HasPrefix(cfg.Origin, "https://") {
		name = "__Host-" + name
	}
	return name
}
func (s *Server) noXCallback(w http.ResponseWriter, r *http.Request) {
	if !managementMethod(w, r, "GET") {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	state := r.URL.Query().Get("state")
	if len(state) != 43 {
		writeJSONError(w, 400, "INVALID_LOGIN", "Invalid login transaction.")
		return
	}
	client, cfg, err := s.noXClient(r.Context())
	if err != nil {
		writeJSONError(w, 503, "AUTH_UNAVAILABLE", "NoX Login is unavailable.")
		return
	}
	name := s.noXTransactionCookie(state, cfg)
	cookie, err := r.Cookie(name)
	if err != nil {
		writeJSONError(w, 400, "INVALID_LOGIN", "Login belongs to another browser.")
		return
	}
	tx, err := s.store.ConsumeNoXTransaction(r.Context(), state, cookie.Value)
	if err != nil {
		writeJSONError(w, 400, "INVALID_LOGIN", "Invalid, expired or used login transaction.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(cfg.Origin, "https://"), SameSite: http.SameSiteLaxMode})
	credentials, err := client.Complete(r.Context(), cfg.Origin+noxlogin.CallbackPath, tx, r.URL.Query())
	if err != nil {
		http.Redirect(w, r, "/vault-dashboard?login_error=denied", 303)
		return
	}
	user, err := s.store.CompleteNoXLogin(r.Context(), cfg.Issuer, credentials.Identity)
	if err != nil {
		writeJSONError(w, 403, "FORBIDDEN", "This identity cannot access the Backend account.")
		return
	}
	token, err := s.store.CreateNoXSession(r.Context(), user.ID, credentials)
	if err != nil {
		writeJSONError(w, 503, "AUTH_UNAVAILABLE", "Unable to save login session.")
		return
	}
	if previous, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.store.DeleteNoXSession(r.Context(), previous.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", MaxAge: int(time.Until(credentials.ExpiresAt).Seconds()), Expires: credentials.ExpiresAt, HttpOnly: true, Secure: strings.HasPrefix(cfg.Origin, "https://"), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, tx.ReturnPath, 303)
}
func (s *Server) noXLogout(w http.ResponseWriter, r *http.Request, all bool) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		_, credentials, found, e := s.store.NoXSession(r.Context(), cookie.Value)
		if e != nil {
			writeJSONError(w, 503, "AUTH_UNAVAILABLE", "Unable to read session.")
			return false
		}
		if found {
			client, _, e := s.noXClient(r.Context())
			if e == nil {
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				e = client.Logout(ctx, credentials, all)
				cancel()
			}
			if e != nil && all {
				writeJSONError(w, 503, "AUTH_UNAVAILABLE", "Suite logout failed; retry. Other sessions have not been confirmed revoked.")
				return false
			}
		} else if all {
			writeJSONError(w, 401, "AUTH_REQUIRED", "An active NoX Login session is required.")
			return false
		}
		if err = s.store.DeleteNoXSession(r.Context(), cookie.Value); err != nil {
			writeJSONError(w, 503, "AUTH_UNAVAILABLE", "Unable to remove local session.")
			return false
		}
	} else if all {
		writeJSONError(w, 401, "AUTH_REQUIRED", "An active NoX Login session is required.")
		return false
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(s.publicURL(r), "https://"), SameSite: http.SameSiteLaxMode})
	return true
}
func (s *Server) handleSuiteLogout(w http.ResponseWriter, r *http.Request, _ storage.User) {
	if !managementMethod(w, r, "POST") {
		return
	}
	if _, present := r.Header["Authorization"]; present {
		writeJSONError(w, 400, "BAD_REQUEST", "Use the browser session for Suite logout.")
		return
	}
	if s.noXLogout(w, r, true) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}
func (s *Server) loginRenewal(r *http.Request) (bool, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false, nil
	}
	_, credentials, found, err := s.store.NoXSession(r.Context(), cookie.Value)
	return found && !time.Now().Before(credentials.BrowserRenewAt), err
}
