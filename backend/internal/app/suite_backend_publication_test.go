package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

func publicationState(t *testing.T, f *suiteHTTPFixture) storage.SuiteAdministration {
	t.Helper()
	status, _, raw := f.call(t, "GET", "/api/admin/suite", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	var response struct {
		State storage.SuiteAdministration `json:"state"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	return response.State
}
func publicationRecord(t *testing.T, f *suiteHTTPFixture) noxmcp.SuiteRegistrationRecord {
	t.Helper()
	for _, record := range publicationState(t, f).Products {
		if record.ProductID == serviceName {
			return record
		}
	}
	t.Fatal("Backend was not published through its normal administrator adapter")
	return noxmcp.SuiteRegistrationRecord{}
}
func publishFromDashboard(t *testing.T, f *suiteHTTPFixture) {
	t.Helper()
	status, _, raw := f.call(t, "POST", "/api/admin/suite/backend/publish", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	if strings.Contains(string(raw), "noxsvc_") || strings.Contains(string(raw), f.adminKey) || strings.Contains(string(raw), "secret") {
		t.Fatal("publication response exposed credentials")
	}
}

func TestBackendPublicationFreshStoreAndActualMCP(t *testing.T) {
	f := suiteHTTPServer(t, true)
	if state := publicationState(t, f); len(state.Products) != 0 || len(state.Services) != 0 {
		t.Fatal("fixture pre-created product trust")
	}
	publishFromDashboard(t, f)
	first := publicationRecord(t, f)
	if first.Connection.BaseUrl != suiteBackendOrigin || first.ManagedBy != nil || first.Declaration.Capabilities.SuiteMcp == nil {
		t.Fatal("incorrect own-product registration")
	}
	before := publicationState(t, f)
	publishFromDashboard(t, f)
	again := publicationRecord(t, f)
	after := publicationState(t, f)
	if first.AssociationID != again.AssociationID || first.Revision != again.Revision || before.RegistryRevision != after.RegistryRevision || len(after.Pairings) != 1 || len(after.Services) != 1 {
		t.Fatal("repeated dashboard load duplicated or changed publication")
	}
	gateway, _ := f.paired(t, "publication-gateway", "gateway")
	actual := &backendSuiteFixture{suiteHTTPFixture: f, gateway: gateway, record: first}
	token := actual.token(t, f.adminKey, false)
	definitions := backendCLITestDefinitions(t, actual, token)
	if len(definitions) != 10 {
		t.Fatal("published Backend did not expose its ten definitions")
	}
	client := actual.connect(t, token)
	listed, err := client.ListTools(context.Background(), nil)
	if err != nil || len(listed.Tools) != 10 {
		t.Fatal("published Backend MCP catalog failed", err)
	}
	for _, tool := range listed.Tools {
		if _, leaked := tool.Meta["cli"]; leaked {
			t.Fatal("CLI paths leaked to ordinary MCP")
		}
	}
	callBackendMCP(t, client, "backend_health", nil, "")
	callBackendMCP(t, client, "backend_info", nil, "")
	callBackendMCP(t, client, "backend_whoami", nil, "")
	created := callBackendMCP(t, client, "backend_vault_create", map[string]any{"name": "disposable publication acceptance"}, "")
	id := created["vaultId"].(string)
	callBackendMCP(t, client, "backend_file_list", map[string]any{"vaultId": id}, "")
	for _, name := range []string{"backend_vault_delete", "backend_vault_restore", "backend_vault_delete", "backend_vault_purge"} {
		callBackendMCP(t, client, name, map[string]any{"vaultId": id}, "")
	}
}

func TestBackendPublicationRequiresExistingAdministratorAndCSRF(t *testing.T) {
	f := suiteHTTPServer(t, true)
	reader, err := f.store.UpsertAllowedUser(context.Background(), "publication-reader@example.test", "USER")
	if err != nil {
		t.Fatal(err)
	}
	readerKey, err := f.store.CurrentAPIKey(context.Background(), reader.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", readerKey, "noxsvc_invalid"} {
		status, _, _ := f.call(t, "POST", "/api/admin/suite/backend/publish", nil, key, nil)
		if status != 401 && status != 403 {
			t.Fatal("publication accepted non-administrator", status)
		}
	}
	admin, authErr := f.s.apiUser(context.Background(), f.adminKey)
	if authErr != nil {
		t.Fatal(authErr)
	}
	session, err := fixtureWebSession(t, context.Background(), f.store, admin.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cookie := sessionCookieName + "=" + session
	for _, headers := range []map[string]string{
		{"Cookie": cookie},
		{"Cookie": cookie, "X-Nox-Request": "1", "Origin": "https://unrelated.example.test"},
	} {
		status, _, raw := f.call(t, "POST", "/api/admin/suite/backend/publish", nil, "", headers)
		suiteHTTPStatus(t, status, 403, raw)
	}
	if len(publicationState(t, f).Services) != 0 {
		t.Fatal("rejected request changed trust")
	}
	for _, target := range []string{"/api/admin/suite/backend/publish?productId=another", "/api/admin/suite/backend/publish"} {
		var input any
		if !strings.Contains(target, "?") {
			input = map[string]string{"productId": "another"}
		}
		status, _, raw := f.call(t, "POST", target, input, f.adminKey, nil)
		suiteHTTPStatus(t, status, 400, raw)
	}
	status, _, raw := f.call(t, "GET", "/api/admin/suite/backend/publish", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 405, raw)
	status, _, raw = f.call(t, "POST", "/api/admin/suite/backend/publish", nil, "", map[string]string{"Cookie": cookie, "X-Nox-Request": "1"})
	suiteHTTPStatus(t, status, 200, raw)
	publicationRecord(t, f)
}

func TestBackendPublicationRestartUpdateAndIndependentSwitches(t *testing.T) {
	f := suiteHTTPServer(t, true)
	publishFromDashboard(t, f)
	first := publicationRecord(t, f)
	f.s.cfg.Version = "publication-next"
	restarted := NewServer(f.s.cfg, f.store)
	restarted.reconcileBackendPublication(context.Background())
	updated := publicationRecord(t, f)
	if updated.AssociationID != first.AssociationID || updated.Revision == first.Revision || updated.Declaration.ProductVersion != "publication-next" {
		t.Fatal("restart did not reconcile the actual declaration while retaining identity")
	}
	f.s.cfg.SuiteMCPEnabled = false
	disabled := NewServer(f.s.cfg, f.store)
	disabled.reconcileBackendPublication(context.Background())
	off := publicationRecord(t, f)
	if off.AssociationID != first.AssociationID || off.Declaration.Capabilities.SuiteMcp != nil {
		t.Fatal("disabling Suite did not withdraw only its capability")
	}
	// Direct MCP remains governed by its existing independent switch.
	direct := connectBackendMCP(t, f.backend, f.adminKey)
	callBackendMCP(t, direct, "backend_info", nil, "")
	f.s.cfg.SuiteMCPEnabled = true
	enabled := NewServer(f.s.cfg, f.store)
	enabled.reconcileBackendPublication(context.Background())
	on := publicationRecord(t, f)
	if on.AssociationID != first.AssociationID || on.Declaration.Capabilities.SuiteMcp == nil {
		t.Fatal("reenabling Suite replaced its association or failed to advertise")
	}
}

func TestBackendPublicationRevocationAndAuthDenialNeverResurrect(t *testing.T) {
	t.Run("removed registration", func(t *testing.T) {
		f := suiteHTTPServer(t, true)
		publishFromDashboard(t, f)
		record := publicationRecord(t, f)
		admin, failure := f.s.apiUser(context.Background(), f.adminKey)
		if failure != nil {
			t.Fatal(failure)
		}
		_, err := f.store.SuiteRemoveProduct(context.Background(), storage.SuiteCaller{UserID: admin.ID}, serviceName, record.Revision, "remove-own", "remove-own")
		if err != nil {
			t.Fatal(err)
		}
		restarted := NewServer(f.s.cfg, f.store)
		restarted.reconcileBackendPublication(context.Background())
		status, _, raw := f.call(t, "POST", "/api/admin/suite/backend/publish", nil, f.adminKey, nil)
		suiteHTTPStatus(t, status, 409, raw)
		if len(publicationState(t, f).Products) != 0 {
			t.Fatal("removed association was resurrected")
		}
	})
	t.Run("Auth admission withdrawn", func(t *testing.T) {
		f := suiteHTTPServer(t, true)
		publishFromDashboard(t, f)
		first := publicationRecord(t, f)
		admin, failure := f.s.apiUser(context.Background(), f.adminKey)
		if failure != nil {
			t.Fatal(failure)
		}
		f.s.cfg.Version = "must-not-publish"
		authFixture(t, f.store).provider.Deny()
		restarted := NewServer(f.s.cfg, f.store)
		restarted.reconcileBackendPublication(context.Background())
		state, err := f.store.SuiteAdministration(context.Background(), storage.SuiteCaller{UserID: admin.ID})
		if err != nil {
			t.Fatal(err)
		}
		if state.Products[0].Revision != first.Revision {
			t.Fatal("unadmitted account refreshed publication")
		}
	})
}

func TestBackendPublicationConcurrentRequestsRemainOneAssociation(t *testing.T) {
	f := suiteHTTPServer(t, true)
	admin, failure := f.s.apiUser(context.Background(), f.adminKey)
	if failure != nil {
		t.Fatal(failure)
	}
	var group sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		group.Go(func() { failures <- f.s.publishBackend(context.Background(), storage.SuiteCaller{UserID: admin.ID}) })
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	state := publicationState(t, f)
	if len(state.Pairings) != 1 || len(state.Services) != 1 || len(state.Products) != 1 {
		t.Fatal("concurrent publication duplicated persistent state")
	}
}

func TestBackendPublicationDisabledFreshStoreDoesNotEnroll(t *testing.T) {
	f := suiteHTTPServer(t)
	publishFromDashboard(t, f)
	state := publicationState(t, f)
	if len(state.Products) != 0 || len(state.Services) != 0 || len(state.Pairings) != 0 {
		t.Fatal("disabled Suite enrolled a product")
	}
}

func TestBackendPublicationRecoversInterruptedPairing(t *testing.T) {
	for _, stage := range []string{"pending", "provisioned", "acknowledged"} {
		t.Run(stage, func(t *testing.T) {
			f := suiteHTTPServer(t, true)
			admin, failure := f.s.apiUser(context.Background(), f.adminKey)
			if failure != nil {
				t.Fatal(failure)
			}
			caller := storage.SuiteCaller{UserID: admin.ID}
			req := noxmcp.SuitePairingRequest{ContractVersion: "1.0", ProductID: serviceName, Purpose: "product", BaseUrl: suiteBackendOrigin, Extra: map[string]json.RawMessage{"nox/backend-publication": json.RawMessage("true")}}
			result, err := f.store.SuiteCreatePairing(context.Background(), caller, req, "", "interrupted-create", "interrupted-create")
			if err != nil {
				t.Fatal(err)
			}
			intent, err := noxmcp.LoadSuitePairingIntent(result.Body)
			if err != nil {
				t.Fatal(err)
			}
			if stage != "pending" {
				declaration, err := f.s.fetchSuiteDeclaration(context.Background(), suiteBackendOrigin, "")
				if err != nil {
					t.Fatal(err)
				}
				value, _ := noxmcp.ParseSuiteJSON(declaration)
				fingerprint, _ := noxmcp.Fingerprint(value)
				secret, _ := base64.RawURLEncoding.DecodeString(intent.Secret)
				binding := noxmcp.SuitePairingBinding{ContractVersion: "1.0", PairingID: intent.PairingID, Nonce: intent.Nonce, ProductID: serviceName, BaseUrl: suiteBackendOrigin, DeclarationFingerprint: fingerprint}
				proof, err := noxmcp.CreateSuitePairingProof(secret, binding)
				if err != nil {
					t.Fatal(err)
				}
				result, err = f.store.SuiteCompletePairing(context.Background(), intent.PairingID, noxmcp.SuitePairingProof{ContractVersion: "1.0", Binding: binding, Proof: proof}, declaration, suiteBackendOrigin, "interrupted-complete", "interrupted-complete")
				if err != nil {
					t.Fatal(err)
				}
				if stage == "acknowledged" {
					provision, err := noxmcp.LoadSuiteProvisioning(result.Body)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = f.store.SuiteAcknowledgePairing(context.Background(), storage.SuiteCaller{ServiceToken: provision.ServiceCredential}, intent.PairingID, "interrupted-ack", "interrupted-ack"); err != nil {
						t.Fatal(err)
					}
					restarted := NewServer(f.s.cfg, f.store)
					restarted.reconcileBackendPublication(context.Background())
					publicationRecord(t, f)
					return
				}
			}
			publishFromDashboard(t, f)
			publicationRecord(t, f)
			state := publicationState(t, f)
			for _, p := range state.Pairings {
				if p.State == "pending" || p.State == "provisioned" {
					t.Fatal("interrupted attempt was left live")
				}
			}
			if len(state.Services) != 1 {
				t.Fatal("recovery left multiple active services")
			}
		})
	}
}

func TestBackendPublicationDoesNotCancelIndependentPairing(t *testing.T) {
	f := suiteHTTPServer(t, true)
	admin, failure := f.s.apiUser(context.Background(), f.adminKey)
	if failure != nil {
		t.Fatal(failure)
	}
	caller := storage.SuiteCaller{UserID: admin.ID}
	result, err := f.store.SuiteCreatePairing(context.Background(), caller,
		noxmcp.SuitePairingRequest{ContractVersion: "1.0", ProductID: serviceName, Purpose: "product", BaseUrl: suiteBackendOrigin},
		"", "independent-create", "independent-create")
	if err != nil {
		t.Fatal(err)
	}
	intent, err := noxmcp.LoadSuitePairingIntent(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw := f.call(t, "POST", "/api/admin/suite/backend/publish", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 409, raw)
	result, err = f.store.SuiteReadPairing(context.Background(), caller, intent.PairingID)
	if err != nil {
		t.Fatal(err)
	}
	var pairing struct {
		State string `json:"state"`
	}
	if err = json.Unmarshal(result.Body, &pairing); err != nil || pairing.State != "pending" {
		t.Fatal("publication canceled an independent pairing", err)
	}
	if len(publicationState(t, f).Products) != 0 {
		t.Fatal("conflicting publication was committed")
	}
}
