package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// Existing protocol tests now exercise actual Unix sockets. Dedicated boundary
// tests exercise TCP/TLS and native administration without the local marker.
func suiteTestUnixServer(t *testing.T, dir, id string, handler http.Handler) *httptest.Server {
	t.Helper()
	path := filepath.Join(dir, id+".sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	_ = server.Listener.Close()
	server.Listener = listener
	server.Start()
	server.URL = "http://" + id
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	server.Client().Transport = transport
	t.Cleanup(func() { server.Close(); transport.CloseIdleConnections() })
	return server
}
func suiteTestBackend(t *testing.T, s *Server) *httptest.Server {
	local, native := s.suiteLocalHandler(), s.Routes()
	return suiteTestUnixServer(t, s.cfg.SuiteSocketDir, "nox-backend", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/nox/") || r.URL.Path == "/mcp/suite" || r.URL.Path == "/nox/integration" {
			local.ServeHTTP(w, r)
			return
		}
		native.ServeHTTP(w, r)
	}))
}
