package app

import (
	"context"
	"database/sql"
	noxauth "github.com/mapherez/nox-auth/client"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	"github.com/mapherez/nox-backend/backend/internal/testauth"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const webSessionDuration = 365 * 24 * time.Hour

type backendAuthFixture struct {
	provider *testauth.Provider
	cfg      storage.NoXLoginConfig
}

var backendAuthFixtures sync.Map

func authFixture(t *testing.T, data *storage.Store) *backendAuthFixture {
	t.Helper()
	if value, ok := backendAuthFixtures.Load(data); ok {
		return value.(*backendAuthFixture)
	}
	directory, err := os.MkdirTemp("", "ba")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory); backendAuthFixtures.Delete(data) })
	socket := filepath.Join(directory, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := testauth.New()
	if err != nil {
		t.Fatal(err)
	}
	config := storage.NoXLoginConfig{Issuer: testauth.Issuer, Origin: "http://backend.test", ClientID: "backend-fixture", Secret: noxauth.GenerateSecret(), SocketPath: socket}
	provider.Register(config.ClientID, config.Secret)
	if err = data.SaveNoXLoginConfiguration(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: provider.Handler()}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	f := &backendAuthFixture{provider, config}
	backendAuthFixtures.Store(data, f)
	return f
}
func newAuthTestServer(t *testing.T, cfg Config, data *storage.Store) *Server {
	t.Helper()
	if data != nil {
		f := authFixture(t, data)
		if cfg.PublicURL != "" {
			config := f.cfg
			config.Origin = cfg.PublicURL
			if err := data.SaveNoXLoginConfiguration(context.Background(), config); err != nil {
				t.Fatal(err)
			}
		}
	}
	return NewServer(cfg, data)
}
func fixtureWebSession(t *testing.T, ctx context.Context, data *storage.Store, userID string, duration time.Duration) (string, error) {
	t.Helper()
	f := authFixture(t, data)
	user, err := data.UserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	credentials := f.provider.Issue(f.cfg.ClientID, user.Email)
	if duration > 0 {
		credentials.ExpiresAt = time.Now().Add(duration)
	}
	token, err := data.CreateNoXSession(ctx, userID, credentials)
	if err == nil && duration < 0 {
		db, e := sql.Open("sqlite", filepath.Join(filepath.Dir(data.DBPath()), "nox-login.db"))
		if e != nil {
			return "", e
		}
		defer db.Close()
		_, err = db.Exec(`UPDATE sessions SET expires=? WHERE token_hash=?`, time.Now().Add(duration).Unix(), noXHash(token))
	}
	return token, err
}

func setFixtureOrigin(t *testing.T, s *Server) {
	t.Helper()
	if s.store == nil {
		return
	}
	f := authFixture(t, s.store)
	cfg := f.cfg
	cfg.Origin = s.cfg.PublicURL
	if err := s.store.SaveNoXLoginConfiguration(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	s.noxLogin.mu.Lock()
	s.noxLogin.client = nil
	s.noxLogin.mu.Unlock()
}
func fixtureSessionUser(s *storage.Store, ctx context.Context, token string) (storage.User, bool, error) {
	u, _, found, err := s.NoXSession(ctx, token)
	return u, found, err
}

// Establish the same verified binding as the callback, without a second account
// allowlist or a test-only authentication path in the production server.
func fixtureManager(t *testing.T, data *storage.Store, user storage.User) {
	t.Helper()
	sub := user.GoogleSub
	if sub == "" {
		sub = "fixture-google-" + user.ID
	}
	_, err := data.CompleteNoXLogin(context.Background(), testauth.Issuer, noxauth.Identity{Subject: "fixture-manager-" + user.ID, Email: user.Email, EmailVerified: true, Admin: true, GoogleSubject: sub, GoogleIssuer: "https://accounts.google.com", Name: user.DisplayName})
	if err != nil {
		t.Fatal(err)
	}
}
