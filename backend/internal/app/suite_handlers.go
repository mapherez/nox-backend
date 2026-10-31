package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

func writeSuiteError(w http.ResponseWriter, err error) {
	var failure *storage.SuiteError
	if !errors.As(err, &failure) {
		failure = &storage.SuiteError{Status: 503, Code: "INTEGRATION_UNAVAILABLE", Message: "A required Backend trust dependency is unavailable.", Retryable: true}
	}
	writeJSON(w, failure.Status, map[string]any{"error": map[string]any{"code": failure.Code, "message": failure.Message, "retryable": failure.Retryable}})
}
func writeSuiteResult(w http.ResponseWriter, result storage.SuiteResult, err error) {
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}
func suiteMethod(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for _, method := range allowed {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeSuiteError(w, suiteHTTPError(405, "METHOD_NOT_ALLOWED", "Method not allowed."))
	return false
}
func (s *Server) registerSuiteTrust(mux *http.ServeMux) {
	mux.HandleFunc("/api/nox/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeSuiteError(w, suiteHTTPError(404, "ROUTE_NOT_FOUND", "Suite management route not found."))
	})
	routes := []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/api/nox/authorizations", s.handleSuiteAuthorization},
		{"/api/nox/service-authorizations", s.handleSuiteProbeAuthorization},
		{"/api/nox/products", s.handleSuiteProducts},
		{"/api/nox/products/{productId}", s.handleSuiteProducts},
		{"/api/nox/products/{productId}/observations", s.handleSuiteObservation},
		{"/api/nox/products/{productId}/observation-epochs", s.handleSuiteObservationEpoch},
		{"/api/nox/events", s.handleSuiteEvents},
		{"/api/nox/keys", s.handleSuiteKeys},
		{"/api/nox/keys/{keyId}", s.handleSuiteRevokeKey},
		{"/api/nox/keys/{keyId}/activate", s.handleSuiteActivateKey},
		{"/api/nox/pairings", s.handleSuiteCreatePairing},
		{"/api/nox/pairings/{pairingId}", s.handleSuitePairing},
		{"/api/nox/pairings/{pairingId}/complete", s.handleSuiteCompletePairing},
		{"/api/nox/pairings/{pairingId}/ack", s.handleSuiteAckPairing},
		{"/api/nox/services", s.handleSuiteServices},
		{"/api/nox/services/{serviceId}", s.handleSuiteServices},
		{"/api/nox/services/{serviceId}/credentials", s.handleSuiteRotateCredential},
		{"/api/nox/services/{serviceId}/credentials/{credentialId}", s.handleSuiteRevokeCredential},
		{"/api/nox/services/{serviceId}/credentials/{credentialId}/ack", s.handleSuiteAckCredential},
		{"/api/nox/services/{serviceId}/deployments", s.handleSuiteDeployments},
		{"/api/nox/services/{serviceId}/deployments/{deploymentId}", s.handleSuiteDeployment},
	}
	for _, route := range routes {
		mux.HandleFunc(route.path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if !s.suiteSecureTransport(r) {
				writeSuiteError(w, suiteHTTPError(403, "FORBIDDEN", "Suite trust requires HTTPS or an explicitly configured trusted TLS proxy boundary."))
				return
			}
			if _, err := s.suiteOrigin(); err != nil {
				writeSuiteError(w, err)
				return
			}
			budget := 20 * time.Second
			if r.URL.Path == "/api/nox/events" {
				budget = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(r.Context(), budget)
			defer cancel()
			r = r.WithContext(ctx)
			controller := http.NewResponseController(w)
			_ = controller.SetReadDeadline(time.Now().Add(budget))
			defer controller.SetReadDeadline(time.Time{})
			if err := s.store.MaintainSuiteTrust(ctx); err != nil {
				writeSuiteError(w, err)
				return
			}
			route.handler(w, r)
		})
	}
}
func (s *Server) suiteCaller(w http.ResponseWriter, r *http.Request, services bool) (storage.SuiteCaller, bool) {
	return s.suiteRequestCaller(w, r, services, false)
}
func (s *Server) suiteRequestCaller(w http.ResponseWriter, r *http.Request, services, members bool) (storage.SuiteCaller, bool) {
	if values, present := r.Header["Authorization"]; present {
		if len(values) != 1 {
			writeSuiteError(w, suiteHTTPError(401, "UNAUTHENTICATED", "Invalid authorization header."))
			return storage.SuiteCaller{}, false
		}
		fields := strings.Fields(values[0])
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			writeSuiteError(w, suiteHTTPError(401, "UNAUTHENTICATED", "A current Bearer credential is required."))
			return storage.SuiteCaller{}, false
		}
		if strings.HasPrefix(fields[1], "noxsvc_") {
			if _, err := s.store.SuiteAuthenticateService(r.Context(), fields[1]); err != nil {
				writeSuiteError(w, err)
				return storage.SuiteCaller{}, false
			}
			if !services {
				writeSuiteError(w, suiteHTTPError(403, "FORBIDDEN", "This operation requires a Backend administrator."))
				return storage.SuiteCaller{}, false
			}
			return storage.SuiteCaller{ServiceToken: fields[1]}, true
		}
		user, err := s.apiUser(r.Context(), fields[1])
		if err != nil {
			status := 401
			if err.status >= 500 {
				status = 503
			}
			writeSuiteError(w, suiteHTTPError(status, "UNAUTHENTICATED", "A valid Backend administrator credential is required."))
			return storage.SuiteCaller{}, false
		}
		if user.Role != storage.UserRoleAdmin && !members {
			writeSuiteError(w, suiteHTTPError(403, "FORBIDDEN", "Admin access is required."))
			return storage.SuiteCaller{}, false
		}
		return storage.SuiteCaller{UserID: user.ID}, true
	}
	user, valid, err := s.webUser(r)
	if err != nil {
		writeSuiteError(w, err)
		return storage.SuiteCaller{}, false
	}
	if !valid {
		writeSuiteError(w, suiteHTTPError(401, "UNAUTHENTICATED", "An active Backend session or service credential is required."))
		return storage.SuiteCaller{}, false
	}
	if user.Role != storage.UserRoleAdmin && !members {
		writeSuiteError(w, suiteHTTPError(403, "FORBIDDEN", "Admin access is required."))
		return storage.SuiteCaller{}, false
	}
	if r.Method != "GET" && (r.Header.Get("X-Nox-Request") != "1" || !s.sameManagementOrigin(r)) {
		writeSuiteError(w, suiteHTTPError(403, "FORBIDDEN", "A same-origin administration request is required."))
		return storage.SuiteCaller{}, false
	}
	return storage.SuiteCaller{UserID: user.ID}, true
}
func readSuiteBody(w http.ResponseWriter, r *http.Request, required bool) ([]byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, noxmcp.SuiteMaxPayloadBytes))
	defer r.Body.Close()
	if err != nil {
		writeSuiteError(w, suiteHTTPError(413, "INVALID_REGISTRATION", "Suite request body exceeds the contract limit or was interrupted."))
		return nil, false
	}
	if len(raw) == 0 && !required {
		return nil, true
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeSuiteError(w, suiteHTTPError(415, "INVALID_REGISTRATION", "Content-Type must be application/json."))
		return nil, false
	}
	value, err := noxmcp.ParseSuiteJSON(raw)
	if err != nil {
		writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "Invalid or ambiguous Suite JSON document."))
		return nil, false
	}
	object, ok := value.(map[string]any)
	if !ok {
		writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "A Suite JSON object is required."))
		return nil, false
	}
	if version, present := object["contractVersion"]; present {
		text, ok := version.(string)
		if !ok {
			writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "The integration contract version must be a string."))
			return nil, false
		}
		if text != "1.0" {
			writeSuiteError(w, suiteHTTPError(422, "UNSUPPORTED_INTEGRATION", "Unsupported integration contract version."))
			return nil, false
		}
	}
	return raw, true
}
func suitePreconditionHeader(w http.ResponseWriter, r *http.Request, create bool) (string, bool) {
	match, none := r.Header.Get("If-Match"), r.Header.Get("If-None-Match")
	if len(r.Header.Values("If-Match")) > 1 || len(r.Header.Values("If-None-Match")) > 1 {
		writeSuiteError(w, suiteHTTPError(428, "PRECONDITION_REQUIRED", "Use a single strong trust precondition."))
		return "", false
	}
	if create && none == "*" && match == "" {
		return "", true
	}
	value, valid := suiteStrongRevision(match)
	if none == "" && valid {
		return value, true
	}
	writeSuiteError(w, suiteHTTPError(428, "PRECONDITION_REQUIRED", "Use If-None-Match: * to create or If-Match with the quoted current revision."))
	return "", false
}
func suiteDigest(w http.ResponseWriter, r *http.Request, body []byte) (string, string, bool) {
	key := r.Header.Get("Idempotency-Key")
	if len(r.Header.Values("Idempotency-Key")) != 1 || len(key) < 1 || len(key) > 128 || strings.IndexFunc(key, func(c rune) bool { return c < 33 || c > 126 }) >= 0 {
		writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "A bounded printable Idempotency-Key is required."))
		return "", "", false
	}
	var value any
	var err error
	if len(body) > 0 {
		value, err = noxmcp.ParseSuiteJSON(body)
		if err != nil {
			writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "Invalid Suite document."))
			return "", "", false
		}
	}
	digest, err := noxmcp.Fingerprint(map[string]any{"method": r.Method, "path": r.URL.EscapedPath(), "ifMatch": r.Header.Get("If-Match"), "ifNoneMatch": r.Header.Get("If-None-Match"), "body": value})
	if err != nil {
		writeSuiteError(w, err)
		return "", "", false
	}
	return key, digest, true
}
func (s *Server) handleSuiteKeys(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "GET", "POST") {
		return
	}
	if r.Method == "GET" {
		keys, err := s.store.SuiteKeys(r.Context())
		if err != nil {
			writeSuiteError(w, err)
			return
		}
		w.Header().Set("ETag", `"`+keys.Revision+`"`)
		writeJSON(w, 200, keys)
		return
	}
	caller, ok := s.suiteCaller(w, r, false)
	if !ok {
		return
	}
	body, ok := readSuiteBody(w, r, false)
	if !ok {
		return
	}
	expected, ok := suitePreconditionHeader(w, r, false)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	result, err := s.store.SuitePrepareSigningKey(r.Context(), caller, expected, key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteActivateKey(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "POST") {
		return
	}
	caller, ok := s.suiteCaller(w, r, false)
	if !ok {
		return
	}
	body, ok := readSuiteBody(w, r, false)
	if !ok {
		return
	}
	expected, ok := suitePreconditionHeader(w, r, false)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	result, err := s.store.SuiteActivateSigningKey(r.Context(), caller, r.PathValue("keyId"), expected, key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteRevokeKey(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "DELETE") {
		return
	}
	caller, ok := s.suiteCaller(w, r, false)
	if !ok {
		return
	}
	expected, ok := suitePreconditionHeader(w, r, false)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, nil)
	if !ok {
		return
	}
	result, err := s.store.SuiteRevokeSigningKey(r.Context(), caller, r.PathValue("keyId"), expected, key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteCreatePairing(w http.ResponseWriter, r *http.Request) {
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
	request, err := noxmcp.LoadSuitePairingRequest(body)
	if err != nil {
		writeSuiteError(w, suiteDocumentError(err, "Invalid pairing request."))
		return
	}
	if err := s.suiteIntentTarget(request.BaseUrl); err != nil {
		writeSuiteError(w, err)
		return
	}
	expected, ok := suitePreconditionHeader(w, r, true)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	result, err := s.store.SuiteCreatePairing(r.Context(), caller, request, expected, key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuitePairing(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "GET", "DELETE") {
		return
	}
	caller, ok := s.suiteCaller(w, r, true)
	if !ok {
		return
	}
	id := r.PathValue("pairingId")
	if r.Method == "GET" {
		result, err := s.store.SuiteReadPairing(r.Context(), caller, id)
		if err == nil {
			var view struct {
				Revision string `json:"revision"`
			}
			if json.Unmarshal(result.Body, &view) == nil {
				w.Header().Set("ETag", `"`+view.Revision+`"`)
			}
		}
		writeSuiteResult(w, result, err)
		return
	}
	expected, ok := suitePreconditionHeader(w, r, false)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, nil)
	if !ok {
		return
	}
	result, err := s.store.SuiteCancelPairing(r.Context(), caller, id, expected, key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteCompletePairing(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "POST") {
		return
	}
	body, ok := readSuiteBody(w, r, true)
	if !ok {
		return
	}
	proof, err := noxmcp.LoadSuitePairingProof(body)
	if err != nil {
		writeSuiteError(w, suiteDocumentError(err, "Invalid pairing proof document."))
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	id := r.PathValue("pairingId")
	request, completed, err := s.store.SuiteCheckPairingProof(r.Context(), id, proof)
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	var declaration json.RawMessage
	if !completed {
		select {
		case s.suiteVerify <- struct{}{}:
			defer func() { <-s.suiteVerify }()
		default:
			writeSuiteError(w, suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "Suite verification concurrency is full. Retry the same operation later."))
			return
		}
		declaration, err = s.fetchSuiteDeclaration(r.Context(), request.BaseUrl, proof.Binding.TlsPublicKeyFingerprint)
		if err != nil {
			writeSuiteError(w, err)
			return
		}
	}
	origin, err := s.suiteOrigin()
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	result, err := s.store.SuiteCompletePairing(r.Context(), id, proof, declaration, origin, key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteAckPairing(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "POST") {
		return
	}
	caller, ok := s.suiteCaller(w, r, true)
	if !ok {
		return
	}
	body, ok := readSuiteBody(w, r, false)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	result, err := s.store.SuiteAcknowledgePairing(r.Context(), caller, r.PathValue("pairingId"), key, digest)
	writeSuiteResult(w, result, err)
}

func (s *Server) handleSuiteServices(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("serviceId")
	if id == "" {
		if !suiteMethod(w, r, "GET") {
			return
		}
	} else {
		if !suiteMethod(w, r, "GET", "DELETE") {
			return
		}
	}
	caller, ok := s.suiteCaller(w, r, true)
	if !ok {
		return
	}
	if r.Method == "GET" {
		result, err := s.store.SuiteReadServices(r.Context(), caller, id)
		writeSuiteResult(w, result, err)
		return
	}
	expected, ok := suitePreconditionHeader(w, r, false)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, nil)
	if !ok {
		return
	}
	result, err := s.store.SuiteRevokeService(r.Context(), caller, id, expected, key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteRotateCredential(w http.ResponseWriter, r *http.Request) {
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
	request, err := noxmcp.LoadSuiteCredentialRotation(body)
	if err != nil {
		writeSuiteError(w, suiteDocumentError(err, "Invalid credential rotation document."))
		return
	}
	expected, ok := suitePreconditionHeader(w, r, false)
	if !ok {
		return
	}
	if expected != request.CredentialRevision {
		writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "Credential revision must agree with If-Match."))
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	result, err := s.store.SuiteRotateCredential(r.Context(), caller, r.PathValue("serviceId"), expected, key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteRevokeCredential(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "DELETE") {
		return
	}
	caller, ok := s.suiteCaller(w, r, true)
	if !ok {
		return
	}
	expected, ok := suitePreconditionHeader(w, r, false)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, nil)
	if !ok {
		return
	}
	result, err := s.store.SuiteRevokeCredential(r.Context(), caller, r.PathValue("serviceId"), r.PathValue("credentialId"), expected, key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteAckCredential(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "POST") {
		return
	}
	caller, ok := s.suiteCaller(w, r, true)
	if !ok {
		return
	}
	body, ok := readSuiteBody(w, r, false)
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	result, err := s.store.SuiteAcknowledgeCredential(r.Context(), caller, r.PathValue("serviceId"), r.PathValue("credentialId"), key, digest)
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteDeployments(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "GET") {
		return
	}
	caller, ok := s.suiteCaller(w, r, true)
	if !ok {
		return
	}
	result, err := s.store.SuiteReadDeployments(r.Context(), caller, r.PathValue("serviceId"))
	writeSuiteResult(w, result, err)
}
func (s *Server) handleSuiteDeployment(w http.ResponseWriter, r *http.Request) {
	if !suiteMethod(w, r, "PUT", "DELETE") {
		return
	}
	caller, ok := s.suiteCaller(w, r, false)
	if !ok {
		return
	}
	var grant *storage.SuiteDeploymentGrant
	var body []byte
	if r.Method == "PUT" {
		body, ok = readSuiteBody(w, r, true)
		if !ok {
			return
		}
		grant = &storage.SuiteDeploymentGrant{}
		if err := json.Unmarshal(body, grant); err != nil {
			writeSuiteError(w, suiteHTTPError(400, "INVALID_REGISTRATION", "Invalid deployment authorization."))
			return
		}
		if err := s.suiteIntentTarget(grant.BaseURL); err != nil {
			writeSuiteError(w, err)
			return
		}
	}
	expected, ok := suitePreconditionHeader(w, r, r.Method == "PUT")
	if !ok {
		return
	}
	key, digest, ok := suiteDigest(w, r, body)
	if !ok {
		return
	}
	result, err := s.store.SuiteSetDeployment(r.Context(), caller, r.PathValue("serviceId"), r.PathValue("deploymentId"), expected, key, digest, grant)
	writeSuiteResult(w, result, err)
}

func suiteStrongRevision(raw string) (string, bool) {
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", false
	}
	value := raw[1 : len(raw)-1]
	for _, c := range value {
		if c < 33 || c > 126 || c == '"' || c == ',' || c == '\\' {
			return "", false
		}
	}
	return value, true
}

func suiteDocumentError(err error, message string) error {
	var failure *noxmcp.SuiteError
	if errors.As(err, &failure) && failure.Code == "UNSUPPORTED_INTEGRATION" {
		return suiteHTTPError(422, "UNSUPPORTED_INTEGRATION", "Unsupported integration contract or required feature.")
	}
	return suiteHTTPError(400, "INVALID_REGISTRATION", message)
}
