package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

// This helper operates only on the disposable test database to reconstruct the
// actual historical schema; production migrations never remove these objects.
func rewindSuiteAuthorizationSchema(t *testing.T, s *Store) {
	t.Helper()
	body, err := migrationFiles.ReadFile("migrations/007_suite_authorization.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, match := range regexp.MustCompile(`CREATE TRIGGER ([a-z_]+)`).FindAllStringSubmatch(string(body), -1) {
		if _, err = s.db.Exec(`DROP TRIGGER ` + match[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"suite_product_admission", "suite_user_authority", "suite_personal_keys"} {
		if _, err = s.db.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec(`DELETE FROM schema_migrations WHERE version=7`); err != nil {
		t.Fatal(err)
	}
}

type issuanceFixture struct {
	*trustFixture
	gateway             noxmcp.SuiteProvisioning
	record              noxmcp.SuiteRegistrationRecord
	member              User
	memberKey, adminKey string
}

func newIssuanceFixture(t *testing.T) *issuanceFixture {
	t.Helper()
	f := newTrustFixture(t)
	gateway := f.provision(t, "generic-delegator", "gateway", true)
	f.provision(t, "generic-destination", "product", true)
	decl := registryDeclaration(t, f, "generic-destination")
	var doc map[string]any
	if err := json.Unmarshal(decl, &doc); err != nil {
		t.Fatal(err)
	}
	doc["capabilities"] = map[string]any{"suiteMcp": map[string]string{"path": "/suite/mcp"}}
	record := registryPut(t, f, f.caller, "generic-destination", "", "register", mustRegistryJSON(t, doc), nil)
	member, err := f.store.UpsertAllowedUser(context.Background(), "inspect@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	memberKey, err := f.store.CurrentAPIKey(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminKey, err := f.store.CurrentAPIKey(context.Background(), f.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &issuanceFixture{f, gateway, record, member, memberKey, adminKey}
}
func (f *issuanceFixture) request(key string) noxmcp.SuiteAuthorizationRequest {
	return noxmcp.SuiteAuthorizationRequest{ContractVersion: "1.0", ApiKey: key, ProductID: f.record.ProductID, RegistrationRevision: f.record.Revision}
}
func (f *issuanceFixture) verification(t *testing.T, kind string) noxmcp.SuiteVerificationOptions {
	t.Helper()
	keys, err := f.store.SuiteKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	trusted := map[string]noxmcp.SuiteSigningKey{}
	for _, key := range keys.Keys {
		trusted[*key.Kid] = key
	}
	return noxmcp.SuiteVerificationOptions{Issuer: keys.Issuer, Audience: f.record.Connection.BaseUrl + "/suite/mcp", ProductID: f.record.ProductID, AssociationID: f.record.AssociationID, RegistrationRevision: f.record.Revision, GatewayServiceID: f.gateway.Service.ServiceID, ServiceCredentialRevision: f.gateway.Service.CredentialRevision, Keys: trusted, Kind: kind, Now: time.Now()}
}
func (f *issuanceFixture) issue(t *testing.T, key string) (SuiteResult, noxmcp.SuiteAuthorizationResponse) {
	t.Helper()
	result, err := f.store.SuiteIssueAuthorization(context.Background(), SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, f.request(key))
	return result, trustDecode[noxmcp.SuiteAuthorizationResponse](t, result, err)
}
func TestSuiteIssuedDelegationsRolesBindingExpiryAndRestart(t *testing.T) {
	f := newIssuanceFixture(t)
	ctx := context.Background()
	result, member := f.issue(t, f.memberKey)
	verified, err := noxmcp.VerifySuiteAuthorizationResponse(result.Body, f.verification(t, "user"))
	if err != nil {
		t.Fatal("published SDK rejected issued delegation", err)
	}
	if verified.Kind() != "user" || member.Context.UserID != f.member.ID || strings.Join(member.Context.Scopes, " ") != "nox:inspect" {
		t.Fatal("wrong user authority")
	}
	if member.Context.GatewayServiceID != f.gateway.Service.ServiceID || member.Context.AssociationID != f.record.AssociationID || member.Context.ServiceCredentialRevision != f.gateway.Service.CredentialRevision {
		t.Fatal("delegation is not bound to live association/caller")
	}
	claims := verified.Claims()
	if claims["exp"].(float64)-claims["iat"].(float64) != 300 {
		t.Fatal("wrong maximum lifetime")
	}
	_, admin := f.issue(t, f.adminKey)
	if strings.Join(admin.Context.Scopes, " ") != "nox:inspect nox:write nox:sensitive-read" {
		t.Fatal("administrator mapping differs from approved contract")
	}
	if strings.Contains(string(result.Body), f.memberKey) || strings.Contains(string(result.Body), f.gateway.ServiceCredential) {
		t.Fatal("issuance leaked an input credential")
	}
	opts := f.verification(t, "user")
	exp, _ := noxmcp.ParseSuiteTimestamp(member.ExpiresAt)
	opts.Now = exp.Add(-5*time.Second - time.Millisecond)
	if _, err = noxmcp.VerifySuiteToken(member.AccessToken, opts); err != nil {
		t.Fatal("token expired before shared safety margin", err)
	}
	opts.Now = exp.Add(-5 * time.Second)
	if _, err = noxmcp.VerifySuiteToken(member.AccessToken, opts); err == nil {
		t.Fatal("expiry safety margin was extended")
	}
	f.reopen(t)
	nextResult, next := f.issue(t, f.memberKey)
	if member.Context.KeyID != next.Context.KeyID || member.Context.AuthRevision != next.Context.AuthRevision {
		t.Fatal("normal restart reset authority metadata")
	}
	if member.AccessToken == next.AccessToken {
		t.Fatal("independent exchanges reused grant identifiers")
	}
	if _, err = noxmcp.VerifySuiteAuthorizationResponse(nextResult.Body, f.verification(t, "user")); err != nil {
		t.Fatal(err)
	}
	// Exchanges do not persist a token/idempotency result or make another service secret.
	var outcomes int
	if err = f.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suite_idempotency WHERE operation LIKE '%authorization%'`).Scan(&outcomes); err != nil || outcomes != 0 {
		t.Fatal("authorization exchange persisted a replayable token", err)
	}
	for _, query := range []string{`SELECT event_json FROM suite_trust_events`, `SELECT response_json FROM suite_idempotency`} {
		rows, err := f.store.db.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var raw string
			_ = rows.Scan(&raw)
			for _, secret := range []string{member.AccessToken, next.AccessToken, f.memberKey, f.gateway.ServiceCredential} {
				if strings.Contains(raw, secret) {
					t.Fatal("secret persisted in operational metadata")
				}
			}
		}
		rows.Close()
	}
}
func TestSuiteIssuedProbeCannotBecomeUserAuthority(t *testing.T) {
	f := newIssuanceFixture(t)
	request := noxmcp.SuiteProbeRequest{ContractVersion: "1.0", ProductID: f.record.ProductID, RegistrationRevision: f.record.Revision}
	result, err := f.store.SuiteIssueProbe(context.Background(), SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, request)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := noxmcp.VerifySuiteAuthorizationResponse(result.Body, f.verification(t, "probe"))
	if err != nil {
		t.Fatal("published SDK rejected actual probe", err)
	}
	if probe.Kind() != "probe" || probe.Claims()["sub"] != f.gateway.Service.ServiceID {
		t.Fatal("probe inherited a user")
	}
	for _, field := range []string{"act", "keyId", "authRevision", "userId", "scopes"} {
		if bytes.Contains(result.Body, []byte(`"`+field+`"`)) {
			t.Fatalf("probe inherited %s", field)
		}
	}
	for _, method := range []string{"initialize", "notifications/initialized", "ping", "server/discover"} {
		if err = noxmcp.RequireSuiteMethod(probe, method, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, method := range []string{"tools/list", "tools/call", "nox/tools/catalog", "resources/list", "resources/subscribe"} {
		if err = noxmcp.RequireSuiteMethod(probe, method, time.Now()); err == nil {
			t.Fatal("probe could access user/business methods", method)
		}
	}
	if _, err = noxmcp.VerifySuiteAuthorizationResponse(result.Body, f.verification(t, "user")); err == nil {
		t.Fatal("probe reinterpreted as user delegation")
	}
	user, _ := f.issue(t, f.memberKey)
	if _, err = noxmcp.VerifySuiteAuthorizationResponse(user.Body, f.verification(t, "probe")); err == nil {
		t.Fatal("user delegation reinterpreted as probe")
	}
}
func TestSuiteIssuanceRejectsCallerAndTargetSubstitution(t *testing.T) {
	f := newIssuanceFixture(t)
	ctx := context.Background()
	result, original := f.issue(t, f.memberKey)
	for _, change := range []func(*noxmcp.SuiteVerificationOptions){
		func(o *noxmcp.SuiteVerificationOptions) { o.Audience = "https://foreign.example.com/suite/mcp" },
		func(o *noxmcp.SuiteVerificationOptions) { o.ProductID = "other-product" },
		func(o *noxmcp.SuiteVerificationOptions) { o.AssociationID = "other-association" },
		func(o *noxmcp.SuiteVerificationOptions) { o.GatewayServiceID = "other-service" },
		func(o *noxmcp.SuiteVerificationOptions) { o.RegistrationRevision = "other-revision" },
		func(o *noxmcp.SuiteVerificationOptions) { o.ServiceCredentialRevision = "other-credential" },
		func(o *noxmcp.SuiteVerificationOptions) { o.Issuer = "other-issuer" },
	} {
		opts := f.verification(t, "user")
		change(&opts)
		if _, err := noxmcp.VerifySuiteAuthorizationResponse(result.Body, opts); err == nil {
			t.Fatal("wrong binding accepted")
		}
	}
	parts := strings.Split(original.AccessToken, ".")
	parts[2] = "AAAA"
	if _, err := noxmcp.VerifySuiteToken(strings.Join(parts, "."), f.verification(t, "user")); err == nil {
		t.Fatal("bad signature accepted")
	}
	req := f.request(f.memberKey)
	req.Extra = map[string]json.RawMessage{"userId": json.RawMessage(`"injected-user"`), "scope": json.RawMessage(`"nox:write"`), "expiresIn": json.RawMessage(`999999`)}
	issued, err := f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, req)
	injected := trustDecode[noxmcp.SuiteAuthorizationResponse](t, issued, err)
	if injected.Context.UserID != f.member.ID || len(injected.Context.Scopes) != 1 {
		t.Fatal("optional assertions granted authority")
	}
	for _, caller := range []SuiteCaller{f.caller, {UserID: f.member.ID}} {
		_, err = f.store.SuiteIssueAuthorization(ctx, caller, req)
		trustExpectedError(t, err, 403)
	}
	participant := f.provision(t, "non-gateway", "product", true)
	_, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: participant.ServiceCredential}, req)
	trustExpectedError(t, err, 403)
	req.ApiKey = "unknown-personal-key"
	_, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, req)
	trustExpectedError(t, err, 401)
	req = f.request(f.memberKey)
	req.RegistrationRevision = "retired-revision"
	_, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, req)
	trustExpectedError(t, err, 409)
}
func TestSuiteCurrentRoleKeyAndAccountInvalidations(t *testing.T) {
	f := newIssuanceFixture(t)
	ctx := context.Background()
	_, first := f.issue(t, f.memberKey)
	gatewayCaller := SuiteCaller{ServiceToken: f.gateway.ServiceCredential}
	snapshot := registrySnapshot(t, f.trustFixture, gatewayCaller)
	if err := f.store.SetUserRole(ctx, f.member.ID, "ADMIN"); err != nil {
		t.Fatal(err)
	}
	_, promoted := f.issue(t, f.memberKey)
	if len(promoted.Context.Scopes) != 3 || promoted.Context.AuthRevision == first.Context.AuthRevision {
		t.Fatal("role change did not affect current grant")
	}
	if _, err := f.store.UpsertAllowedUser(ctx, f.member.Email, "USER"); err != nil {
		t.Fatal(err)
	}
	_, demoted := f.issue(t, f.memberKey)
	if len(demoted.Context.Scopes) != 1 || demoted.Context.AuthRevision == promoted.Context.AuthRevision {
		t.Fatal("upsert bypassed authority invalidation")
	}
	newKey, err := f.store.RotateAPIKey(ctx, f.member.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteIssueAuthorization(ctx, gatewayCaller, f.request(f.memberKey))
	trustExpectedError(t, err, 401)
	_, rotated := f.issue(t, newKey)
	if rotated.Context.KeyID == demoted.Context.KeyID || rotated.Context.AuthRevision == demoted.Context.AuthRevision {
		t.Fatal("rotation retained old key authority")
	}
	if err = f.store.SetUserStatus(ctx, f.member.ID, "DISABLED"); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteIssueAuthorization(ctx, gatewayCaller, f.request(newKey))
	trustExpectedError(t, err, 401)
	if err = f.store.SetUserStatus(ctx, f.member.ID, "ACTIVE"); err != nil {
		t.Fatal(err)
	}
	_, reenabled := f.issue(t, newKey)
	if reenabled.Context.AuthRevision == rotated.Context.AuthRevision {
		t.Fatal("reenable reused retired authorization revision")
	}
	if err = f.store.DeleteUser(ctx, f.member.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteIssueAuthorization(ctx, gatewayCaller, f.request(newKey))
	trustExpectedError(t, err, 401)
	page := registryPage(t, f.trustFixture, gatewayCaller, snapshot.Cursor, 256)
	invalidations := 0
	keyInvalidated := false
	for _, event := range page.Events {
		if event.Type != "authorization.invalidated" {
			continue
		}
		invalidations++
		var user, key string
		_ = json.Unmarshal(event.Data["userId"], &user)
		_ = json.Unmarshal(event.Data["keyId"], &key)
		if user != f.member.ID {
			t.Fatal("invalidation targeted another user")
		}
		if key == demoted.Context.KeyID {
			keyInvalidated = true
		}
	}
	if invalidations < 6 || !keyInvalidated {
		t.Fatal("missing current authority invalidations", invalidations)
	}
}
func TestSuiteConcurrentIssuanceIsRequestLocal(t *testing.T) {
	f := newIssuanceFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	results := make(chan noxmcp.SuiteAuthorizationResponse, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(admin bool) {
			defer wg.Done()
			key := f.memberKey
			if admin {
				key = f.adminKey
			}
			result, err := f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, f.request(key))
			if err != nil {
				errors <- err
				return
			}
			var response noxmcp.SuiteAuthorizationResponse
			err = json.Unmarshal(result.Body, &response)
			if err == nil {
				if admin && response.Context.UserID != f.owner.ID || !admin && response.Context.UserID != f.member.ID {
					err = ErrForbidden
				}
			}
			if err != nil {
				errors <- err
				return
			}
			results <- response
		}(i%2 == 0)
	}
	wg.Wait()
	close(errors)
	close(results)
	for err := range errors {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for result := range results {
		if seen[result.AccessToken] {
			t.Fatal("concurrent contexts shared a token")
		}
		seen[result.AccessToken] = true
	}
	if len(seen) != 12 {
		t.Fatal("missing issued contexts")
	}
}

func TestSuiteIssuanceBoundsOverlapAndRejectsRevocation(t *testing.T) {
	f := newIssuanceFixture(t)
	ctx := context.Background()
	rotate, err := f.store.SuiteRotateCredential(ctx, f.caller, f.gateway.Service.ServiceID, f.gateway.Service.CredentialRevision, "rotate", "rotate")
	if err != nil {
		t.Fatal(err)
	}
	var rotation struct {
		ServiceCredential string                      `json:"serviceCredential"`
		AcknowledgeBy     string                      `json:"acknowledgeBy"`
		Service           noxmcp.SuiteServiceIdentity `json:"service"`
	}
	if err = json.Unmarshal(rotate.Body, &rotation); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(25 * time.Second)
	f.store.suiteClock = func() time.Time { return now }
	_, out := f.issue(t, f.memberKey)
	expires, _ := noxmcp.ParseSuiteTimestamp(out.ExpiresAt)
	deadline, _ := noxmcp.ParseSuiteTimestamp(rotation.AcknowledgeBy)
	if expires.After(deadline) || expires.After(now.Add(300*time.Second)) {
		t.Fatal("overlap credential obtained a longer grant")
	}
	_, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: rotation.ServiceCredential}, f.request(f.memberKey))
	trustExpectedError(t, err, 403)
	if _, err = f.store.SuiteAcknowledgeCredential(ctx, SuiteCaller{ServiceToken: rotation.ServiceCredential}, f.gateway.Service.ServiceID, rotation.Service.CredentialID, "ack-rotation", "ack-rotation"); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, f.request(f.memberKey))
	trustExpectedError(t, err, 401)
	f.gateway.ServiceCredential = rotation.ServiceCredential
	f.gateway.Service = rotation.Service
	if _, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, f.request(f.memberKey)); err != nil {
		t.Fatal("acknowledged credential cannot issue", err)
	}
	// Significant wall-clock rollback cannot create another five-minute window.
	now = now.Add(-10 * time.Second)
	_, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, f.request(f.memberKey))
	trustExpectedError(t, err, 503)
}
func TestSuiteIssuanceRejectsRemovedUnclassifiedAndReassociatedTargets(t *testing.T) {
	f := newIssuanceFixture(t)
	ctx := context.Background()
	caller := SuiteCaller{ServiceToken: f.gateway.ServiceCredential}
	f.provision(t, "no-suite-capability", "product", true)
	record := registryPut(t, f.trustFixture, f.caller, "no-suite-capability", "", "no-suite-registration", registryDeclaration(t, f.trustFixture, "no-suite-capability"), nil)
	request := f.request(f.memberKey)
	request.ProductID = record.ProductID
	request.RegistrationRevision = record.Revision
	_, err := f.store.SuiteIssueAuthorization(ctx, caller, request)
	trustExpectedError(t, err, 422)
	var head string
	if err = f.store.db.QueryRow(`SELECT revision FROM suite_trust_heads WHERE product_id=?`, f.record.ProductID).Scan(&head); err != nil {
		t.Fatal(err)
	}
	repair := trustRequest(f.record.ProductID, "product", f.record.Connection.BaseUrl)
	paired, err := f.store.SuiteCreatePairing(ctx, f.caller, repair, head, "replace-association", "replace-association")
	intent := trustDecode[noxmcp.SuitePairingIntent](t, paired, err)
	proof, decl := trustProof(t, intent)
	paired, err = f.store.SuiteCompletePairing(ctx, intent.PairingID, proof, decl, "https://backend.example.com", "replace-complete", "replace-complete")
	provision := trustDecode[noxmcp.SuiteProvisioning](t, paired, err)
	if _, err = f.store.SuiteAcknowledgePairing(ctx, SuiteCaller{ServiceToken: provision.ServiceCredential}, intent.PairingID, "replace-ack", "replace-ack"); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteIssueAuthorization(ctx, caller, f.request(f.memberKey))
	trustExpectedError(t, err, 409)
	if _, err = f.store.SuiteRemoveProduct(ctx, f.caller, f.record.ProductID, f.record.Revision, "remove", "remove"); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteIssueAuthorization(ctx, caller, f.request(f.memberKey))
	trustExpectedError(t, err, 404)
	probe := noxmcp.SuiteProbeRequest{ContractVersion: "1.0", ProductID: f.record.ProductID, RegistrationRevision: f.record.Revision}
	_, err = f.store.SuiteIssueProbe(ctx, caller, probe)
	trustExpectedError(t, err, 404)
}
func TestSuiteSigningRotationAndExplicitRevocationAffectIssuance(t *testing.T) {
	f := newIssuanceFixture(t)
	ctx := context.Background()
	first, _ := f.issue(t, f.memberKey)
	keys, err := f.store.SuiteKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.SuitePrepareSigningKey(ctx, f.caller, keys.Revision, "prepare", "prepare")
	var prepared struct{ KeyID, Revision string }
	if err != nil || json.Unmarshal(result.Body, &prepared) != nil {
		t.Fatal(err)
	}
	result, err = f.store.SuiteActivateSigningKey(ctx, f.caller, prepared.KeyID, prepared.Revision, "activate", "activate")
	if err != nil {
		t.Fatal(err)
	}
	current, _ := f.issue(t, f.memberKey)
	if _, err = noxmcp.VerifySuiteAuthorizationResponse(first.Body, f.verification(t, "user")); err != nil {
		t.Fatal("rotation removed a still-valid prior public key", err)
	}
	if _, err = noxmcp.VerifySuiteAuthorizationResponse(current.Body, f.verification(t, "user")); err != nil {
		t.Fatal("new signer not verifiable", err)
	}
	keys, err = f.store.SuiteKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteRevokeSigningKey(ctx, f.caller, prepared.KeyID, keys.Revision, "revoke", "revoke")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, f.request(f.memberKey))
	trustExpectedError(t, err, 503)
	f.reopen(t)
	_, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, f.request(f.memberKey))
	trustExpectedError(t, err, 503)
	if _, err = noxmcp.VerifySuiteAuthorizationResponse(current.Body, f.verification(t, "user")); err == nil {
		t.Fatal("explicitly revoked signing key still trusted")
	}
}
func TestSuiteNativeAuthorityMutationAndEventRollbackTogether(t *testing.T) {
	f := newIssuanceFixture(t)
	ctx := context.Background()
	_, before := f.issue(t, f.memberKey)
	if _, err := f.store.db.Exec(`CREATE TRIGGER reject_authorization_event BEFORE INSERT ON suite_trust_events WHEN NEW.type='authorization.invalidated' BEGIN SELECT RAISE(ABORT,'injected invalidation failure'); END`); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []func() error{
		func() error { return f.store.SetUserRole(ctx, f.member.ID, "ADMIN") },
		func() error { return f.store.SetUserStatus(ctx, f.member.ID, "DISABLED") },
		func() error { _, err := f.store.RotateAPIKey(ctx, f.member.ID); return err },
		func() error { return f.store.DeleteUser(ctx, f.member.ID) },
		func() error {
			return f.store.UpdateManagedUser(ctx, f.member.ID, "role", "ADMIN", UserManagementPolicy{ActorID: f.owner.ID, CanManageAdmins: true})
		},
		func() error {
			return f.store.DeleteManagedUser(ctx, f.member.ID, UserManagementPolicy{ActorID: f.owner.ID, CanManageAdmins: true})
		},
	} {
		if err := mutation(); err == nil {
			t.Fatal("authority mutation committed without its event")
		}
		user, err := f.store.UserByID(ctx, f.member.ID)
		if err != nil || user.Role != "USER" || user.Status != "ACTIVE" {
			t.Fatal("failed mutation changed account", err)
		}
		_, after := f.issue(t, f.memberKey)
		if after.Context.AuthRevision != before.Context.AuthRevision || after.Context.KeyID != before.Context.KeyID {
			t.Fatal("failed invalidation changed authority metadata")
		}
	}
	if _, err := f.store.db.Exec(`DROP TRIGGER reject_authorization_event`); err != nil {
		t.Fatal(err)
	}
	policy := UserManagementPolicy{ActorID: f.owner.ID, CanManageAdmins: true}
	if err := f.store.UpdateManagedUser(ctx, f.member.ID, "role", "ADMIN", policy); err != nil {
		t.Fatal(err)
	}
	_, after := f.issue(t, f.memberKey)
	if after.Context.AuthRevision == before.Context.AuthRevision || len(after.Context.Scopes) != 3 {
		t.Fatal("managed administration did not invalidate")
	}
	rows, err := f.store.db.Query(`SELECT event_id,event_json FROM suite_trust_events WHERE type='authorization.invalidated'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		_ = rows.Scan(&id, &raw)
		event, err := noxmcp.LoadSuiteEvent([]byte(raw))
		if err != nil || event.EventID != id {
			t.Fatal("invalid durable native event envelope", err)
		}
	}
}
func TestSuiteMigrationSixToSevenPreservesExistingAccountsAndTrust(t *testing.T) {
	f := newIssuanceFixture(t)
	ctx := context.Background()
	snapshot := registrySnapshot(t, f.trustFixture, f.caller)
	keys, err := f.store.SuiteKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session, err := f.store.CreateWebSession(ctx, f.member.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rewindSuiteAuthorizationSchema(t, f.store)
	f.reopen(t)
	key, err := f.store.CurrentAPIKey(ctx, f.member.ID)
	if err != nil || key != f.memberKey {
		t.Fatal("migration rotated existing personal key", err)
	}
	if _, ok, err := f.store.UserBySessionToken(ctx, session); err != nil || !ok {
		t.Fatal("migration invalidated native session", err)
	}
	next, err := f.store.SuiteKeys(ctx)
	if err != nil || !suiteSame(keys, next) {
		t.Fatal("migration changed issuer/signing keys", err)
	}
	current := registrySnapshot(t, f.trustFixture, f.caller)
	if current.RegistryRevision != snapshot.RegistryRevision || !suiteSame(current.Products, snapshot.Products) {
		t.Fatal("migration changed registry")
	}
	out, _ := f.issue(t, f.memberKey)
	if _, err = noxmcp.VerifySuiteAuthorizationResponse(out.Body, f.verification(t, "user")); err != nil {
		t.Fatal("migrated native key cannot authorize", err)
	}
}
func TestSuiteMissingInvalidationTriggerFailsSchemaGuard(t *testing.T) {
	f := newTrustFixture(t)
	if _, err := f.store.db.Exec(`DROP TRIGGER suite_user_authority_changed`); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(context.Background(), f.dir)
	if err == nil {
		opened.Close()
		t.Fatal("missing invalidation trigger was silently accepted")
	}
	if !strings.Contains(err.Error(), "unsupported database schema") {
		t.Fatal(err)
	}
}
func TestSuiteKnownIncompatibleUpdateBlocksNewGrantsUntilVerifiedRepair(t *testing.T) {
	f := newIssuanceFixture(t)
	ctx := context.Background()
	request := noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: f.record.Connection.BaseUrl}
	prepared, err := f.store.SuitePrepareRegistration(ctx, f.caller, f.record.ProductID, request, f.record.Revision, "try-update", "try-update")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.SuiteBlockIncompatibleRegistration(ctx, f.caller, f.record.ProductID, request, f.record.Revision, prepared); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	_, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, f.request(f.memberKey))
	trustExpectedError(t, err, 422)
	snapshot := registrySnapshot(t, f.trustFixture, f.caller)
	if len(snapshot.Products) != 1 || snapshot.Products[0].Revision != f.record.Revision {
		t.Fatal("incompatibility deleted/changed membership")
	}
	decl := mustRegistryJSON(t, f.record.Declaration)
	result, err := f.store.SuiteCommitRegistration(ctx, f.caller, f.record.ProductID, request, f.record.Revision, "repair", "repair", prepared, decl)
	if err != nil {
		t.Fatal(err)
	}
	repaired := trustDecode[noxmcp.SuiteRegistrationRecord](t, result, nil)
	if repaired.Revision != f.record.Revision {
		t.Fatal("identical verified recovery changed material revision")
	}
	if _, err = f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: f.gateway.ServiceCredential}, f.request(f.memberKey)); err != nil {
		t.Fatal("verified repair did not restore eligibility", err)
	}
}
