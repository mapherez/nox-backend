package app

import (
	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
	"net/http"
)

func (s *Server) suiteIssuingCaller(w http.ResponseWriter, r *http.Request, grant string) (storage.SuiteCaller, bool) {
	caller, ok := s.suiteCaller(w, r, true)
	if !ok {
		return caller, false
	}
	if caller.ServiceToken == "" {
		writeSuiteError(w, suiteHTTPError(403, "FORBIDDEN", "Authorization exchange requires the Gateway's own service credential."))
		return caller, false
	}
	if err := s.store.SuiteCheckIssuer(r.Context(), caller, grant); err != nil {
		writeSuiteError(w, err)
		return caller, false
	}
	if r.URL.RawQuery != "" {
		writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "Authorization exchange accepts its bounded JSON body only; credentials must not appear in URLs."))
		return caller, false
	}
	return caller, true
}
func (s *Server) handleSuiteAuthorization(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "POST") {
		return
	}
	caller, ok := s.suiteIssuingCaller(w, r, "authorization.delegate")
	if !ok {
		return
	}
	body, ok := readSuiteBody(w, r, true)
	if !ok {
		return
	}
	request, err := noxmcp.LoadSuiteAuthorizationRequest(body)
	if err != nil {
		writeSuiteError(w, suiteDocumentError(err, "Invalid user authorization request."))
		return
	}
	result, err := s.store.SuiteIssueAuthorization(r.Context(), caller, request)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteProbeAuthorization(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "POST") {
		return
	}
	caller, ok := s.suiteIssuingCaller(w, r, "mcp.probe")
	if !ok {
		return
	}
	body, ok := readSuiteBody(w, r, true)
	if !ok {
		return
	}
	request, err := noxmcp.LoadSuiteProbeRequest(body)
	if err != nil {
		writeSuiteError(w, suiteDocumentError(err, "Invalid service authorization request."))
		return
	}
	result, err := s.store.SuiteIssueProbe(r.Context(), caller, request)
	writeSuiteResult(w, result, err)
}
