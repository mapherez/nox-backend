package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

// Publication uses the existing authenticated administrator and unchanged
// pairing/proof/ACK/registry checks. There is no privileged internal caller.
func (s *Server) handleBackendPublication(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, http.MethodPost) {
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 {
		writeJSONError(w, 400, "BAD_REQUEST", "Backend publication accepts no arguments.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := s.publishBackend(ctx, storage.SuiteCaller{UserID: actor.ID}); err != nil {
		writeSuiteError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"enabled": s.cfg.SuiteMCPEnabled})
}

func (s *Server) publishBackend(ctx context.Context, caller storage.SuiteCaller) error {
	s.backendPublicationMu.Lock()
	defer s.backendPublicationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := s.store.SuiteAdministration(ctx, caller)
	if err != nil {
		return err
	}
	var record *noxmcp.SuiteRegistrationRecord
	for i := range state.Products {
		if state.Products[i].ProductID == serviceName {
			record = &state.Products[i]
			break
		}
	}
	if !s.cfg.SuiteMCPEnabled && record == nil {
		return nil
	}
	if _, err = s.suiteOrigin(); err != nil {
		return err
	}
	declaration, err := s.fetchSuiteDeclaration(ctx, suiteBackendOrigin, "")
	if err != nil {
		return err
	}
	doc, err := noxmcp.LoadSuiteProductDeclaration(declaration)
	if err != nil || doc.ProductID != serviceName {
		return suiteHTTPError(409, "REGISTRATION_CONFLICT", "The local Backend declaration does not match its identity.")
	}
	expected := ""
	if record != nil {
		if record.ManagedBy != nil || record.Connection.BaseUrl != suiteBackendOrigin {
			return suiteHTTPError(409, "REGISTRATION_CONFLICT", "The Backend has a different association. Reconcile it before publishing its tools.")
		}
		expected = record.Revision
	}
	req := noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: suiteBackendOrigin,
		Extra: map[string]json.RawMessage{"nox/backend-publication": json.RawMessage("true")}}
	prepared, err := s.store.SuitePrepareRegistration(ctx, caller, serviceName, req, expected, rand.Text(), rand.Text())
	if err != nil {
		// Known trust heads, including revoked ones, are never replaced here.
		for _, service := range state.Services {
			if service.ProductID == serviceName && service.Current {
				return err
			}
		}
		var failure *storage.SuiteError
		if !errors.As(err, &failure) || failure.Code != "REGISTRATION_CONFLICT" {
			return err
		}
		if err = s.pairBackend(ctx, caller, declaration); err != nil {
			return err
		}
		prepared, err = s.store.SuitePrepareRegistration(ctx, caller, serviceName, req, expected, rand.Text(), rand.Text())
		if err != nil {
			return err
		}
	}
	_, err = s.store.SuiteCommitRegistration(ctx, caller, serviceName, req, expected, rand.Text(), rand.Text(), prepared, declaration)
	if err == nil {
		s.backendPublicationDone = true
	}
	return err
}

func (s *Server) pairBackend(ctx context.Context, caller storage.SuiteCaller, declaration json.RawMessage) error {
	// Interrupted pre-ACK attempts are retired only when marked as this workflow.
	// An independent administrator's pairing is not canceled by publication.
	pending, err := s.store.SuiteBackendPublicationPairings(ctx, caller)
	if err != nil {
		return err
	}
	for _, p := range pending {
		if _, err = s.store.SuiteCancelPairing(ctx, caller, p.PairingID, p.Revision, rand.Text(), rand.Text()); err != nil {
			return err
		}
	}
	req := noxmcp.SuitePairingRequest{ContractVersion: "1.0", ProductID: serviceName, Purpose: "product", BaseUrl: suiteBackendOrigin,
		Extra: map[string]json.RawMessage{"nox/backend-publication": json.RawMessage("true")}}
	result, err := s.store.SuiteCreatePairing(ctx, caller, req, "", rand.Text(), rand.Text())
	if err != nil {
		return err
	}
	intent, err := noxmcp.LoadSuitePairingIntent(result.Body)
	if err != nil {
		return err
	}
	value, err := noxmcp.ParseSuiteJSON(declaration)
	if err != nil {
		return err
	}
	fingerprint, err := noxmcp.Fingerprint(value)
	if err != nil {
		return err
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(intent.Secret)
	if err != nil {
		return err
	}
	binding := noxmcp.SuitePairingBinding{ContractVersion: "1.0", PairingID: intent.PairingID, Nonce: intent.Nonce, ProductID: serviceName, BaseUrl: suiteBackendOrigin, DeclarationFingerprint: fingerprint}
	proof, err := noxmcp.CreateSuitePairingProof(secret, binding)
	if err != nil {
		return err
	}
	result, err = s.store.SuiteCompletePairing(ctx, intent.PairingID, noxmcp.SuitePairingProof{ContractVersion: "1.0", Binding: binding, Proof: proof}, declaration, suiteBackendOrigin, rand.Text(), rand.Text())
	if err != nil {
		return err
	}
	provision, err := noxmcp.LoadSuiteProvisioning(result.Body)
	if err != nil {
		return err
	}
	// CompletePairing durably stores provisioning in the encrypted recovery
	// vault before ACK. No protocol credential is sent to the browser.
	_, err = s.store.SuiteAcknowledgePairing(ctx, storage.SuiteCaller{ServiceToken: provision.ServiceCredential}, intent.PairingID, rand.Text(), rand.Text())
	return err
}

func (s *Server) reconcileBackendPublication(ctx context.Context) {
	s.backendPublicationMu.Lock()
	done := s.backendPublicationDone
	s.backendPublicationMu.Unlock()
	if done || s.cfg.SuiteSocketDir == "" {
		return
	}
	user, err := s.store.SuiteBackendPublicationActor(ctx)
	if err != nil || user.ID == "" {
		return
	}
	work, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err = s.accountAdmission(work, user); err != nil {
		return
	}
	_ = s.publishBackend(work, storage.SuiteCaller{UserID: user.ID})
}
