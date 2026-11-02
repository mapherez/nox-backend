package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

func suiteHTTPError(status int, code, message string) error {
	return &storage.SuiteError{Status: status, Code: code, Message: message, Retryable: status == 503}
}
func (s *Server) suiteSecureTransport(r *http.Request) bool {
	local, _ := r.Context().Value(suiteLocalKey{}).(bool)
	return local
}
func (s *Server) suiteOrigin() (string, error) {
	if s.cfg.SuiteSocketDir == "" {
		return "", suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "The local Suite channel is disabled.")
	}
	return suiteBackendOrigin, nil
}
func (s *Server) fetchSuiteDeclaration(ctx context.Context, origin, pin string) (json.RawMessage, error) {
	client, closeClient, err := s.suiteLocalClient(origin)
	if err != nil {
		return nil, err
	}
	defer closeClient()
	if pin != "" {
		return nil, suiteHTTPError(409, "REGISTRATION_CONFLICT", "This association uses network transport. Reassociate through the local Suite channel.")
	}
	request, err := http.NewRequestWithContext(ctx, "GET", origin+"/nox/integration", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		var rejected *storage.SuiteError
		if errors.As(err, &rejected) {
			return nil, rejected
		}
		return nil, suiteHTTPError(503, "PRODUCT_UNREACHABLE", "The product local Suite channel is unavailable. Update its Suite adapter and same-host deployment.")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, suiteHTTPError(403, "FORBIDDEN", "Suite declaration redirects are not followed.")
	}
	if response.StatusCode != 200 {
		return nil, suiteHTTPError(503, "PRODUCT_UNREACHABLE", "The product declaration endpoint did not return success.")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, noxmcp.SuiteMaxPayloadBytes+1))
	if err != nil {
		return nil, suiteHTTPError(503, "PRODUCT_UNREACHABLE", "The product declaration response was interrupted.")
	}
	if len(raw) > noxmcp.SuiteMaxPayloadBytes {
		return nil, suiteHTTPError(400, "INVALID_REGISTRATION", "The product declaration exceeds the contract limit.")
	}
	if _, err := noxmcp.LoadSuiteProductDeclaration(raw); err != nil {
		return nil, suiteDocumentError(err, "The product declaration is invalid.")
	}
	return raw, nil
}
func validateSuiteConfig(cfg Config) error {
	if cfg.SuiteReportInterval < 0 || cfg.SuiteObservationFreshness < 0 || cfg.SuiteReportInterval%time.Millisecond != 0 || cfg.SuiteObservationFreshness%time.Millisecond != 0 {
		return fmt.Errorf("Suite observation intervals must be positive whole milliseconds")
	}
	if (cfg.TLSCertFile == "") != (cfg.TLSKeyFile == "") {
		return fmt.Errorf("TLS certificate and key files must be configured together")
	}
	return nil
}

// An intent can precede deployment; its socket must exist at completion.
func (s *Server) suiteIntentTarget(origin string) error {
	_, err := s.suiteSocketPath(origin)
	return err
}
