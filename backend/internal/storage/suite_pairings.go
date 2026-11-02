package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

type suitePairing struct {
	ID, State, Revision, ExpectedRevision, Nonce                         string
	ExpiresAt                                                            int64
	Request                                                              noxmcp.SuitePairingRequest
	CreatorUserID, CreatorServiceID, CreatorCredentialID                 string
	SecretHandle, IntentHandle, ProvisioningHandle, ServiceID, ProofHash string
}

func (x *suiteTx) pairing(id string) (suitePairing, error) {
	var p suitePairing
	var raw string
	err := x.tx.QueryRowContext(x.ctx, `SELECT record_json FROM suite_pairings WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, suiteError(404, "PAIRING_NOT_FOUND", "Pairing not found.")
	}
	if err != nil {
		return p, err
	}
	err = suiteDecode(raw, &p)
	return p, err
}
func (x *suiteTx) putPairing(p suitePairing) error {
	raw, err := suiteJSON(p)
	if err != nil {
		return err
	}
	_, err = x.tx.ExecContext(x.ctx, `INSERT INTO suite_pairings VALUES (?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,expires_at=excluded.expires_at,record_json=excluded.record_json`, p.ID, p.Request.ProductID, p.State, p.ExpiresAt, raw)
	return err
}
func (x *suiteTx) head(product string) (string, string, error) {
	var id, revision string
	err := x.tx.QueryRowContext(x.ctx, `SELECT service_id,revision FROM suite_trust_heads WHERE product_id=?`, product).Scan(&id, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return id, revision, err
}
func (x *suiteTx) checkHead(product, expected string) error {
	_, actual, err := x.head(product)
	if err != nil {
		return err
	}
	if actual != expected {
		return suitePrecondition()
	}
	return nil
}
func (x *suiteTx) deploymentAllowed(service suiteService, req noxmcp.SuitePairingRequest) error {
	if req.Purpose != "product" || req.ManagedBy == nil || req.ManagedBy.ProductID != service.ProductID {
		return suiteDenied()
	}
	var product, origin string
	err := x.tx.QueryRowContext(x.ctx, `SELECT product_id,origin FROM suite_deployments WHERE service_id=? AND deployment_id=?`, service.ID, req.ManagedBy.DeploymentID).Scan(&product, &origin)
	if errors.Is(err, sql.ErrNoRows) {
		return suiteDenied()
	}
	if err != nil {
		return err
	}
	if product != req.ProductID || origin != req.BaseUrl {
		return suiteDenied()
	}
	return nil
}
func (x *suiteTx) authorizePairing(a suiteActor, req noxmcp.SuitePairingRequest) error {
	if a.admin() {
		if req.ManagedBy != nil {
			installerID, _, err := x.head(req.ManagedBy.ProductID)
			if err != nil {
				return err
			}
			if installerID == "" {
				return suiteDenied()
			}
			installer, err := x.service(installerID)
			if err != nil {
				return err
			}
			if installer.State != "active" || installer.Purpose != "installer" {
				return suiteDenied()
			}
			return x.deploymentAllowed(installer, req)
		}
		return nil
	}
	if a.service == nil || a.service.Purpose != "installer" || !a.grant("pairing.manage-associated") {
		return suiteDenied()
	}
	return x.deploymentAllowed(*a.service, req)
}
func (x *suiteTx) pairingCreator(p suitePairing) error {
	if p.CreatorUserID != "" {
		a, err := x.actor(SuiteCaller{UserID: p.CreatorUserID})
		if err != nil {
			return err
		}
		if !a.admin() {
			return suiteDenied()
		}
		return x.authorizePairing(a, p.Request)
	}
	service, err := x.service(p.CreatorServiceID)
	if err != nil {
		return err
	}
	credential, err := x.credential(p.CreatorCredentialID)
	if err != nil {
		return err
	}
	if credential.State == "revoked" || credential.State == "provisioned" || x.deadlineExpired(credential.NotAfter) {
		return suiteDenied()
	}
	return x.authorizePairing(suiteActor{service: &service, credential: &credential}, p.Request)
}
func suiteGrants(purpose string) []string {
	switch purpose {
	case "gateway":
		return []string{"registry.read", "authorization.delegate", "events.read", "mcp.report", "mcp.probe"}
	case "installer":
		return []string{"registry.read", "registry.manage-associated", "events.read", "lifecycle.report", "pairing.manage-associated"}
	default:
		return []string{"events.read"}
	}
}
func (s *Store) SuiteCreatePairing(ctx context.Context, caller SuiteCaller, req noxmcp.SuitePairingRequest, expected, key, digest string) (SuiteResult, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return SuiteResult{}, err
	}
	req, err = noxmcp.LoadSuitePairingRequest(raw)
	if err != nil {
		return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "Invalid pairing request.")
	}
	req.BaseUrl, err = noxmcp.NormalizeSuiteOrigin(req.BaseUrl)
	if err != nil {
		return SuiteResult{}, err
	}
	authorize := func(x *suiteTx, a suiteActor) error { return x.authorizePairing(a, req) }
	return s.suiteMutation(ctx, caller, "pairings.create", key, digest, authorize, func(x *suiteTx, a suiteActor) (SuiteResult, error) {
		if err := x.maintain(); err != nil {
			return SuiteResult{}, err
		}
		if err := x.checkHead(req.ProductID, expected); err != nil {
			return SuiteResult{}, err
		}
		var pending int
		if err := x.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM suite_pairings WHERE product_id=? AND state IN ('pending','provisioned')`, req.ProductID).Scan(&pending); err != nil {
			return SuiteResult{}, err
		}
		if pending != 0 {
			return SuiteResult{}, suiteConflict()
		}
		id, err := randomID("pairing_")
		if err != nil {
			return SuiteResult{}, err
		}
		nonce, err := suiteRandom()
		if err != nil {
			return SuiteResult{}, err
		}
		secret, err := suiteRandom()
		if err != nil {
			return SuiteResult{}, err
		}
		revision, err := randomID("pairing_revision_")
		if err != nil {
			return SuiteResult{}, err
		}
		expiry := x.now.Add(10 * time.Minute)
		secretBytes, err := base64.RawURLEncoding.DecodeString(secret)
		if err != nil {
			return SuiteResult{}, err
		}
		handle, err := x.secret(secretBytes, expiry)
		if err != nil {
			return SuiteResult{}, err
		}
		intent := noxmcp.SuitePairingIntent{ContractVersion: "1.0", PairingID: id, ProductID: req.ProductID, BaseUrl: req.BaseUrl, Purpose: req.Purpose, Nonce: nonce, Secret: secret, ExpiresAt: suiteTime(expiry), ManagedBy: req.ManagedBy, Extra: map[string]json.RawMessage{}}
		intent.Extra["revision"], err = json.Marshal(revision)
		if err != nil {
			return SuiteResult{}, err
		}
		result, err := x.secretResponse(201, intent, expiry)
		if err != nil {
			return result, err
		}
		p := suitePairing{ID: id, State: "pending", Revision: revision, ExpectedRevision: expected, Nonce: nonce, ExpiresAt: expiry.UnixMilli(), Request: req, SecretHandle: handle, IntentHandle: result.secretHandle}
		if a.user != nil {
			p.CreatorUserID = a.user.ID
		} else {
			p.CreatorServiceID = a.service.ID
			p.CreatorCredentialID = a.credential.ID
		}
		if err := x.putPairing(p); err != nil {
			return SuiteResult{}, err
		}
		return result, x.bindConnectionPairing(p.ID)
	})
}
func (x *suiteTx) checkProof(id string, proof noxmcp.SuitePairingProof) (suitePairing, error) {
	p, err := x.pairing(id)
	if err == nil {
		err = x.checkConnectionPairing(id)
	}
	if err != nil {
		return p, err
	}
	if p.State != "pending" && p.State != "provisioned" {
		return p, suiteRecoveryClosed()
	}
	if x.deadlineExpired(p.ExpiresAt) {
		return p, suiteRecoveryClosed()
	}
	if err := x.pairingCreator(p); err != nil {
		return p, err
	}
	if err := x.checkHead(p.Request.ProductID, p.ExpectedRevision); err != nil {
		return p, err
	}
	b := proof.Binding
	origin, err := noxmcp.NormalizeSuiteOrigin(b.BaseUrl)
	if err != nil {
		return p, suiteDenied()
	}
	if b.PairingID != id || b.ProductID != p.Request.ProductID || origin != p.Request.BaseUrl || b.Nonce != p.Nonce {
		return p, suiteDenied()
	}
	secret, err := x.readSecret(p.SecretHandle)
	if err != nil {
		return p, err
	}
	if !noxmcp.VerifySuitePairingProof(secret, b, proof.Proof) {
		return p, suiteDenied()
	}
	raw, err := json.Marshal(proof)
	if err != nil {
		return p, err
	}
	if p.State == "provisioned" && p.ProofHash != suiteProofHash(raw) {
		return p, suiteConflict()
	}
	return p, nil
}

// Check first, before any network access. Completion repeats this check in the
// commit transaction, after the verified declaration has been fetched.
func (s *Store) SuiteCheckPairingProof(ctx context.Context, id string, proof noxmcp.SuitePairingProof) (noxmcp.SuitePairingRequest, bool, error) {
	var req noxmcp.SuitePairingRequest
	var completed bool
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		p, err := x.checkProof(id, proof)
		req = p.Request
		completed = p.State == "provisioned"
		return SuiteResult{}, err
	})
	return req, completed, err
}
func (s *Store) SuiteCompletePairing(ctx context.Context, id string, proof noxmcp.SuitePairingProof, declaration json.RawMessage, backendOrigin, key, digest string) (SuiteResult, error) {
	origin, err := noxmcp.NormalizeSuiteOrigin(backendOrigin)
	if err != nil {
		return SuiteResult{}, suiteError(503, "INTEGRATION_UNAVAILABLE", "The Backend Suite protocol origin is invalid.")
	}
	return s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		p, err := x.checkProof(id, proof)
		if err != nil {
			return SuiteResult{}, err
		}
		principal, operation := "pairing:"+id, "pairings.complete:"+id
		if result, ok, err := x.replay(principal, operation, key, digest); ok || err != nil {
			return result, err
		}
		if p.State == "provisioned" {
			body, err := x.readSecret(p.ProvisioningHandle)
			if err != nil {
				return SuiteResult{}, err
			}
			result := SuiteResult{Status: 200, Body: body, secretHandle: p.ProvisioningHandle}
			return result, x.remember(principal, operation, key, digest, result)
		}
		if err := x.checkHead(p.Request.ProductID, p.ExpectedRevision); err != nil {
			return SuiteResult{}, err
		}
		doc, err := noxmcp.LoadSuiteProductDeclaration(declaration)
		if err != nil {
			return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "The verified product declaration is invalid.")
		}
		if doc.ProductID != p.Request.ProductID {
			return SuiteResult{}, suiteConflict()
		}
		value, err := noxmcp.ParseSuiteJSON(declaration)
		if err != nil {
			return SuiteResult{}, err
		}
		fingerprint, err := noxmcp.Fingerprint(value)
		if err != nil {
			return SuiteResult{}, err
		}
		if fingerprint != proof.Binding.DeclarationFingerprint {
			return SuiteResult{}, suiteConflict()
		}
		serviceID, err := randomID("service_")
		if err != nil {
			return SuiteResult{}, err
		}
		associationID, err := randomID("association_")
		if err != nil {
			return SuiteResult{}, err
		}
		revision, err := randomID("association_revision_")
		if err != nil {
			return SuiteResult{}, err
		}
		service := suiteService{ID: serviceID, ProductID: p.Request.ProductID, AssociationID: associationID, Purpose: p.Request.Purpose, Origin: p.Request.BaseUrl, TLSFingerprint: suiteFingerprintValue(proof.Binding.TlsPublicKeyFingerprint), DeclarationFingerprint: fingerprint, Declaration: declaration, ManagedBy: p.Request.ManagedBy, Grants: suiteGrants(p.Request.Purpose), State: "provisioned", Revision: revision}
		if err := x.putService(service); err != nil {
			return SuiteResult{}, err
		}
		credential, token, err := x.newCredential(serviceID, "provisioned", p.ExpiresAt)
		if err != nil {
			return SuiteResult{}, err
		}
		credential.PairingID = id
		service.CurrentCredentialID = credential.ID
		if err := x.putService(service); err != nil {
			return SuiteResult{}, err
		}
		var issuer string
		if err := x.tx.QueryRowContext(ctx, `SELECT issuer FROM suite_authority WHERE singleton=1`).Scan(&issuer); err != nil {
			return SuiteResult{}, err
		}
		provisioning := noxmcp.SuiteProvisioning{ContractVersion: "1.0", PairingID: id, Service: x.identity(service, credential), ServiceCredential: token, Issuer: issuer, BackendOrigin: origin, SigningKeysUri: origin + "/api/nox/keys"}
		result, err := x.secretResponse(200, provisioning, time.UnixMilli(p.ExpiresAt))
		if err != nil {
			return result, err
		}
		if err := x.putCredential(credential); err != nil {
			return SuiteResult{}, err
		}
		raw, err := json.Marshal(proof)
		if err != nil {
			return SuiteResult{}, err
		}
		p.ProofHash = suiteProofHash(raw)
		p.State = "provisioned"
		p.ServiceID = serviceID
		p.ProvisioningHandle = result.secretHandle
		p.Revision, err = randomID("pairing_revision_")
		if err != nil {
			return SuiteResult{}, err
		}
		if err := x.putPairing(p); err != nil {
			return SuiteResult{}, err
		}
		return result, x.remember(principal, operation, key, digest, result)
	})
}
func (s *Store) SuiteAcknowledgePairing(ctx context.Context, caller SuiteCaller, id, key, digest string) (SuiteResult, error) {
	authorize := func(x *suiteTx, a suiteActor) error {
		if err := x.checkConnectionPairing(id); err != nil {
			return err
		}
		p, err := x.pairing(id)
		if err != nil {
			return err
		}
		if a.service == nil || a.service.ID != p.ServiceID || a.credential.PairingID != id {
			return suiteDenied()
		}
		return nil
	}
	return s.suiteMutation(ctx, caller, "pairings.ack:"+id, key, digest, authorize, func(x *suiteTx, a suiteActor) (SuiteResult, error) {
		p, err := x.pairing(id)
		if err != nil {
			return SuiteResult{}, err
		}
		if p.State != "provisioned" || x.deadlineExpired(p.ExpiresAt) {
			return SuiteResult{}, suiteRecoveryClosed()
		}
		if err := x.pairingCreator(p); err != nil {
			return SuiteResult{}, err
		}
		if err := x.checkHead(p.Request.ProductID, p.ExpectedRevision); err != nil {
			return SuiteResult{}, err
		}
		oldID, _, err := x.head(p.Request.ProductID)
		if err != nil {
			return SuiteResult{}, err
		}
		if oldID != "" {
			old, err := x.service(oldID)
			if err != nil {
				return SuiteResult{}, err
			}
			if err := x.revokeService(old); err != nil {
				return SuiteResult{}, err
			}
		}
		service, credential := *a.service, *a.credential
		service.State = "active"
		credential.State = "active"
		credential.NotAfter = 0
		if err := x.putService(service); err != nil {
			return SuiteResult{}, err
		}
		if err := x.putCredential(credential); err != nil {
			return SuiteResult{}, err
		}
		if _, err := x.tx.ExecContext(ctx, `INSERT INTO suite_trust_heads VALUES (?,?,?) ON CONFLICT(product_id) DO UPDATE SET service_id=excluded.service_id,revision=excluded.revision`, service.ProductID, service.ID, service.Revision); err != nil {
			return SuiteResult{}, err
		}
		for _, handle := range []string{p.SecretHandle, p.IntentHandle, p.ProvisioningHandle} {
			if err := x.eraseSecret(handle); err != nil {
				return SuiteResult{}, err
			}
		}
		p.SecretHandle = ""
		p.IntentHandle = ""
		p.ProvisioningHandle = ""
		p.State = "acknowledged"
		p.Revision, err = randomID("pairing_revision_")
		if err != nil {
			return SuiteResult{}, err
		}
		if err := x.putPairing(p); err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(200, map[string]any{"acknowledged": true, "service": x.identity(service, credential)})
	})
}
func (x *suiteTx) mayReadPairing(a suiteActor, p suitePairing) error {
	if a.admin() {
		return nil
	}
	if a.service != nil && a.service.ID == p.CreatorServiceID && a.grant("pairing.manage-associated") {
		return x.authorizePairing(a, p.Request)
	}
	return suiteDenied()
}
func (s *Store) SuiteReadPairing(ctx context.Context, caller SuiteCaller, id string) (SuiteResult, error) {
	return s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		a, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if !a.admin() && !a.grant("pairing.manage-associated") {
			return SuiteResult{}, suiteDenied()
		}
		p, err := x.pairing(id)
		if err != nil {
			return SuiteResult{}, err
		}
		if err := x.mayReadPairing(a, p); err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(200, map[string]any{"contractVersion": "1.0", "pairingId": p.ID, "productId": p.Request.ProductID, "baseUrl": p.Request.BaseUrl, "purpose": p.Request.Purpose, "state": p.State, "revision": p.Revision, "expiresAt": suiteTime(time.UnixMilli(p.ExpiresAt))})
	})
}
func (s *Store) SuiteCancelPairing(ctx context.Context, caller SuiteCaller, id, expected, key, digest string) (SuiteResult, error) {
	authorize := func(x *suiteTx, a suiteActor) error {
		if !a.admin() && !a.grant("pairing.manage-associated") {
			return suiteDenied()
		}
		p, err := x.pairing(id)
		if err != nil {
			return err
		}
		return x.mayReadPairing(a, p)
	}
	return s.suiteMutation(ctx, caller, "pairings.cancel:"+id, key, digest, authorize, func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		p, err := x.pairing(id)
		if err != nil {
			return SuiteResult{}, err
		}
		if p.Revision != expected {
			return SuiteResult{}, suitePrecondition()
		}
		if p.State == "acknowledged" {
			return SuiteResult{}, suiteConflict()
		}
		if p.State == "provisioned" {
			service, err := x.service(p.ServiceID)
			if err != nil {
				return SuiteResult{}, err
			}
			if err := x.revokeService(service); err != nil {
				return SuiteResult{}, err
			}
		}
		for _, h := range []string{p.SecretHandle, p.IntentHandle, p.ProvisioningHandle} {
			if err := x.eraseSecret(h); err != nil {
				return SuiteResult{}, err
			}
		}
		p.SecretHandle = ""
		p.IntentHandle = ""
		p.ProvisioningHandle = ""
		p.State = "cancelled"
		p.Revision, err = randomID("pairing_revision_")
		if err != nil {
			return SuiteResult{}, err
		}
		if err := x.putPairing(p); err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(200, map[string]bool{"cancelled": true})
	})
}

func suiteProofHash(raw []byte) string {
	value, err := noxmcp.ParseSuiteJSON(raw)
	if err != nil {
		return ""
	}
	hash, err := noxmcp.Fingerprint(value)
	if err != nil {
		return ""
	}
	return hash
}

func suiteFingerprintValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
