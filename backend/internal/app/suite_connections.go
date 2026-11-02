package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

type connectionSetupView struct {
	AttemptID      string `json:"attemptId"`
	State          string `json:"state"`
	ProductID      string `json:"productId"`
	DisplayName    string `json:"displayName"`
	ProductVersion string `json:"productVersion"`
	ErrorCode      string `json:"errorCode,omitempty"`
	Message        string `json:"message,omitempty"`
	CanResume      bool   `json:"canResume"`
	CanVerify      bool   `json:"canVerify"`
	CanCancel      bool   `json:"canCancel"`
}

func setupView(a storage.SuiteConnectionAttempt) *connectionSetupView {
	if a.ID == "" {
		return nil
	}
	closed := a.State == "completed" || a.State == "cancelled"
	return &connectionSetupView{AttemptID: a.ID, State: a.State, ProductID: a.ProductID, DisplayName: a.DisplayName, ProductVersion: a.ProductVersion, ErrorCode: a.ErrorCode, Message: a.Message, CanResume: !closed && (a.State == "verified" || a.PairingID != ""), CanVerify: closed || a.PairingID == "", CanCancel: !closed}
}
func foundationIdentity(service string) (string, string) {
	if service == "yard" {
		return "nox-yard", "installer"
	}
	if service == "gateway" {
		return "nox-gateway", "gateway"
	}
	return "", ""
}
func (s *Server) handleSuiteAdminOperation(w http.ResponseWriter, r *http.Request, _ storage.User) {
	// managementActor has already rejected service Bearer credentials and checked
	// active administrator/session/CSRF. This is not a proxy to another service.
	clone := r.Clone(r.Context())
	u := *r.URL
	u.Path = "/api/nox/" + r.PathValue("operation")
	u.RawPath = ""
	clone.URL = &u
	s.suiteLocalRoutes().ServeHTTP(w, suiteLocalRequest(clone))
}
func (s *Server) handleConnectionSetup(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "POST") {
		return
	}
	service := r.PathValue("service")
	id, _ := foundationIdentity(service)
	if id == "" {
		writeJSONError(w, 404, "NOT_FOUND", "Unknown foundation-service role.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	caller := storage.SuiteCaller{UserID: actor.ID}
	var input struct {
		Revision  string `json:"revision"`
		AttemptID string `json:"attemptId"`
	}
	if !decodeManagementJSON(w, r, &input) {
		return
	}
	action := r.PathValue("action")
	if action == "verify" {
		select {
		case s.suiteVerify <- struct{}{}:
			defer func() { <-s.suiteVerify }()
		default:
			writeSuiteError(w, suiteHTTPError(503, "INTEGRATION_UNAVAILABLE", "Verification capacity is occupied. Retry later."))
			return
		}
		items, err := s.store.FoundationConnections(ctx)
		if err != nil {
			writeManagementError(w, err)
			return
		}
		var connection storage.FoundationConnection
		for _, item := range items {
			if item.Service == service {
				connection = item
			}
		}
		if connection.Revision == "" || connection.Revision != input.Revision {
			writeManagementError(w, storage.ErrPreconditionFailed)
			return
		}
		origin := suiteProductOrigin(id)
		raw, err := s.fetchSuiteDeclaration(ctx, origin, "")
		if err != nil {
			writeSuiteError(w, err)
			return
		}
		doc, err := noxmcp.LoadSuiteProductDeclaration(raw)
		if err != nil {
			writeSuiteError(w, err)
			return
		}
		if doc.ProductID != id {
			writeSuiteError(w, suiteHTTPError(409, "REGISTRATION_CONFLICT", "The local declaration does not match the intended foundation service."))
			return
		}
		value, _ := noxmcp.ParseSuiteJSON(raw)
		fp, err := noxmcp.Fingerprint(value)
		if err != nil {
			writeSuiteError(w, err)
			return
		}
		a, err := s.store.SuitePrepareConnection(ctx, caller, storage.SuiteConnectionAttempt{Service: service, ActorID: actor.ID, ConfigurationRevision: connection.Revision, Origin: connection.Origin, PeerOrigin: origin, ProductID: doc.ProductID, DisplayName: doc.DisplayName, ProductVersion: doc.ProductVersion, DeclarationFingerprint: fp})
		if err != nil {
			writeSuiteError(w, err)
			return
		}
		writeJSON(w, 200, setupView(a))
		return
	}
	if action != "connect" && action != "cancel" {
		writeJSONError(w, 404, "NOT_FOUND", "Unknown connection operation.")
		return
	}
	a, err := s.store.SuiteClaimConnection(ctx, caller, service, input.AttemptID, action == "cancel")
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	if action == "cancel" {
		err = s.cancelConnection(ctx, caller, &a)
	} else {
		err = s.connectFoundation(ctx, caller, &a, true)
	}
	s.finishConnectionAttempt(ctx, caller, &a, err)
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	writeJSON(w, 200, setupView(a))
}
func (s *Server) finishConnectionAttempt(ctx context.Context, caller storage.SuiteCaller, a *storage.SuiteConnectionAttempt, err error) {
	if err != nil {
		a.State = "error"
		a.ErrorCode = "INTEGRATION_UNAVAILABLE"
		a.Message = "Connection could not be completed. Refresh its state and resume the same attempt."
		var failure *storage.SuiteError
		if errors.As(err, &failure) {
			a.ErrorCode = failure.Code
			a.Message = failure.Message
		}
	}
	// Persist diagnostics even after a client disconnect; storage still checks
	// current actor/configuration/attempt/lease before accepting this update.
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
	}
	_ = s.store.SuiteUpdateConnection(ctx, caller, *a, true)
}
func connectionDigest(action string, a storage.SuiteConnectionAttempt) (string, string) {
	key := a.ID + ":" + action
	digest, _ := noxmcp.Fingerprint(map[string]any{"action": action, "attempt": a.ID, "revision": a.ConfigurationRevision, "productId": a.ProductID, "origin": a.PeerOrigin})
	return key, digest
}
func (s *Server) connectFoundation(ctx context.Context, caller storage.SuiteCaller, a *storage.SuiteConnectionAttempt, deliver bool) error {
	ctx = storage.SuiteConnectionContext(ctx, *a)
	_, purpose := foundationIdentity(a.Service)
	if a.State == "completed" {
		return nil
	}
	if a.PairingID == "" {
		status, err := s.store.SuiteFoundationStatus(ctx, purpose, a.PeerOrigin)
		if err != nil {
			return err
		}
		if status == "registered" {
			a.State = "completed"
			a.ErrorCode = ""
			a.Message = "Service is registered. MCP availability is tracked separately."
			return nil
		}
		if !deliver {
			return nil
		}
		// Confirmation binds the declaration verified for this exact attempt.
		raw, err := s.fetchSuiteDeclaration(ctx, a.PeerOrigin, "")
		if err != nil {
			return err
		}
		value, _ := noxmcp.ParseSuiteJSON(raw)
		fp, err := noxmcp.Fingerprint(value)
		if err != nil {
			return err
		}
		if fp != a.DeclarationFingerprint {
			return suiteHTTPError(409, "REGISTRATION_CONFLICT", "The service changed after verification. Verify it again before connecting.")
		}
		key, digest := connectionDigest("pair", *a)
		result, err := s.store.SuiteCreatePairing(ctx, caller, noxmcp.SuitePairingRequest{ContractVersion: "1.0", ProductID: a.ProductID, BaseUrl: a.PeerOrigin, Purpose: purpose}, a.ExpectedTrustRevision, key, digest)
		if err != nil {
			return err
		}
		intent, err := noxmcp.LoadSuitePairingIntent(result.Body)
		if err != nil {
			return err
		}
		a.PairingID = intent.PairingID
		a.State = "connecting"
	}
	pairing, err := s.store.SuiteConnectionPairing(ctx, caller, *a)
	if err != nil {
		return err
	}
	if pairing.State != "acknowledged" && deliver {
		// Reuse the durable encrypted intent response and the same idempotency key.
		key, digest := connectionDigest("pair", *a)
		result, err := s.store.SuiteCreatePairing(ctx, caller, noxmcp.SuitePairingRequest{ContractVersion: "1.0", ProductID: a.ProductID, BaseUrl: a.PeerOrigin, Purpose: purpose}, a.ExpectedTrustRevision, key, digest)
		if err != nil {
			return err
		}
		intent, err := noxmcp.LoadSuitePairingIntent(result.Body)
		if err != nil {
			return err
		}
		deliveryErr := s.deliverConnectionIntent(ctx, *a, intent)
		pairing, err = s.store.SuiteConnectionPairing(ctx, caller, *a)
		if err != nil {
			return err
		}
		if deliveryErr != nil && pairing.State != "acknowledged" {
			return deliveryErr
		}
	}
	if pairing.State != "acknowledged" {
		a.State = "waiting"
		a.ErrorCode = ""
		a.Message = "Waiting for the service to save and acknowledge its credentials. Refresh or resume this connection."
		return nil
	}
	registered, err := s.store.SuiteConnectionRegistered(ctx, caller, *a)
	if err != nil {
		return err
	}
	if registered {
		a.State = "completed"
		a.ErrorCode = ""
		a.Message = "Service is registered. MCP availability is tracked separately."
		return nil
	}
	key, digest := connectionDigest("register", *a)
	req := noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: a.PeerOrigin}
	prepared, err := s.store.SuitePrepareRegistration(ctx, caller, a.ProductID, req, a.RegistrationRevision, key, digest)
	if err != nil {
		return err
	}
	if prepared.Replay == nil && prepared.ServiceID != pairing.ServiceID {
		return suiteHTTPError(409, "REGISTRATION_CONFLICT", "The association changed while completing this connection.")
	}
	if prepared.Replay == nil {
		raw, err := s.fetchSuiteDeclaration(ctx, a.PeerOrigin, "")
		if err != nil {
			return err
		}
		if _, err = s.store.SuiteCommitRegistration(ctx, caller, a.ProductID, req, a.RegistrationRevision, key, digest, prepared, raw); err != nil {
			return err
		}
	}
	a.State = "completed"
	a.ErrorCode = ""
	a.Message = "Service is registered. MCP availability is tracked separately."
	return nil
}
func (s *Server) deliverConnectionIntent(ctx context.Context, a storage.SuiteConnectionAttempt, intent noxmcp.SuitePairingIntent) error {
	client, closeClient, err := s.suiteLocalClient(a.PeerOrigin)
	if err != nil {
		return err
	}
	defer closeClient()
	raw, err := json.Marshal(map[string]any{"contractVersion": "1.0", "backendOrigin": suiteBackendOrigin, "intent": intent})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.PeerOrigin+"/nox/integration/pairing", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", a.ID)
	res, err := client.Do(req)
	if err != nil {
		return suiteHTTPError(503, "PRODUCT_UNREACHABLE", "The service did not confirm receipt. Resume the same connection to reconcile its state.")
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode == 404 || res.StatusCode == 405 {
		return suiteHTTPError(422, "UNSUPPORTED_INTEGRATION", "The service needs its Suite pairing adapter update before it can connect automatically.")
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return suiteHTTPError(403, "FORBIDDEN", "Pairing delivery redirects are not followed.")
	}
	if res.StatusCode != 200 && res.StatusCode != 202 && res.StatusCode != 204 {
		return suiteHTTPError(503, "PRODUCT_UNREACHABLE", "The service did not accept the pairing request. Check its local integration state and resume.")
	}
	return nil
}
func (s *Server) cancelConnection(ctx context.Context, caller storage.SuiteCaller, a *storage.SuiteConnectionAttempt) error {
	ctx = storage.SuiteConnectionContext(ctx, *a)
	if a.PairingID != "" {
		p, err := s.store.SuiteConnectionPairing(ctx, caller, *a)
		if err != nil {
			return err
		}
		key, digest := connectionDigest("cancel", *a)
		if _, err = s.store.SuiteCancelPairing(ctx, caller, a.PairingID, p.Revision, key, digest); err != nil {
			return err
		}
	}
	a.State = "cancelled"
	a.ErrorCode = ""
	a.Message = "Pending connection cancelled. Existing registered services are unchanged."
	return nil
}
func (s *Server) reconcileConnections(ctx context.Context) {
	attempts, err := s.store.SuiteConnectionAttempts(ctx)
	if err != nil {
		return
	}
	for _, a := range attempts {
		if a.PairingID == "" || (a.State != "waiting" && a.State != "connecting" && a.State != "error") {
			continue
		}
		caller := storage.SuiteCaller{UserID: a.ActorID}
		p, err := s.store.SuiteConnectionPairing(ctx, caller, a)
		if err != nil || p.State != "acknowledged" {
			continue
		}
		claimed, err := s.store.SuiteClaimConnection(ctx, caller, a.Service, a.ID, false)
		if err != nil {
			continue
		}
		work, cancel := context.WithTimeout(ctx, 15*time.Second)
		err = s.connectFoundation(work, caller, &claimed, false)
		s.finishConnectionAttempt(work, caller, &claimed, err)
		cancel()
	}
}
