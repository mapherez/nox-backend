package app

import (
	"errors"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	"net/http"
	"net/url"
	"strconv"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

func (s *Server) handleSuiteProducts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("productId")
	if id == "" {
		if !suiteMethod(w, r, "GET") {
			return
		}
	} else if !suiteMethod(w, r, "GET", "PUT", "DELETE") {
		return
	}
	caller, ok := s.suiteRequestCaller(w, r, true, r.Method == "GET")
	if !ok {
		return
	}
	if id != "" && !noxmcp.ValidSuiteProductID(id) {
		writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "Invalid product identifier."))
		return
	}
	if r.Method == "GET" {
		result, err := s.store.SuiteReadRegistry(r.Context(), caller, id)
		if err == nil && result.ETag != "" {
			w.Header().Set("ETag", `"`+result.ETag+`"`)
		}
		writeSuiteResult(w, result, err)
		return
	}
	body, ok := readSuiteBody(w, r, r.Method == "PUT")
	if !ok {
		return
	}
	expected, ok := suitePreconditionHeader(w, r, r.Method == "PUT")
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	if r.Method == "DELETE" {
		result, err := s.store.SuiteRemoveProduct(r.Context(), caller, id, expected, key, digest)
		writeSuiteResult(w, result, err)
		return
	}
	req, err := noxmcp.LoadSuiteRegistrationRequest(body)
	if err != nil {
		writeSuiteError(w, suiteDocumentError(err, "Invalid registration request."))
		return
	}
	prepared, err := s.store.SuitePrepareRegistration(r.Context(), caller, id, req, expected, key, digest)
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	if prepared.Replay != nil {
		writeSuiteResult(w, *prepared.Replay, nil)
		return
	}
	select {
	case s.suiteVerify <- struct{}{}:
		defer func() { <-s.suiteVerify }()
	default:
		writeSuiteError(w, suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "Suite verification concurrency is full. Retry the same operation later."))
		return
	}
	declaration, err := s.fetchSuiteDeclaration(r.Context(), prepared.Origin, prepared.TLSFingerprint)
	if err != nil {
		var failure *storage.SuiteError
		if errors.As(err, &failure) && ((failure.Status == 422 && failure.Code == "UNSUPPORTED_INTEGRATION") || (failure.Status == 400 && failure.Code == "INVALID_REGISTRATION")) {
			if blocked := s.store.SuiteBlockIncompatibleRegistration(r.Context(), caller, id, req, expected, prepared); blocked != nil {
				writeSuiteError(w, blocked)
				return
			}
		}
		writeSuiteError(w, err)
		return
	}
	doc, err := noxmcp.LoadSuiteProductDeclaration(declaration)
	if err != nil {
		writeSuiteError(w, suiteDocumentError(err, "Invalid verified declaration."))
		return
	}
	if doc.ProductID != id {
		if err = s.store.SuiteBlockIncompatibleRegistration(r.Context(), caller, id, req, expected, prepared); err != nil {
			writeSuiteError(w, err)
			return
		}
		writeSuiteError(w, suiteHTTPError(409, "REGISTRATION_CONFLICT", "The verified product identity differs from the registered product."))
		return
	}
	result, err := s.store.SuiteCommitRegistration(r.Context(), caller, id, req, expected, key, digest, prepared, declaration)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteEvents(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "GET") {
		return
	}
	caller, ok := s.suiteCaller(w, r, true)
	if !ok {
		return
	}
	if caller.ServiceToken == "" {
		writeSuiteError(w, suiteHTTPError(403, "FORBIDDEN", "The event feed requires a service credential."))
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "Malformed event query."))
		return
	}
	for key, values := range query {
		if (key != "after" && key != "waitMs" && key != "limit") || len(values) != 1 {
			writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "Invalid event query parameters."))
			return
		}
	}
	after := query.Get("after")
	if after == "" {
		writeSuiteError(w, suiteHTTPError(410, "EVENT_CURSOR_EXPIRED", "Read a complete authorized registry snapshot to obtain an event cursor."))
		return
	}
	wait, limit := 25000, 256
	if values, exists := query["waitMs"]; exists {
		wait, err = strconv.Atoi(values[0])
		if err != nil || wait < 0 || wait > 25000 {
			writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "waitMs must be an integer from 0 to 25000."))
			return
		}
	}
	if values, exists := query["limit"]; exists {
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > 256 {
			writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "limit must be an integer from 1 to 256."))
			return
		}
	}
	identity, err := s.store.SuiteAuthenticateService(r.Context(), caller.ServiceToken)
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	s.suitePollMu.Lock()
	if s.suitePolls == nil {
		s.suitePolls = map[string]bool{}
	}
	if s.suitePolls[identity.ServiceID] || len(s.suitePolls) >= 64 {
		s.suitePollMu.Unlock()
		writeSuiteError(w, suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "Use one shared event subscription per service; retry with bounded backoff."))
		return
	}
	s.suitePolls[identity.ServiceID] = true
	s.suitePollMu.Unlock()
	defer func() { s.suitePollMu.Lock(); delete(s.suitePolls, identity.ServiceID); s.suitePollMu.Unlock() }()
	result, err := s.store.SuiteReadEvents(r.Context(), caller, after, time.Duration(wait)*time.Millisecond, limit)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteObservation(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "POST") {
		return
	}
	caller, ok := s.suiteCaller(w, r, true)
	if !ok {
		return
	}
	body, ok := readSuiteBody(w, r, true)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	req, err := noxmcp.LoadSuiteObservation(body)
	if err != nil {
		writeSuiteError(w, suiteDocumentError(err, "Invalid observation document."))
		return
	}
	if req.ProductID != r.PathValue("productId") {
		writeSuiteError(w, suiteHTTPError(409, "REGISTRATION_CONFLICT", "Product route and observation identity differ."))
		return
	}
	result, err := s.store.SuiteSubmitObservation(r.Context(), caller, body, key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteObservationEpoch(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "POST") {
		return
	}
	caller, ok := s.suiteCaller(w, r, true)
	if !ok {
		return
	}
	body, ok := readSuiteBody(w, r, true)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	req, err := noxmcp.LoadSuiteObservationEpochRequest(body)
	if err != nil {
		writeSuiteError(w, suiteDocumentError(err, "Invalid observation epoch request."))
		return
	}
	if req.ProductID != r.PathValue("productId") {
		writeSuiteError(w, suiteHTTPError(409, "REGISTRATION_CONFLICT", "Product route and epoch identity differ."))
		return
	}
	result, err := s.store.SuiteCreateObservationEpoch(r.Context(), caller, req, key, digest)
	writeSuiteResult(w, result, err)
}
