package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSuiteAdministrationSnapshotAuthorityRedactionAndRestart(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	gateway := f.provision(t, "suite-admin-source", "gateway", true)
	installer := f.provision(t, "suite-admin-installer", "installer", true)
	f.provision(t, "suite-admin-product", "product", true)
	record := registryPut(t, f, f.caller, "suite-admin-product", "", "register-admin-view", registryDeclaration(t, f, "suite-admin-product"), nil)
	intent := f.intent(t, trustRequest("suite-admin-pending", "product", "https://pending.example.com"), "admin-view-pending")
	rotation, err := f.store.SuiteRotateCredential(ctx, f.caller, gateway.Service.ServiceID, gateway.Service.CredentialRevision, "admin-view-rotation", "admin-view-rotation")
	var response struct {
		ServiceCredential string `json:"serviceCredential"`
	}
	if err != nil || json.Unmarshal(rotation.Body, &response) != nil {
		t.Fatal(err)
	}
	_, err = f.store.SuiteSetDeployment(ctx, f.caller, installer.Service.ServiceID, "deployment-1", "", "deployment-key", "deployment-digest", &SuiteDeploymentGrant{ProductID: "future-deployment", BaseURL: "https://future.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.store.SuiteAdministration(ctx, f.caller)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Services) != 3 || len(snapshot.Products) != 1 || snapshot.Products[0].Revision != record.Revision || snapshot.Issuer == "" || len(snapshot.SigningKeys) != 1 {
		t.Fatalf("incomplete snapshot: %+v", snapshot)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{gateway.ServiceCredential, installer.ServiceCredential, response.ServiceCredential, intent.Secret, "secretHandle", "SecretHandle", "verifier", "IntentHandle", "Nonce"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("administrative view disclosed private state")
		}
	}
	foundOverlap, foundReplacement, foundDeployment := false, false, false
	for _, service := range snapshot.Services {
		for _, credential := range service.Credentials {
			foundOverlap = foundOverlap || credential.State == "overlap"
			foundReplacement = foundReplacement || (credential.State == "provisioned" && credential.Current)
		}
		foundDeployment = foundDeployment || len(service.Deployments) > 0
	}
	if !foundOverlap || !foundReplacement || !foundDeployment {
		t.Fatal("lost rotation or deployment state")
	}
	member, err := f.store.UpsertAllowedUser(ctx, "snapshot-member@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	for _, caller := range []SuiteCaller{{}, {UserID: member.ID}, {ServiceToken: gateway.ServiceCredential}} {
		_, err := f.store.SuiteAdministration(ctx, caller)
		if err == nil {
			t.Fatal("non-admin read full native administration")
		}
	}
	f.reopen(t)
	after, err := f.store.SuiteAdministration(ctx, f.caller)
	if err != nil {
		t.Fatal(err)
	}
	after.ObservedAt = snapshot.ObservedAt
	later, _ := json.Marshal(after)
	if string(later) != string(raw) {
		t.Fatal("restart changed administrative state")
	}
	// The read projection never introduces a registry member from a pairing alone.
	for _, product := range after.Products {
		if product.ProductID == intent.ProductID {
			t.Fatal("pending pairing became membership")
		}
	}
}
