package storage

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

type trustFixture struct {
	store  *Store
	owner  User
	caller SuiteCaller
	dir    string
}

func newTrustFixture(t *testing.T) *trustFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.UpsertAllowedUser(ctx, "owner@example.com", "ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	f := &trustFixture{store, owner, SuiteCaller{UserID: owner.ID}, dir}
	t.Cleanup(func() { _ = f.store.Close() })
	return f
}
func (f *trustFixture) reopen(t *testing.T) {
	t.Helper()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(context.Background(), f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.store = store
}
func trustDecode[T any](t *testing.T, result SuiteResult, err error) T {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(result.Body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func trustExpectedError(t *testing.T, err error, status int) {
	t.Helper()
	var failure *SuiteError
	if !errors.As(err, &failure) || failure.Status != status {
		t.Fatalf("expected Suite error %d, got %v", status, err)
	}
}
func trustRequest(product, purpose, origin string) noxmcp.SuitePairingRequest {
	return noxmcp.SuitePairingRequest{ContractVersion: "1.0", ProductID: product, Purpose: purpose, BaseUrl: origin}
}
func (f *trustFixture) intent(t *testing.T, req noxmcp.SuitePairingRequest, key string) noxmcp.SuitePairingIntent {
	t.Helper()
	result, err := f.store.SuiteCreatePairing(context.Background(), f.caller, req, "", key, key)
	return trustDecode[noxmcp.SuitePairingIntent](t, result, err)
}
func trustProof(t *testing.T, intent noxmcp.SuitePairingIntent) (noxmcp.SuitePairingProof, json.RawMessage) {
	t.Helper()
	decl := json.RawMessage(fmt.Sprintf(`{"contractVersion":"1.0","productId":%q,"productVersion":"1.0","displayName":"Protocol fixture","capabilities":{},"extension":{"value":true}}`, intent.ProductID))
	value, err := noxmcp.ParseSuiteJSON(decl)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := noxmcp.Fingerprint(value)
	if err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256([]byte("explicit fixture TLS public key"))
	binding := noxmcp.SuitePairingBinding{ContractVersion: "1.0", PairingID: intent.PairingID, Nonce: intent.Nonce, ProductID: intent.ProductID, DeclarationFingerprint: fingerprint, BaseUrl: intent.BaseUrl, TlsPublicKeyFingerprint: base64.RawURLEncoding.EncodeToString(pin[:])}
	secret, err := base64.RawURLEncoding.DecodeString(intent.Secret)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := noxmcp.CreateSuitePairingProof(secret, binding)
	if err != nil {
		t.Fatal(err)
	}
	return noxmcp.SuitePairingProof{ContractVersion: "1.0", Binding: binding, Proof: proof}, decl
}
func (f *trustFixture) provision(t *testing.T, product, purpose string, ack bool) noxmcp.SuiteProvisioning {
	t.Helper()
	intent := f.intent(t, trustRequest(product, purpose, "https://"+product+".example.com"), "create:"+product)
	proof, decl := trustProof(t, intent)
	result, err := f.store.SuiteCompletePairing(context.Background(), intent.PairingID, proof, decl, "https://backend.example.com", "complete:"+product, "complete:"+product)
	out := trustDecode[noxmcp.SuiteProvisioning](t, result, err)
	if _, err := noxmcp.LoadSuiteProvisioning(result.Body); err != nil {
		t.Fatal(err)
	}
	if ack {
		if _, err := f.store.SuiteAcknowledgePairing(context.Background(), SuiteCaller{ServiceToken: out.ServiceCredential}, intent.PairingID, "ack:"+product, "ack:"+product); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestSuiteIssuerKeysAndSecretsSurviveRestart(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	first, err := f.store.SuiteKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Keys) != 1 || first.Issuer == "" {
		t.Fatal("missing initial issuer/key")
	}
	for _, key := range first.Keys {
		raw, _ := json.Marshal(key)
		if _, err := noxmcp.LoadSuiteSigningKey(raw); err != nil {
			t.Fatal(err)
		}
	}
	f.reopen(t)
	second, err := f.store.SuiteKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) {
		t.Fatal("ordinary restart changed authority")
	}
	path := filepath.Join(f.dir, "secrets", "suite")
	for _, name := range []string{filepath.Join(f.dir, "secrets"), path} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatal("secret directory permissions", err)
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("secret file permissions", err)
		}
	}
	// Possession of a public key never reveals a private scalar or PEM.
	if strings.Contains(string(a), `"d"`) || strings.Contains(string(a), "PRIVATE KEY") {
		t.Fatal("private signing material was published")
	}
}

func TestSuitePairingRecoveryAckAndSecretErasure(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	req := trustRequest("arbitrary-product", "product", "https://product.example.com")
	intent := f.intent(t, req, "create")
	proof, decl := trustProof(t, intent)
	first, err := f.store.SuiteCompletePairing(ctx, intent.PairingID, proof, decl, "https://backend.example.com", "complete", "complete")
	if err != nil {
		t.Fatal(err)
	}
	provision := trustDecode[noxmcp.SuiteProvisioning](t, first, nil)
	if len(provision.Service.Grants) != 1 || provision.Service.Grants[0] != "events.read" {
		t.Fatal("ordinary product inherited foundation privileges")
	}
	f.reopen(t)
	// Identical completion returns the original credential, not another generation.
	replay, err := f.store.SuiteCompletePairing(ctx, intent.PairingID, proof, nil, "https://backend.example.com", "complete", "complete")
	if err != nil || !bytes.Equal(first.Body, replay.Body) {
		t.Fatal("lost response recovery changed provisioning", err)
	}
	otherKey, err := f.store.SuiteCompletePairing(ctx, intent.PairingID, proof, nil, "https://backend.example.com", "different-key", "different-key")
	if err != nil || !bytes.Equal(first.Body, otherKey.Body) {
		t.Fatal("identical proof minted a second credential", err)
	}
	caller := SuiteCaller{ServiceToken: provision.ServiceCredential}
	_, err = f.store.SuiteCreatePairing(ctx, caller, trustRequest("unrelated", "product", "https://unrelated.example.com"), "", "unauthorized", "unauthorized")
	trustExpectedError(t, err, 403)
	ack, err := f.store.SuiteAcknowledgePairing(ctx, caller, intent.PairingID, "ack", "ack")
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.store.SuiteAcknowledgePairing(ctx, caller, intent.PairingID, "ack", "ack")
	if err != nil || !bytes.Equal(ack.Body, again.Body) {
		t.Fatal("ack lost response was not idempotent", err)
	}
	_, err = f.store.SuiteCompletePairing(ctx, intent.PairingID, proof, nil, "https://backend.example.com", "complete", "complete")
	trustExpectedError(t, err, 409)
	_, err = f.store.SuiteCreatePairing(ctx, f.caller, req, "", "create", "create")
	trustExpectedError(t, err, 409)
	var count int
	if err := f.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suite_secret_handles`).Scan(&count); err != nil || count != 1 {
		t.Fatal("ack retained provisioning/pairing secrets", count, err)
	}
	// Suite secrets are absent from SQLite, its WAL, and ordinary management output.
	for _, suffix := range []string{"", "-wal"} {
		raw, _ := os.ReadFile(f.store.DBPath() + suffix)
		if bytes.Contains(raw, []byte(intent.Secret)) || bytes.Contains(raw, []byte(provision.ServiceCredential)) {
			t.Fatal("Suite secret persisted in ordinary SQLite state")
		}
	}
	view, err := f.store.SuiteReadServices(ctx, f.caller, provision.Service.ServiceID)
	if err != nil || bytes.Contains(view.Body, []byte(provision.ServiceCredential)) {
		t.Fatal("management view disclosed service credential", err)
	}
	if _, valid, err := f.store.AuthenticateAPIKey(ctx, provision.ServiceCredential); err != nil || valid {
		t.Fatal("service credential authenticated as a personal key", err)
	}
	f.reopen(t)
	if _, err := f.store.SuiteReadServices(ctx, caller, provision.Service.ServiceID); err != nil {
		t.Fatal("acknowledged credential did not survive restart", err)
	}
}

func TestSuiteProofBindingExpiryCancellationAndCurrentAuthorization(t *testing.T) {
	for _, field := range []string{"nonce", "product", "origin", "declaration", "tls"} {
		t.Run(field, func(t *testing.T) {
			f := newTrustFixture(t)
			intent := f.intent(t, trustRequest("bound-product", "product", "https://bound.example.com"), "create")
			proof, _ := trustProof(t, intent)
			switch field {
			case "nonce":
				proof.Binding.Nonce = strings.Repeat("a", 43)
			case "product":
				proof.Binding.ProductID = "other"
			case "origin":
				proof.Binding.BaseUrl = "https://other.example.com"
			case "declaration":
				proof.Binding.DeclarationFingerprint = strings.Repeat("a", 43)
			case "tls":
				proof.Binding.TlsPublicKeyFingerprint = strings.Repeat("a", 43)
			}
			_, _, err := f.store.SuiteCheckPairingProof(context.Background(), intent.PairingID, proof)
			trustExpectedError(t, err, 403)
		})
	}
	t.Run("expiry", func(t *testing.T) {
		f := newTrustFixture(t)
		intent := f.intent(t, trustRequest("expired-product", "product", "https://expired.example.com"), "create")
		proof, _ := trustProof(t, intent)
		future := time.Now().Add(11 * time.Minute)
		f.store.suiteClock = func() time.Time { return future }
		_, _, err := f.store.SuiteCheckPairingProof(context.Background(), intent.PairingID, proof)
		trustExpectedError(t, err, 409)
		if err := f.store.MaintainSuiteTrust(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		f := newTrustFixture(t)
		intent := f.intent(t, trustRequest("cancel-product", "product", "https://cancel.example.com"), "create")
		proof, _ := trustProof(t, intent)
		var revision string
		_ = json.Unmarshal(intent.Extra["revision"], &revision)
		_, err := f.store.SuiteCancelPairing(context.Background(), f.caller, intent.PairingID, revision, "cancel", "cancel")
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = f.store.SuiteCheckPairingProof(context.Background(), intent.PairingID, proof)
		trustExpectedError(t, err, 409)
	})
	t.Run("creator loses admin", func(t *testing.T) {
		f := newTrustFixture(t)
		intent := f.intent(t, trustRequest("revoked-intent", "product", "https://revoked.example.com"), "create")
		proof, _ := trustProof(t, intent)
		if err := f.store.SetUserRole(context.Background(), f.owner.ID, "USER"); err != nil {
			t.Fatal(err)
		}
		_, _, err := f.store.SuiteCheckPairingProof(context.Background(), intent.PairingID, proof)
		trustExpectedError(t, err, 403)
	})
}

func TestSuiteIdempotencyConcurrencyAndCommitRollback(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	req := trustRequest("concurrent-product", "product", "https://concurrent.example.com")
	var wait sync.WaitGroup
	outcomes := make(chan SuiteResult, 2)
	failures := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			out, err := f.store.SuiteCreatePairing(ctx, f.caller, req, "", "same-key", "same-request")
			outcomes <- out
			failures <- err
		}()
	}
	wait.Wait()
	close(outcomes)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first []byte
	for out := range outcomes {
		if first == nil {
			first = out.Body
		} else if !bytes.Equal(first, out.Body) {
			t.Fatal("concurrent retry created different pairings")
		}
	}
	_, err := f.store.SuiteCreatePairing(ctx, f.caller, req, "", "same-key", "different-request")
	trustExpectedError(t, err, 409)
	provision := f.provision(t, "rollback-product", "gateway", true)
	if _, err := f.store.db.ExecContext(ctx, `CREATE TRIGGER reject_suite_event BEFORE INSERT ON suite_trust_events BEGIN SELECT RAISE(ABORT,'injected commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteRevokeCredential(ctx, f.caller, provision.Service.ServiceID, provision.Service.CredentialID, provision.Service.CredentialRevision, "revoke", "revoke")
	if err == nil {
		t.Fatal("injected transaction failure ignored")
	}
	if _, err := f.store.SuiteReadServices(ctx, SuiteCaller{ServiceToken: provision.ServiceCredential}, provision.Service.ServiceID); err != nil {
		t.Fatal("failed revoke still changed credential", err)
	}
	if _, err := f.store.db.ExecContext(ctx, `DROP TRIGGER reject_suite_event`); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteRevokeCredential(ctx, f.caller, provision.Service.ServiceID, provision.Service.CredentialID, provision.Service.CredentialRevision, "revoke", "revoke")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteReadServices(ctx, SuiteCaller{ServiceToken: provision.ServiceCredential}, provision.Service.ServiceID)
	trustExpectedError(t, err, 401)
}

func TestSuiteCredentialRotationOverlapAckAndRecovery(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	provision := f.provision(t, "rotating-service", "gateway", true)
	old := SuiteCaller{ServiceToken: provision.ServiceCredential}
	first, err := f.store.SuiteRotateCredential(ctx, old, provision.Service.ServiceID, provision.Service.CredentialRevision, "rotate", "rotate")
	if err != nil {
		t.Fatal(err)
	}
	var rotated struct {
		Service    noxmcp.SuiteServiceIdentity `json:"service"`
		Credential string                      `json:"serviceCredential"`
	}
	if err := json.Unmarshal(first.Body, &rotated); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SuiteReadServices(ctx, old, provision.Service.ServiceID); err != nil {
		t.Fatal("old credential retired before overlap", err)
	}
	f.reopen(t)
	replay, err := f.store.SuiteRotateCredential(ctx, old, provision.Service.ServiceID, provision.Service.CredentialRevision, "rotate", "rotate")
	if err != nil || !bytes.Equal(first.Body, replay.Body) {
		t.Fatal("rotation recovery changed replacement", err)
	}
	replacement := SuiteCaller{ServiceToken: rotated.Credential}
	if _, err := f.store.SuiteAcknowledgeCredential(ctx, replacement, provision.Service.ServiceID, rotated.Service.CredentialID, "ack-rotation", "ack-rotation"); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteReadServices(ctx, old, provision.Service.ServiceID)
	trustExpectedError(t, err, 401)
	if _, err := f.store.SuiteReadServices(ctx, replacement, provision.Service.ServiceID); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteRotateCredential(ctx, replacement, provision.Service.ServiceID, rotated.Service.CredentialRevision, "rotate", "rotate")
	trustExpectedError(t, err, 409)
	second, err := f.store.SuiteRotateCredential(ctx, replacement, provision.Service.ServiceID, rotated.Service.CredentialRevision, "rotate-again", "rotate-again")
	if err != nil {
		t.Fatal(err)
	}
	var pending struct {
		Service    noxmcp.SuiteServiceIdentity `json:"service"`
		Credential string                      `json:"serviceCredential"`
	}
	_ = json.Unmarshal(second.Body, &pending)
	future := time.Now().Add(6 * time.Minute)
	f.store.suiteClock = func() time.Time { return future }
	_, err = f.store.SuiteReadServices(ctx, replacement, provision.Service.ServiceID)
	trustExpectedError(t, err, 401)
	_, err = f.store.SuiteAcknowledgeCredential(ctx, SuiteCaller{ServiceToken: pending.Credential}, provision.Service.ServiceID, pending.Service.CredentialID, "late", "late")
	trustExpectedError(t, err, 401)
	// Explicit admin recovery cannot revive the expired predecessor generation.
	recovered, err := f.store.SuiteRotateCredential(ctx, f.caller, provision.Service.ServiceID, pending.Service.CredentialRevision, "admin-recovery", "admin-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.Body) == 0 {
		t.Fatal("missing recovery")
	}
	_, err = f.store.SuiteReadServices(ctx, replacement, provision.Service.ServiceID)
	trustExpectedError(t, err, 401)
}

func TestSuiteInstallerMayPairOnlyAuthorizedDeployment(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	installer := f.provision(t, "generic-installer", "installer", true)
	caller := SuiteCaller{ServiceToken: installer.ServiceCredential}
	req := trustRequest("future-domain-product", "product", "https://future.example.com")
	req.ManagedBy = &noxmcp.SuiteRegistrationRecordManagedBy{ProductID: installer.Service.ProductID, DeploymentID: "opaque-deployment"}
	_, err := f.store.SuiteCreatePairing(ctx, caller, req, "", "before-grant", "before-grant")
	trustExpectedError(t, err, 403)
	grant := &SuiteDeploymentGrant{ProductID: req.ProductID, BaseURL: req.BaseUrl}
	_, err = f.store.SuiteSetDeployment(ctx, caller, installer.Service.ServiceID, "opaque-deployment", "", "self-grant", "self-grant", grant)
	trustExpectedError(t, err, 403)
	result, err := f.store.SuiteSetDeployment(ctx, f.caller, installer.Service.ServiceID, "opaque-deployment", "", "grant", "grant", grant)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Revision string `json:"revision"`
	}
	_ = json.Unmarshal(result.Body, &view)
	paired, err := f.store.SuiteCreatePairing(ctx, caller, req, "", "authorized", "authorized")
	if err != nil {
		t.Fatal(err)
	}
	intent := trustDecode[noxmcp.SuitePairingIntent](t, paired, nil)
	proof, _ := trustProof(t, intent)
	wrong := req
	wrong.ProductID = "another-product"
	_, err = f.store.SuiteCreatePairing(ctx, caller, wrong, "", "wrong", "wrong")
	trustExpectedError(t, err, 403)
	_, err = f.store.SuiteSetDeployment(ctx, f.caller, installer.Service.ServiceID, "opaque-deployment", view.Revision, "withdraw", "withdraw", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.store.SuiteCheckPairingProof(ctx, intent.PairingID, proof)
	trustExpectedError(t, err, 403)
	gateway := f.provision(t, "generic-gateway", "gateway", true)
	_, err = f.store.SuiteCreatePairing(ctx, SuiteCaller{ServiceToken: gateway.ServiceCredential}, req, "", "gateway-enroll", "gateway-enroll")
	trustExpectedError(t, err, 403)
}

func TestSuiteSigningKeyRotationRetentionRevocationAndRestart(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	before, err := f.store.SuiteKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := f.store.SuitePrepareSigningKey(ctx, f.caller, before.Revision, "prepare", "prepare")
	if err != nil {
		t.Fatal(err)
	}
	var key struct {
		ID       string `json:"keyId"`
		Revision string `json:"revision"`
	}
	_ = json.Unmarshal(prepared.Body, &key)
	published, err := f.store.SuiteKeys(ctx)
	if err != nil || len(published.Keys) != 2 {
		t.Fatal("new key not advertised before activation", err)
	}
	if _, err := f.store.SuiteActivateSigningKey(ctx, f.caller, key.ID, key.Revision, "activate", "activate"); err != nil {
		t.Fatal(err)
	}
	// The current private key signs a fixture and verifies against the published JWK.
	_, err = f.store.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		private, id, err := x.activeSigningKey()
		if err != nil {
			return SuiteResult{}, err
		}
		if id != key.ID {
			t.Fatal("wrong active key")
		}
		header, _ := json.Marshal(map[string]string{"alg": "ES256", "typ": "nox-delegation+jwt", "kid": id})
		payload, _ := json.Marshal(map[string]any{"iss": before.Issuer, "aud": "https://product.example.com/mcp", "sub": f.owner.ID, "act": map[string]string{"sub": "gateway-fixture"}, "iat": x.now.Unix(), "nbf": x.now.Unix(), "exp": x.now.Add(5 * time.Minute).Unix(), "jti": "fixture-jti", "scope": "nox:inspect", "nox": map[string]string{"contractVersion": "1.0", "keyId": "personal-key-fixture", "authRevision": "auth-fixture", "serviceCredentialRevision": "credential-fixture", "productId": "generic-product", "registrationRevision": "registration-fixture", "associationId": "association-fixture"}})
		input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
		hash := sha256.Sum256([]byte(input))
		r, s, err := ecdsa.Sign(rand.Reader, private, hash[:])
		if err != nil {
			return SuiteResult{}, err
		}
		signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
		token := input + "." + base64.RawURLEncoding.EncodeToString(signature)
		keys := map[string]noxmcp.SuiteSigningKey{}
		for _, jwk := range published.Keys {
			keys[*jwk.Kid] = jwk
		}
		_, err = noxmcp.VerifySuiteToken(token, noxmcp.SuiteVerificationOptions{Issuer: before.Issuer, Audience: "https://product.example.com/mcp", ProductID: "generic-product", AssociationID: "association-fixture", GatewayServiceID: "gateway-fixture", RegistrationRevision: "registration-fixture", ServiceCredentialRevision: "credential-fixture", Keys: keys, Now: x.now})
		if err != nil {
			t.Fatal("persisted signing key did not verify through the released SDK", err)
		}
		return SuiteResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := f.store.SuiteKeys(ctx)
	if err != nil || len(retained.Keys) != 2 {
		t.Fatal("old key removed before maximum token expiry", err)
	}
	f.reopen(t)
	retained, err = f.store.SuiteKeys(ctx)
	if err != nil || retained.Issuer != before.Issuer {
		t.Fatal("rotation reset issuer", err)
	}
	if _, err := f.store.SuiteRevokeSigningKey(ctx, f.caller, key.ID, retained.Revision, "revoke-key", "revoke-key"); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	_, err = f.store.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		_, _, err := x.activeSigningKey()
		if err == nil {
			t.Fatal("revoked signing key was resurrected")
		}
		return SuiteResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(6 * time.Minute)
	f.store.suiteClock = func() time.Time { return future }
	if err := f.store.MaintainSuiteTrust(ctx); err != nil {
		t.Fatal(err)
	}
	empty, err := f.store.SuiteKeys(ctx)
	if err != nil || len(empty.Keys) != 0 {
		t.Fatal("expired old verification key remained active", err)
	}
}
func TestSuiteMissingMasterKeyFailsClosed(t *testing.T) {
	f := newTrustFixture(t)
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	master := filepath.Join(f.dir, "secrets", "suite", "master.key")
	if err := os.Remove(master); err != nil {
		t.Fatal(err)
	}
	if opened, err := Open(context.Background(), f.dir); err == nil {
		_ = opened.Close()
		t.Fatal("lost secret-store key silently reset authority")
	}
	if _, err := os.Stat(master); !os.IsNotExist(err) {
		t.Fatal("missing master key was regenerated")
	}
}

func TestSuiteCompetingProofsCannotProvisionDifferentCredentials(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	intent := f.intent(t, trustRequest("competing-product", "product", "https://competing.example.com"), "create")
	first, decl := trustProof(t, intent)
	second := first
	hash := sha256.Sum256([]byte("other TLS key"))
	second.Binding.TlsPublicKeyFingerprint = base64.RawURLEncoding.EncodeToString(hash[:])
	secret, _ := base64.RawURLEncoding.DecodeString(intent.Secret)
	var err error
	second.Proof, err = noxmcp.CreateSuitePairingProof(secret, second.Binding)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	outcomes := make(chan error, 2)
	for i, proof := range []noxmcp.SuitePairingProof{first, second} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := f.store.SuiteCompletePairing(ctx, intent.PairingID, proof, decl, "https://backend.example.com", fmt.Sprint(i), fmt.Sprint(i))
			outcomes <- err
		}()
	}
	wait.Wait()
	close(outcomes)
	success, conflict := 0, 0
	for err := range outcomes {
		if err == nil {
			success++
		} else {
			var failure *SuiteError
			if errors.As(err, &failure) && failure.Status == 409 {
				conflict++
			} else {
				t.Fatal(err)
			}
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("competing completions created independent outcomes", success, conflict)
	}
	var count int
	if err := f.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suite_credentials`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate service credential", count, err)
	}
}
func TestSuiteExplicitRepairRetainsOldServiceUntilNewAcknowledgment(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	old := f.provision(t, "repair-product", "product", true)
	view, err := f.store.SuiteReadServices(ctx, f.caller, old.Service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Revision string `json:"revision"`
	}
	_ = json.Unmarshal(view.Body, &state)
	request := trustRequest("repair-product", "product", "https://new-origin.example.com")
	_, err = f.store.SuiteCreatePairing(ctx, f.caller, request, "", "stale-create", "stale-create")
	trustExpectedError(t, err, 412)
	result, err := f.store.SuiteCreatePairing(ctx, f.caller, request, state.Revision, "repair", "repair")
	if err != nil {
		t.Fatal(err)
	}
	intent := trustDecode[noxmcp.SuitePairingIntent](t, result, nil)
	proof, decl := trustProof(t, intent)
	result, err = f.store.SuiteCompletePairing(ctx, intent.PairingID, proof, decl, "https://backend.example.com", "complete-repair", "complete-repair")
	if err != nil {
		t.Fatal(err)
	}
	replacement := trustDecode[noxmcp.SuiteProvisioning](t, result, nil)
	if _, err := f.store.SuiteReadServices(ctx, SuiteCaller{ServiceToken: old.ServiceCredential}, old.Service.ServiceID); err != nil {
		t.Fatal("old association retired before replacement acknowledgment", err)
	}
	if _, err := f.store.SuiteAcknowledgePairing(ctx, SuiteCaller{ServiceToken: replacement.ServiceCredential}, intent.PairingID, "ack-repair", "ack-repair"); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteReadServices(ctx, SuiteCaller{ServiceToken: old.ServiceCredential}, old.Service.ServiceID)
	trustExpectedError(t, err, 401)
	if _, err := f.store.SuiteReadServices(ctx, SuiteCaller{ServiceToken: replacement.ServiceCredential}, replacement.Service.ServiceID); err != nil {
		t.Fatal(err)
	}
}
func TestSuiteClockRollbackFailsClosedWithoutRegeneratingAuthority(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	before, err := f.store.SuiteKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	f.store.suiteClock = func() time.Time { return past }
	_, err = f.store.SuiteKeys(ctx)
	trustExpectedError(t, err, 503)
	f.store.suiteClock = nil
	after, err := f.store.SuiteKeys(ctx)
	if err != nil || after.Issuer != before.Issuer || after.Revision != before.Revision {
		t.Fatal("clock failure regenerated trust", err)
	}
}

func TestSuiteServiceRevocationRetiresEveryCredentialGeneration(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	provision := f.provision(t, "revoked-service", "gateway", true)
	rotated, err := f.store.SuiteRotateCredential(ctx, f.caller, provision.Service.ServiceID, provision.Service.CredentialRevision, "rotate", "rotate")
	if err != nil {
		t.Fatal(err)
	}
	var replacement struct {
		Credential string `json:"serviceCredential"`
	}
	_ = json.Unmarshal(rotated.Body, &replacement)
	view, err := f.store.SuiteReadServices(ctx, f.caller, provision.Service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Revision string `json:"revision"`
	}
	_ = json.Unmarshal(view.Body, &record)
	if _, err := f.store.SuiteRevokeService(ctx, f.caller, provision.Service.ServiceID, record.Revision, "revoke-service", "revoke-service"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{provision.ServiceCredential, replacement.Credential} {
		_, err := f.store.SuiteAuthenticateService(ctx, token)
		trustExpectedError(t, err, 401)
	}
	var events int
	if err := f.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suite_trust_events WHERE type='service.invalidated'`).Scan(&events); err != nil || events < 2 {
		t.Fatal("service revocation lacked durable invalidations", events, err)
	}
	f.reopen(t)
	_, err = f.store.SuiteAuthenticateService(ctx, provision.ServiceCredential)
	trustExpectedError(t, err, 401)
}
