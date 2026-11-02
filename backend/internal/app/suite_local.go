package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

const suiteBackendOrigin = "http://nox-backend"

type suiteLocalKey struct{}

// Typed context cannot be set through a TCP source address, TLS or headers.
func suiteLocalRequest(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), suiteLocalKey{}, true))
}
func (s *Server) suiteLocalRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/nox/integration", s.handleIntegrationDeclaration)
	s.registerSuiteTrust(mux)
	if s.cfg.SuiteMCPEnabled {
		mux.Handle("/mcp/suite", s.suiteMCPHandler())
	}
	return mux
}
func (s *Server) suiteLocalHandler() http.Handler {
	routes := s.suiteLocalRoutes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "nox-backend" {
			writeJSONError(w, 400, "BAD_REQUEST", "Unexpected local Suite destination.")
			return
		}
		routes.ServeHTTP(w, suiteLocalRequest(r))
	})
}

// Preserve the readable convention for normal IDs. Numeric-only and 64-byte
// IDs need DNS-safe labels to remain valid origins in all three shared SDKs.
func suiteProductOrigin(id string) string {
	host := id
	numeric := id != ""
	for _, c := range id {
		if c < '0' || c > '9' {
			numeric = false
			break
		}
	}
	if len(id) > 63 || numeric {
		if len(id) > 32 {
			host = id[:32] + "." + id[32:]
		}
		host += ".nox-suite"
	}
	return "http://" + host
}
func (s *Server) suiteSocketPath(origin string) (string, error) {
	normalized, err := noxmcp.NormalizeSuiteOrigin(origin)
	if err != nil {
		return "", suiteHTTPError(400, "INVALID_REGISTRATION", "Invalid local Suite origin.")
	}
	u, _ := url.Parse(normalized)
	id := u.Host
	if strings.HasSuffix(id, ".nox-suite") {
		id = strings.ReplaceAll(strings.TrimSuffix(id, ".nox-suite"), ".", "")
	}
	if normalized != origin || u.Scheme != "http" || u.Port() != "" || !noxmcp.ValidSuiteProductID(id) || suiteProductOrigin(id) != origin {
		return "", suiteHTTPError(403, "FORBIDDEN", "Suite products must use their same-host local channel.")
	}
	if s.cfg.SuiteSocketDir == "" {
		return "", suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "The local Suite channel is disabled.")
	}
	return filepath.Join(s.cfg.SuiteSocketDir, id+".sock"), nil
}
func validateSuiteSocketDirectory(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("Suite socket directory must be absolute")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("local Suite directory unavailable: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0007 != 0 {
		return fmt.Errorf("Suite socket directory must be a real directory without access for other users")
	}
	return nil
}
func (s *Server) suiteLocalClient(origin string) (*http.Client, func(), error) {
	path, err := s.suiteSocketPath(origin)
	if err != nil {
		return nil, nil, err
	}
	if err = validateSuiteSocketDirectory(s.cfg.SuiteSocketDir); err != nil {
		return nil, nil, suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "The local Suite volume is unavailable. Update the deployment to mount its local volume.")
	}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 3 * time.Second, MaxConnsPerHost: 1}
	tr.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		if address != strings.TrimPrefix(origin, "http://")+":80" {
			return nil, errors.New("unexpected local Suite destination")
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("local Suite participant unavailable")
		}
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", path)
	}
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, tr.CloseIdleConnections, nil
}
