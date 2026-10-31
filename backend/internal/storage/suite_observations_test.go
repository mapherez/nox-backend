package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

func observationEpoch(t *testing.T, f *trustFixture, caller SuiteCaller, rec noxmcp.SuiteRegistrationRecord, producer, source, key string) noxmcp.SuiteObservationEpoch {
	t.Helper()
	req := noxmcp.SuiteObservationEpochRequest{ContractVersion: "1.0", ProductID: rec.ProductID, RegistrationRevision: rec.Revision, AssociationID: rec.AssociationID, ProducerID: producer, Source: source}
	result, err := f.store.SuiteCreateObservationEpoch(context.Background(), caller, req, key, key)
	return trustDecode[noxmcp.SuiteObservationEpoch](t, result, err)
}
func observationSubmit(t *testing.T, f *trustFixture, caller SuiteCaller, obs noxmcp.SuiteObservation, key string) (noxmcp.SuiteAcceptedObservation, error) {
	t.Helper()
	result, err := f.store.SuiteSubmitObservation(context.Background(), caller, mustRegistryJSON(t, obs), key, key)
	if err != nil {
		return noxmcp.SuiteAcceptedObservation{}, err
	}
	var out noxmcp.SuiteAcceptedObservation
	err = json.Unmarshal(result.Body, &out)
	return out, err
}
func TestSuiteObservationOrderingFreshnessAndEpochRecovery(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	gateway := f.provision(t, "observation-router", "gateway", true)
	f.provision(t, "observed-catalog", "product", true)
	rec := registryPut(t, f, f.caller, "observed-catalog", "", "register", registryDeclaration(t, f, "observed-catalog"), nil)
	caller := SuiteCaller{ServiceToken: gateway.ServiceCredential}
	epoch := observationEpoch(t, f, caller, rec, gateway.Service.ServiceID, "gateway", "epoch")
	snapshot := registrySnapshot(t, f, caller)
	obs := noxmcp.SuiteObservation{ContractVersion: "1.0", ProductID: rec.ProductID, RegistrationRevision: rec.Revision, AssociationID: rec.AssociationID, Source: "gateway", ProducerID: gateway.Service.ServiceID, ProducerEpoch: epoch.ProducerEpoch, Sequence: "9007199254740993", ObservedAt: suiteTime(time.Now()), State: "ready"}
	accepted, err := observationSubmit(t, f, caller, obs, "first")
	if err != nil || !accepted.Accepted {
		t.Fatal("first observation rejected", err)
	}
	f.reopen(t)
	again, err := observationSubmit(t, f, caller, obs, "duplicate")
	if err != nil || again.Accepted {
		t.Fatal("duplicate not ignored", err)
	}
	obs.Sequence = "9007199254740992"
	again, err = observationSubmit(t, f, caller, obs, "older")
	if err != nil || again.Accepted {
		t.Fatal("older report not ignored", err)
	}
	obs.Sequence = "9007199254740993"
	obs.State = "unreachable"
	_, err = observationSubmit(t, f, caller, obs, "conflicting")
	trustExpectedError(t, err, 409)
	obs.Sequence = "18446744073709551615"
	obs.ObservedAt = suiteTime(time.Now().Add(-2 * time.Minute))
	accepted, err = observationSubmit(t, f, caller, obs, "max-sequence")
	if err != nil || !accepted.Accepted {
		t.Fatal("uint64 sequence rejected", err)
	}
	state, err := noxmcp.SuiteObservationState(accepted.Envelope, time.Now())
	if err != nil || state != "unknown" {
		t.Fatal("old report became fresh")
	}
	after := registrySnapshot(t, f, caller)
	if after.RegistryRevision != snapshot.RegistryRevision || len(after.Products) != 1 {
		t.Fatal("heartbeat changed membership or registration revision")
	}
	obs.Sequence = "18446744073709551616"
	_, err = observationSubmit(t, f, caller, obs, "overflow")
	trustExpectedError(t, err, 400)
	obs.Sequence = "1"
	obs.ObservedAt = suiteTime(time.Now().Add(time.Minute))
	_, err = observationSubmit(t, f, caller, obs, "future")
	trustExpectedError(t, err, 400)
	obs.ObservedAt = suiteTime(time.Now())
	obs.State = "ready"
	newEpoch := observationEpoch(t, f, caller, rec, gateway.Service.ServiceID, "gateway", "new-epoch")
	_, err = observationSubmit(t, f, caller, obs, "retired-epoch")
	trustExpectedError(t, err, 409)
	obs.ProducerEpoch = newEpoch.ProducerEpoch
	accepted, err = observationSubmit(t, f, caller, obs, "new-producer")
	if err != nil || !accepted.Accepted {
		t.Fatal("sequence reset could not recover", err)
	}
	if len(registrySnapshot(t, f, caller).Observations) != 1 {
		t.Fatal("epoch retained duplicate old observations")
	}
	page := registryPage(t, f, caller, snapshot.Cursor, 256)
	events := 0
	for _, e := range page.Events {
		if e.Type == "observation.updated" {
			events++
		}
	}
	if events != 3 {
		t.Fatalf("duplicate/older observations generated events: %d", events)
	}
	obs.AssociationID = "retired-association"
	_, err = observationSubmit(t, f, caller, obs, "wrong-association")
	trustExpectedError(t, err, 409)
	result, err := f.store.SuiteReadRegistry(ctx, f.caller, rec.ProductID)
	if err != nil || result.ETag != rec.Revision {
		t.Fatal("live observations affected stable registration validator", err)
	}
}
func TestSuiteObservationRollbackAndReporterPermissions(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	gateway := f.provision(t, "atomic-router", "gateway", true)
	product := f.provision(t, "atomic-catalog", "product", true)
	rec := registryPut(t, f, f.caller, product.Service.ProductID, "", "register", registryDeclaration(t, f, product.Service.ProductID), nil)
	caller := SuiteCaller{ServiceToken: gateway.ServiceCredential}
	epoch := observationEpoch(t, f, caller, rec, gateway.Service.ServiceID, "gateway", "epoch")
	obs := noxmcp.SuiteObservation{ContractVersion: "1.0", ProductID: rec.ProductID, RegistrationRevision: rec.Revision, AssociationID: rec.AssociationID, Source: "gateway", ProducerID: gateway.Service.ServiceID, ProducerEpoch: epoch.ProducerEpoch, Sequence: "1", ObservedAt: suiteTime(time.Now()), State: "ready"}
	_, err := f.store.db.Exec(`CREATE TRIGGER reject_observation BEFORE INSERT ON suite_trust_events WHEN NEW.type='observation.updated' BEGIN SELECT RAISE(ABORT,'injected observation failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = observationSubmit(t, f, caller, obs, "rollback")
	if err == nil {
		t.Fatal("failed event committed observation")
	}
	if len(registrySnapshot(t, f, f.caller).Observations) != 0 {
		t.Fatal("failed event left observation persisted")
	}
	_, err = f.store.db.Exec(`DROP TRIGGER reject_observation`)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := observationSubmit(t, f, caller, obs, "rollback")
	if err != nil || !accepted.Accepted {
		t.Fatal("same request did not recover after rollback", err)
	}
	_, err = observationSubmit(t, f, SuiteCaller{ServiceToken: product.ServiceCredential}, obs, "impersonation")
	trustExpectedError(t, err, 403)
	_, err = observationSubmit(t, f, f.caller, obs, "admin-not-producer")
	trustExpectedError(t, err, 403)
	obs.Source = "yard"
	_, err = observationSubmit(t, f, caller, obs, "gateway-lifecycle")
	trustExpectedError(t, err, 400) // ready is not a Yard lifecycle state
	obs.State = "running"
	obs.ManagedBy = &noxmcp.SuiteRegistrationRecordManagedBy{ProductID: "arbitrary-installer", DeploymentID: "project"}
	_, err = observationSubmit(t, f, caller, obs, "wrong-source-grant")
	trustExpectedError(t, err, 403)
	// Revoke the source during a later retry: historical outcomes grant no authority.
	var trustRevision string
	_ = f.store.db.QueryRow(`SELECT revision FROM suite_trust_heads WHERE product_id=?`, gateway.Service.ProductID).Scan(&trustRevision)
	_, err = f.store.SuiteRevokeService(ctx, f.caller, gateway.Service.ServiceID, trustRevision, "revoke", "revoke")
	if err != nil {
		t.Fatal(err)
	}
	obs.Source = "gateway"
	obs.ManagedBy = nil
	obs.State = "ready"
	_, err = observationSubmit(t, f, caller, obs, "rollback")
	trustExpectedError(t, err, 401)
}
func TestSuiteObservationConfiguredFreshnessAndSeparateSources(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	gateway := f.provision(t, "separate-router", "gateway", true)
	installer := f.provision(t, "separate-installer", "installer", true)
	managed := &noxmcp.SuiteRegistrationRecordManagedBy{ProductID: installer.Service.ProductID, DeploymentID: "project"}
	_, err := f.store.SuiteSetDeployment(ctx, f.caller, installer.Service.ServiceID, "project", "", "grant", "grant", &SuiteDeploymentGrant{ProductID: "separate-catalog", BaseURL: "https://separate-catalog.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	request := trustRequest("separate-catalog", "product", "https://separate-catalog.example.com")
	request.ManagedBy = managed
	out, err := f.store.SuiteCreatePairing(ctx, f.caller, request, "", "intent", "intent")
	intent := trustDecode[noxmcp.SuitePairingIntent](t, out, err)
	proof, decl := trustProof(t, intent)
	out, err = f.store.SuiteCompletePairing(ctx, intent.PairingID, proof, decl, "https://backend.example.com", "complete", "complete")
	p := trustDecode[noxmcp.SuiteProvisioning](t, out, err)
	_, err = f.store.SuiteAcknowledgePairing(ctx, SuiteCaller{ServiceToken: p.ServiceCredential}, intent.PairingID, "ack", "ack")
	if err != nil {
		t.Fatal(err)
	}
	rec := registryPut(t, f, f.caller, "separate-catalog", "", "register", decl, managed)
	f.store.ConfigureSuiteObservationTiming(10*time.Second, 30*time.Second)
	gc, ic := SuiteCaller{ServiceToken: gateway.ServiceCredential}, SuiteCaller{ServiceToken: installer.ServiceCredential}
	ge := observationEpoch(t, f, gc, rec, gateway.Service.ServiceID, "gateway", "gateway-epoch")
	ie := observationEpoch(t, f, ic, rec, installer.Service.ServiceID, "yard", "yard-epoch")
	obs := noxmcp.SuiteObservation{ContractVersion: "1.0", ProductID: rec.ProductID, RegistrationRevision: rec.Revision, AssociationID: rec.AssociationID, Source: "gateway", ProducerID: gateway.Service.ServiceID, ProducerEpoch: ge.ProducerEpoch, Sequence: "1", ObservedAt: suiteTime(time.Now().Add(-31 * time.Second)), State: "unreachable"}
	_, err = observationSubmit(t, f, gc, obs, "gateway-report")
	if err != nil {
		t.Fatal(err)
	}
	obs.Source = "yard"
	obs.ProducerID = installer.Service.ServiceID
	obs.ProducerEpoch = ie.ProducerEpoch
	obs.State = "running"
	obs.ManagedBy = &noxmcp.SuiteRegistrationRecordManagedBy{ProductID: "arbitrary-installer", DeploymentID: "project"}
	obs.ObservedAt = suiteTime(time.Now())
	obs.ManagedBy = managed
	accepted, err := observationSubmit(t, f, ic, obs, "yard-report")
	if err != nil {
		t.Fatal(err)
	}
	observed, _ := noxmcp.ParseSuiteTimestamp(obs.ObservedAt)
	until, _ := noxmcp.ParseSuiteTimestamp(accepted.Envelope.FreshUntil)
	if until.Sub(observed) != 30*time.Second {
		t.Fatal("configured freshness ignored")
	}
	member, err := f.store.UpsertAllowedUser(ctx, "availability@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.SuiteReadRegistry(ctx, SuiteCaller{UserID: member.ID}, rec.ProductID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Body), `"gateway":"unknown"`) || !strings.Contains(string(result.Body), `"yard":"running"`) {
		t.Fatalf("lifecycle overrode MCP readiness: %s", result.Body)
	}
	obs.Sequence = "2"
	obs.ManagedBy = &noxmcp.SuiteRegistrationRecordManagedBy{ProductID: installer.Service.ProductID, DeploymentID: "foreign-project"}
	_, err = observationSubmit(t, f, ic, obs, "foreign-project")
	trustExpectedError(t, err, 403)
}
