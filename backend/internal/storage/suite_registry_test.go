package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

func registryDeclaration(t *testing.T, f *trustFixture, id string) json.RawMessage {
	t.Helper()
	var raw string
	if err := f.store.db.QueryRow(`SELECT record_json FROM suite_services WHERE product_id=? AND state='active'`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var svc suiteService
	if err := json.Unmarshal([]byte(raw), &svc); err != nil {
		t.Fatal(err)
	}
	return svc.Declaration
}
func registryPut(t *testing.T, f *trustFixture, caller SuiteCaller, id, expected, key string, decl []byte, managed *noxmcp.SuiteRegistrationRecordManagedBy) noxmcp.SuiteRegistrationRecord {
	t.Helper()
	req := noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: "https://" + id + ".example.com", ManagedBy: managed}
	prepare, err := f.store.SuitePrepareRegistration(context.Background(), caller, id, req, expected, key, key)
	if err != nil {
		t.Fatal(err)
	}
	if prepare.Replay != nil {
		return trustDecode[noxmcp.SuiteRegistrationRecord](t, *prepare.Replay, nil)
	}
	result, err := f.store.SuiteCommitRegistration(context.Background(), caller, id, req, expected, key, key, prepare, decl)
	return trustDecode[noxmcp.SuiteRegistrationRecord](t, result, err)
}
func registrySnapshot(t *testing.T, f *trustFixture, c SuiteCaller) noxmcp.SuiteRegistrySnapshot {
	t.Helper()
	result, err := f.store.SuiteReadRegistry(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := noxmcp.LoadSuiteRegistrySnapshot(result.Body)
	if err != nil {
		t.Fatalf("invalid SDK snapshot: %v", err)
	}
	return v
}
func registryPage(t *testing.T, f *trustFixture, c SuiteCaller, cursor string, limit int) noxmcp.SuiteEventPage {
	t.Helper()
	result, err := f.store.SuiteReadEvents(context.Background(), c, cursor, 0, limit)
	if err != nil {
		t.Fatal(err)
	}
	v, err := noxmcp.LoadSuiteEventPage(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestSuiteRegistryVerifiedMaterialChangesReplayAndRestart(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	req := noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: "https://future-catalog.example.com"}
	_, err := f.store.SuitePrepareRegistration(ctx, f.caller, "future-catalog", req, "", "unpaired", "unpaired")
	trustExpectedError(t, err, 409)
	f.provision(t, "future-catalog", "product", true)
	decl := registryDeclaration(t, f, "future-catalog")
	original := registryPut(t, f, f.caller, "future-catalog", "", "enroll", decl, nil)
	first := registrySnapshot(t, f, f.caller)
	same := registryPut(t, f, f.caller, "future-catalog", original.Revision, "same", decl, nil)
	if same.Revision != original.Revision || registrySnapshot(t, f, f.caller).RegistryRevision != first.RegistryRevision {
		t.Fatal("identical configuration changed revision")
	}
	f.reopen(t)
	prepared, err := f.store.SuitePrepareRegistration(ctx, f.caller, "future-catalog", req, "", "enroll", "enroll")
	if err != nil || prepared.Replay == nil {
		t.Fatalf("lost-response replay unavailable: %v", err)
	}
	replay := trustDecode[noxmcp.SuiteRegistrationRecord](t, *prepared.Replay, nil)
	if replay.Revision != original.Revision {
		t.Fatal("replay changed registration")
	}
	_, err = f.store.SuitePrepareRegistration(ctx, f.caller, "future-catalog", req, "", "enroll", "different")
	trustExpectedError(t, err, 409)
	_, err = f.store.SuitePrepareRegistration(ctx, f.caller, "future-catalog", req, "", "another", "another")
	trustExpectedError(t, err, 412)
	changed := bytes.Replace(decl, []byte(`"1.0"`), []byte(`"2.0"`), 1) // this changes contractVersion; unsupported declarations cannot mutate state
	preparation, err := f.store.SuitePrepareRegistration(ctx, f.caller, "future-catalog", req, original.Revision, "invalid", "invalid")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteCommitRegistration(ctx, f.caller, "future-catalog", req, original.Revision, "invalid", "invalid", preparation, changed)
	trustExpectedError(t, err, 400)
	changed = bytes.Replace(decl, []byte(`"productVersion":"1.0"`), []byte(`"productVersion":"2.0"`), 1)
	updated := registryPut(t, f, f.caller, "future-catalog", original.Revision, "update", changed, nil)
	if updated.Revision == original.Revision || updated.Declaration.Extra["extension"] == nil {
		t.Fatal("material change or optional metadata lost")
	}
	if registrySnapshot(t, f, f.caller).RegistryRevision == first.RegistryRevision {
		t.Fatal("registry revision did not advance")
	}
}
func TestSuiteRegistryConcurrentWritesAndAtomicRollback(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	f.provision(t, "race-catalog", "product", true)
	req := noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: "https://race-catalog.example.com"}
	decl := registryDeclaration(t, f, "race-catalog")
	prep, err := f.store.SuitePrepareRegistration(ctx, f.caller, "race-catalog", req, "", "one", "one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.db.Exec(`CREATE TRIGGER reject_product_event BEFORE INSERT ON suite_trust_events WHEN NEW.type='product.upserted' BEGIN SELECT RAISE(ABORT,'injected registry failure'); END`); err != nil {
		t.Fatal(err)
	}
	before := registrySnapshot(t, f, f.caller)
	_, err = f.store.SuiteCommitRegistration(ctx, f.caller, "race-catalog", req, "", "one", "one", prep, decl)
	if err == nil {
		t.Fatal("injected failure committed")
	}
	after := registrySnapshot(t, f, f.caller)
	if len(after.Products) != 0 || after.RegistryRevision != before.RegistryRevision {
		t.Fatal("failed event did not roll back membership")
	}
	var outcomes int
	_ = f.store.db.QueryRow(`SELECT COUNT(*) FROM suite_idempotency WHERE operation='products.put:race-catalog'`).Scan(&outcomes)
	if outcomes != 0 {
		t.Fatal("failed mutation remembered")
	}
	if _, err = f.store.db.Exec(`DROP TRIGGER reject_product_event`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	revisions := make(chan string, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := f.store.SuiteCommitRegistration(ctx, f.caller, "race-catalog", req, "", "one", "one", prep, decl)
			if err == nil {
				var rec noxmcp.SuiteRegistrationRecord
				err = json.Unmarshal(result.Body, &rec)
				revisions <- rec.Revision
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	close(revisions)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	revision := ""
	for got := range revisions {
		if revision != "" && revision != got {
			t.Fatal("concurrent replay created separate results")
		}
		revision = got
	}
	var events int
	_ = f.store.db.QueryRow(`SELECT COUNT(*) FROM suite_trust_events WHERE type='product.upserted'`).Scan(&events)
	if events != 1 {
		t.Fatalf("duplicate registry events: %d", events)
	}
}
func TestSuiteRegistryRemovalTombstoneAndDelayedRetry(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	p := f.provision(t, "remove-catalog", "product", true)
	rec := registryPut(t, f, f.caller, p.Service.ProductID, "", "register", registryDeclaration(t, f, p.Service.ProductID), nil)
	result, err := f.store.SuiteRemoveProduct(ctx, f.caller, rec.ProductID, rec.Revision, "remove", "remove")
	if err != nil || result.Status != 204 || len(result.Body) != 0 {
		t.Fatalf("remove failed: %v", err)
	}
	_, err = f.store.SuiteAuthenticateService(ctx, p.ServiceCredential)
	trustExpectedError(t, err, 401)
	f.reopen(t)
	// Re-enrollment requires a new proven association, never revives removed trust.
	state, err := f.store.SuiteReadServices(ctx, f.caller, p.Service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Revision string `json:"revision"`
	}
	if err = json.Unmarshal(state.Body, &view); err != nil {
		t.Fatal(err)
	}
	head := view.Revision
	request := trustRequest(rec.ProductID, "product", "https://remove-catalog.example.com")
	out, err := f.store.SuiteCreatePairing(ctx, f.caller, request, head, "repair", "repair")
	intent := trustDecode[noxmcp.SuitePairingIntent](t, out, err)
	proof, decl := trustProof(t, intent)
	out, err = f.store.SuiteCompletePairing(ctx, intent.PairingID, proof, decl, "https://backend.example.com", "repair-complete", "repair-complete")
	provision := trustDecode[noxmcp.SuiteProvisioning](t, out, err)
	_, err = f.store.SuiteAcknowledgePairing(ctx, SuiteCaller{ServiceToken: provision.ServiceCredential}, intent.PairingID, "repair-ack", "repair-ack")
	if err != nil {
		t.Fatal(err)
	}
	current := registryPut(t, f, f.caller, rec.ProductID, "", "new-enroll", decl, nil)
	if current.AssociationID == rec.AssociationID || current.Revision == rec.Revision {
		t.Fatal("removed identifiers reused")
	}
	result, err = f.store.SuiteRemoveProduct(ctx, f.caller, rec.ProductID, rec.Revision, "remove", "remove")
	if err != nil || result.Status != 204 {
		t.Fatalf("lost removal replay failed: %v", err)
	}
	if len(registrySnapshot(t, f, f.caller).Products) != 1 {
		t.Fatal("old replay removed replacement")
	}
	_, err = f.store.SuiteRemoveProduct(ctx, f.caller, rec.ProductID, rec.Revision, "delayed-new-key", "delayed-new-key")
	trustExpectedError(t, err, 412)
}
func TestSuiteRegistryRoleFilteringAndInstallerScope(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	gateway := f.provision(t, "generic-router", "gateway", true)
	installer := f.provision(t, "generic-installer", "installer", true)
	managed := &noxmcp.SuiteRegistrationRecordManagedBy{ProductID: installer.Service.ProductID, DeploymentID: "opaque-project"}
	grant := &SuiteDeploymentGrant{ProductID: "managed-catalog", BaseURL: "https://managed-catalog.example.com"}
	_, err := f.store.SuiteSetDeployment(ctx, f.caller, installer.Service.ServiceID, managed.DeploymentID, "", "grant", "grant", grant)
	if err != nil {
		t.Fatal(err)
	}
	req := trustRequest(grant.ProductID, "product", grant.BaseURL)
	req.ManagedBy = managed
	out, err := f.store.SuiteCreatePairing(ctx, SuiteCaller{ServiceToken: installer.ServiceCredential}, req, "", "managed-pair", "managed-pair")
	intent := trustDecode[noxmcp.SuitePairingIntent](t, out, err)
	proof, decl := trustProof(t, intent)
	out, err = f.store.SuiteCompletePairing(ctx, intent.PairingID, proof, decl, "https://backend.example.com", "managed-complete", "managed-complete")
	p := trustDecode[noxmcp.SuiteProvisioning](t, out, err)
	_, err = f.store.SuiteAcknowledgePairing(ctx, SuiteCaller{ServiceToken: p.ServiceCredential}, intent.PairingID, "managed-ack", "managed-ack")
	if err != nil {
		t.Fatal(err)
	}
	caller := SuiteCaller{ServiceToken: installer.ServiceCredential}
	rec := registryPut(t, f, caller, grant.ProductID, "", "enroll-managed", decl, managed)
	member, err := f.store.UpsertAllowedUser(ctx, "member@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.SuiteReadRegistry(ctx, SuiteCaller{UserID: member.ID}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"baseUrl", "associationId", "deploymentId", "opaque-project", "extension", "cursor"} {
		if strings.Contains(string(result.Body), leak) {
			t.Fatalf("member registry leaked %s", leak)
		}
	}
	if !strings.Contains(string(result.Body), grant.ProductID) {
		t.Fatal("member has no summary")
	}
	if len(registrySnapshot(t, f, SuiteCaller{ServiceToken: gateway.ServiceCredential}).Products) != 1 {
		t.Fatal("gateway cannot synchronize")
	}
	request := noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: grant.BaseURL, ManagedBy: managed}
	_, err = f.store.SuitePrepareRegistration(ctx, SuiteCaller{ServiceToken: gateway.ServiceCredential}, rec.ProductID, request, rec.Revision, "no-gateway-write", "no-gateway-write")
	trustExpectedError(t, err, 403)
	_, err = f.store.SuitePrepareRegistration(ctx, caller, rec.ProductID, noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: "https://unapproved.example.com", ManagedBy: managed}, rec.Revision, "no-target", "no-target")
	trustExpectedError(t, err, 403)
	result, err = f.store.SuiteReadRegistry(ctx, SuiteCaller{ServiceToken: p.ServiceCredential}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result.Body, []byte(rec.ProductID)) {
		t.Fatal("participant cannot reconcile own membership")
	}
}
func TestSuiteEventsScopePagingRetentionAndRestart(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	gateway := f.provision(t, "event-router", "gateway", true)
	participant := f.provision(t, "own-catalog", "product", true)
	ownCaller := SuiteCaller{ServiceToken: participant.ServiceCredential}
	gatewayCaller := SuiteCaller{ServiceToken: gateway.ServiceCredential}
	own := registrySnapshot(t, f, ownCaller)
	snapshot := registrySnapshot(t, f, gatewayCaller)
	f.provision(t, "other-catalog", "product", true)
	registryPut(t, f, f.caller, "other-catalog", "", "other", registryDeclaration(t, f, "other-catalog"), nil)
	hidden := registryPage(t, f, ownCaller, own.Cursor, 1)
	if len(hidden.Events) != 0 || hidden.Cursor == own.Cursor {
		t.Fatal("filtered scan did not advance privately")
	}
	_, err := f.store.SuiteReadEvents(ctx, ownCaller, snapshot.Cursor, 0, 10)
	trustExpectedError(t, err, 410)
	registryPut(t, f, f.caller, "own-catalog", "", "own", registryDeclaration(t, f, "own-catalog"), nil)
	page := registryPage(t, f, gatewayCaller, snapshot.Cursor, 1)
	if !page.HasMore || len(page.Events) != 1 {
		t.Fatal("bounded draining lost events")
	}
	reduced, err := noxmcp.ReduceSuiteRegistry(snapshot, mustRegistryJSON(t, page), snapshot.Cursor)
	if err != nil {
		t.Fatal(err)
	}
	next := registryPage(t, f, gatewayCaller, page.Cursor, 256)
	reduced, err = noxmcp.ReduceSuiteRegistry(reduced, mustRegistryJSON(t, next), page.Cursor)
	if err != nil || len(reduced.Products) != 2 {
		t.Fatalf("SDK could not reconstruct registry: %v", err)
	}
	f.reopen(t)
	empty := registryPage(t, f, gatewayCaller, next.Cursor, 256)
	if len(empty.Events) != 0 || empty.Cursor != next.Cursor {
		t.Fatal("restart invalidated normal cursor")
	}
	if _, err = f.store.db.Exec(`UPDATE suite_trust_events SET occurred_at=?`, time.Now().Add(-8*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteReadEvents(ctx, gatewayCaller, snapshot.Cursor, 0, 10)
	trustExpectedError(t, err, 410)
	fresh := registrySnapshot(t, f, gatewayCaller)
	_ = registryPage(t, f, gatewayCaller, fresh.Cursor, 10)
	err = f.store.ResetSuiteEventCursors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteReadEvents(ctx, gatewayCaller, fresh.Cursor, 0, 10)
	trustExpectedError(t, err, 410)
}
func mustRegistryJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestSuiteEventLongPollWakeCancellationAndCurrentCredential(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	gateway := f.provision(t, "poll-router", "gateway", true)
	caller := SuiteCaller{ServiceToken: gateway.ServiceCredential}
	snapshot := registrySnapshot(t, f, caller)
	done := make(chan error, 1)
	go func() {
		_, err := f.store.SuiteReadEvents(ctx, caller, snapshot.Cursor, 25*time.Second, 10)
		done <- err
	}()
	f.provision(t, "wake-catalog", "product", true)
	registryPut(t, f, f.caller, "wake-catalog", "", "wake", registryDeclaration(t, f, "wake-catalog"), nil)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("commit did not wake subscriber")
	}
	snapshot = registrySnapshot(t, f, caller)
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err := f.store.SuiteReadEvents(cancelCtx, caller, snapshot.Cursor, 25*time.Second, 10)
	if err == nil {
		t.Fatal("cancelled poll remained active")
	}
	go func() {
		_, err := f.store.SuiteReadEvents(ctx, caller, snapshot.Cursor, 25*time.Second, 10)
		done <- err
	}()
	result, err := f.store.SuiteReadServices(ctx, f.caller, gateway.Service.ServiceID)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Revision string `json:"revision"`
	}
	_ = json.Unmarshal(result.Body, &view)
	_, err = f.store.SuiteRevokeService(ctx, f.caller, gateway.Service.ServiceID, view.Revision, "revoke-poll", "revoke-poll")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		trustExpectedError(t, err, 401)
	case <-time.After(3 * time.Second):
		t.Fatal("revoked poll remained admitted")
	}
}
func TestSuiteEventCountRetentionIsBounded(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	gateway := f.provision(t, "retention-router", "gateway", true)
	caller := SuiteCaller{ServiceToken: gateway.ServiceCredential}
	snapshot := registrySnapshot(t, f, caller)
	// SQLite generates the realistic retention boundary without 100,001 slow RPCs.
	_, err := f.store.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<100001) INSERT INTO suite_trust_events(event_id,type,event_json,occurred_at) SELECT 'bulk-'||x,'signing-keys.changed','{}',? FROM n`, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.MaintainSuiteTrust(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = f.store.db.QueryRow(`SELECT COUNT(*) FROM suite_trust_events`).Scan(&count)
	if count > suiteEventMaximum {
		t.Fatal("unbounded event retention", count)
	}
	_, err = f.store.SuiteReadEvents(ctx, caller, snapshot.Cursor, 0, 1)
	trustExpectedError(t, err, 410)
}
func TestSuiteRegistryPreflightCannotCommitAfterTrustRevocation(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	p := f.provision(t, "recheck-catalog", "product", true)
	req := noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: "https://recheck-catalog.example.com"}
	prep, err := f.store.SuitePrepareRegistration(ctx, f.caller, p.Service.ProductID, req, "", "pending", "pending")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteRevokeService(ctx, f.caller, p.Service.ServiceID, prep.TrustRevision, "revoke", "revoke")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteCommitRegistration(ctx, f.caller, p.Service.ProductID, req, "", "pending", "pending", prep, []byte(fmt.Sprintf(`{"contractVersion":"1.0","productId":%q,"productVersion":"1","displayName":"Fixture","capabilities":{}}`, p.Service.ProductID)))
	trustExpectedError(t, err, 409)
}

func TestSuiteMigrationFiveToSixPreservesInstalledTrust(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	p := f.provision(t, "upgrade-catalog", "product", true)
	before, err := f.store.SuiteKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct an actual point-4 schema in disposable data, with its issuer,
	// service credential and encrypted signing material already populated.
	for _, table := range []string{"suite_registry_state", "suite_products", "suite_product_tombstones", "suite_observation_epochs", "suite_observations"} {
		if _, err = f.store.db.Exec(`DROP TABLE ` + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.store.db.Exec(`DROP INDEX suite_trust_events_time`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.db.Exec(`DELETE FROM schema_migrations WHERE version=6`); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	after, err := f.store.SuiteKeys(ctx)
	if err != nil || !suiteSame(before, after) {
		t.Fatal("migration replaced authority", err)
	}
	if _, err = f.store.SuiteAuthenticateService(ctx, p.ServiceCredential); err != nil {
		t.Fatal("migration broke installed credentials", err)
	}
	snapshot := registrySnapshot(t, f, f.caller)
	if len(snapshot.Products) != 0 {
		t.Fatal("migration invented membership")
	}
}
