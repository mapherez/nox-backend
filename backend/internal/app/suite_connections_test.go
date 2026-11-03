package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

type connectionParticipant struct {
	server      *httptest.Server
	declaration json.RawMessage
	mode        atomic.Int32
	deliveries  atomic.Int32
	mu          sync.Mutex
	intents     []noxmcp.SuitePairingIntent
	credential  string
}

func startConnectionParticipant(t *testing.T, f *suiteHTTPFixture, id string) *connectionParticipant {
	t.Helper()
	p := &connectionParticipant{declaration: json.RawMessage(`{"contractVersion":"1.0","productId":"` + id + `","productVersion":"test","displayName":"Local participant","capabilities":{}}`)}
	p.mode.Store(1)
	p.server = suiteTestUnixServer(t, f.s.cfg.SuiteSocketDir, id, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("personal credentials forwarded to participant")
		}
		if r.Method == "GET" && r.URL.Path == "/nox/integration" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(p.declaration)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/nox/integration/pairing" {
			http.NotFound(w, r)
			return
		}
		var input struct {
			ContractVersion string                    `json:"contractVersion"`
			BackendOrigin   string                    `json:"backendOrigin"`
			Intent          noxmcp.SuitePairingIntent `json:"intent"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if input.ContractVersion != "1.0" || input.BackendOrigin != suiteBackendOrigin || input.Intent.BaseUrl != "http://"+id || r.Header.Get("Idempotency-Key") == "" {
			t.Error("invalid automatic pairing envelope")
			w.WriteHeader(400)
			return
		}
		if p.mode.Load() == 4 {
			http.NotFound(w, r)
			return
		}
		p.deliveries.Add(1)
		p.mu.Lock()
		p.intents = append(p.intents, input.Intent)
		p.mu.Unlock()
		if p.mode.Load() == 0 {
			w.WriteHeader(202)
			return
		}
		if p.mode.Load() == 3 {
			w.WriteHeader(503)
			return
		}
		p.completeAndAck(t, f, input.Intent)
		if p.mode.Load() == 2 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		w.WriteHeader(204)
	}))
	return p
}
func (p *connectionParticipant) completeAndAck(t *testing.T, f *suiteHTTPFixture, intent noxmcp.SuitePairingIntent) {
	t.Helper()
	proof := suiteHTTPProof(t, intent, p.declaration, "")
	status, _, raw := f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", proof, "", map[string]string{"Idempotency-Key": intent.PairingID + ":complete"})
	if status != 200 {
		t.Errorf("completion failed (%d): %s", status, raw)
		return
	}
	provision, err := noxmcp.LoadSuiteProvisioning(raw)
	if err != nil {
		t.Error(err)
		return
	}
	p.mu.Lock()
	p.credential = provision.ServiceCredential
	p.mu.Unlock()
	// Participant persistence precedes ACK; it is not simulated by the Backend.
	path := filepath.Join(f.dir, "participant-credential")
	if err = os.WriteFile(path, []byte(provision.ServiceCredential), 0600); err != nil {
		t.Error(err)
		return
	}
	status, _, raw = f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/ack", nil, provision.ServiceCredential, map[string]string{"Idempotency-Key": intent.PairingID + ":ack"})
	if status != 200 {
		t.Errorf("acknowledgement failed (%d): %s", status, raw)
	}
}
func prepareConnection(t *testing.T, f *suiteHTTPFixture, service string) connectionSetupView {
	t.Helper()
	status, _, raw := f.call(t, "PUT", "/api/admin/connections/"+service, map[string]string{"origin": "http://dashboard.invalid:8081"}, f.adminKey, map[string]string{"If-None-Match": "*"})
	suiteHTTPStatus(t, status, 200, raw)
	var saved connectionView
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "POST", "/api/admin/connections/"+service+"/verify", map[string]string{"revision": saved.Revision}, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	var a connectionSetupView
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if a.State != "verified" || a.AttemptID == "" {
		t.Fatal("verification did not prepare confirmation")
	}
	return a
}
func connectAttempt(t *testing.T, f *suiteHTTPFixture, service, id string) (int, []byte) {
	t.Helper()
	status, _, raw := f.call(t, "POST", "/api/admin/connections/"+service+"/connect", map[string]string{"attemptId": id}, f.adminKey, nil)
	return status, raw
}
func TestFoundationAutomaticLocalPairingRegistrationAndReplay(t *testing.T) {
	f := suiteHTTPServer(t)
	p := startConnectionParticipant(t, f, "nox-yard")
	a := prepareConnection(t, f, "yard")
	status, raw := connectAttempt(t, f, "yard", a.AttemptID)
	suiteHTTPStatus(t, status, 200, raw)
	var done connectionSetupView
	_ = json.Unmarshal(raw, &done)
	if done.State != "completed" || p.deliveries.Load() != 1 {
		t.Fatal("automatic association incomplete", done.State)
	}
	state, err := f.store.SuiteAdministration(context.Background(), storage.SuiteCaller{UserID: mustConnectionAdmin(t, f).ID})
	if err != nil || len(state.Products) != 1 || len(state.Services) != 1 || state.Services[0].State != "active" {
		t.Fatal("registration does not match acknowledged service", err)
	}
	if state.Products[0].Connection.BaseUrl != "http://nox-yard" {
		t.Fatal("native web URL became service identity")
	}
	for _, leak := range []string{p.credential, "secret", "nonce", "noxsvc_", "Lease", "DeclarationFingerprint"} {
		if strings.Contains(string(raw), leak) {
			t.Fatal("setup view leaked private protocol state", leak)
		}
	}
	status, raw = connectAttempt(t, f, "yard", a.AttemptID)
	suiteHTTPStatus(t, status, 200, raw)
	if p.deliveries.Load() != 1 {
		t.Fatal("completed replay redelivered setup secrets")
	}
	status, _, raw = f.call(t, "GET", "/api/admin/connections", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	if !strings.Contains(string(raw), `"status":"registered"`) {
		t.Fatal("dashboard did not report actual registration")
	}
}
func mustConnectionAdmin(t *testing.T, f *suiteHTTPFixture) storage.User {
	t.Helper()
	user, err := f.s.apiUser(context.Background(), f.adminKey)
	if err != nil {
		t.Fatal(err)
	}
	return user
}
func TestFoundationLostDeliveryRetriesSameIntentWithoutDuplicateAssociation(t *testing.T) {
	f := suiteHTTPServer(t)
	p := startConnectionParticipant(t, f, "nox-gateway")
	p.mode.Store(3)
	a := prepareConnection(t, f, "gateway")
	status, raw := connectAttempt(t, f, "gateway", a.AttemptID)
	suiteHTTPStatus(t, status, 503, raw)
	stored, err := f.store.SuiteConnectionAttempt(context.Background(), "gateway")
	if err != nil || stored.PairingID == "" || stored.State != "error" {
		t.Fatal("lost delivery not recoverable", err)
	}
	p.mode.Store(2) // Save+ACK succeeds; delivery response itself is lost.
	status, raw = connectAttempt(t, f, "gateway", a.AttemptID)
	suiteHTTPStatus(t, status, 200, raw)
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.intents) != 2 || p.intents[0].PairingID != p.intents[1].PairingID || p.intents[0].Secret != p.intents[1].Secret {
		t.Fatal("retry changed the intent")
	}
	var done connectionSetupView
	_ = json.Unmarshal(raw, &done)
	if done.State != "completed" {
		t.Fatal("lost receipt hid committed acknowledgement")
	}
}
func TestFoundationPendingSetupSurvivesRestartAndReconcilesAcknowledgement(t *testing.T) {
	f := suiteHTTPServer(t)
	p := startConnectionParticipant(t, f, "nox-yard")
	p.mode.Store(0)
	a := prepareConnection(t, f, "yard")
	status, raw := connectAttempt(t, f, "yard", a.AttemptID)
	suiteHTTPStatus(t, status, 200, raw)
	var pending connectionSetupView
	_ = json.Unmarshal(raw, &pending)
	if pending.State != "waiting" {
		t.Fatal("receipt falsely reported integration")
	}
	cfg := f.s.cfg
	f.backend.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.store = store
	f.s = newAuthTestServer(t, cfg, store)
	f.backend = suiteTestBackend(t, f.s)
	f.client = f.backend.Client()
	p.mu.Lock()
	intent := p.intents[0]
	p.mu.Unlock()
	p.completeAndAck(t, f, intent)
	f.s.reconcileConnections(context.Background())
	result, err := f.store.SuiteConnectionAttempt(context.Background(), "yard")
	if err != nil || result.State != "completed" || p.deliveries.Load() != 1 {
		t.Fatal("restart reconciliation repeated delivery or failed registration", err, result.State)
	}
}
func TestFoundationCancelPreventsLateProofAndAllowsConfigurationChange(t *testing.T) {
	f := suiteHTTPServer(t)
	p := startConnectionParticipant(t, f, "nox-yard")
	p.mode.Store(0)
	a := prepareConnection(t, f, "yard")
	status, raw := connectAttempt(t, f, "yard", a.AttemptID)
	suiteHTTPStatus(t, status, 200, raw)
	items, err := f.store.FoundationConnections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.SaveFoundationConnection(context.Background(), "yard", "http://changed.invalid", items[0].Revision)
	if err == nil {
		t.Fatal("pending setup address changed without cancellation")
	}
	status, _, raw = f.call(t, "POST", "/api/admin/connections/yard/cancel", map[string]string{"attemptId": a.AttemptID}, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	p.mu.Lock()
	intent := p.intents[0]
	p.mu.Unlock()
	proof := suiteHTTPProof(t, intent, p.declaration, "")
	status, _, raw = f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", proof, "", nil)
	suiteHTTPStatus(t, status, 409, raw)
	if _, err = f.store.SaveFoundationConnection(context.Background(), "yard", "http://changed.invalid", items[0].Revision); err != nil {
		t.Fatal("cancelled setup still blocked address edit", err)
	}
}
func TestFoundationRejectsUnadaptedParticipantAndWrongIdentity(t *testing.T) {
	f := suiteHTTPServer(t)
	p := startConnectionParticipant(t, f, "nox-yard")
	p.mode.Store(4) // Declaration exists; automatic receiver is absent.
	a := prepareConnection(t, f, "yard")
	status, raw := connectAttempt(t, f, "yard", a.AttemptID)
	suiteHTTPStatus(t, status, 422, raw)
	state, err := f.store.SuiteAdministration(context.Background(), storage.SuiteCaller{UserID: mustConnectionAdmin(t, f).ID})
	if err != nil || len(state.Products) != 0 || len(state.Services) != 0 {
		t.Fatal("unadapted service became integrated", err)
	}
}
func TestNativeHumanAdministrationWorksOverHTTPButServicesCannotUseTCP(t *testing.T) {
	f := suiteHTTPServer(t, true)
	for _, tlsEnabled := range []bool{false, true} {
		var server *httptest.Server
		if tlsEnabled {
			server = httptest.NewTLSServer(f.s.Routes())
		} else {
			server = httptest.NewServer(f.s.Routes())
		}
		func() {
			defer server.Close()
			for _, path := range []string{"/api/nox/keys", "/mcp/suite"} {
				req, _ := http.NewRequest("GET", server.URL+path, nil)
				req.Header.Set("Authorization", "Bearer "+f.adminKey)
				req.Header.Set("X-Forwarded-Proto", "https")
				req.Header.Set("X-Nox-Suite-Transport", "https")
				res, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if res.StatusCode != 403 {
					t.Fatal("TCP service transport admitted", res.StatusCode)
				}
			}
			for _, path := range []string{"/api/admin/suite", "/api/admin/suite/operations/keys", "/v1/auth/check"} {
				req, _ := http.NewRequest("GET", server.URL+path, nil)
				req.Header.Set("Authorization", "Bearer "+f.adminKey)
				res, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if res.StatusCode != 200 {
					t.Fatal("native API blocked", path, res.StatusCode)
				}
			}
		}()
	}
}
func TestFoundationSetupChecksCurrentAdminConfigurationAndLease(t *testing.T) {
	f := suiteHTTPServer(t)
	_ = startConnectionParticipant(t, f, "nox-yard")
	a := prepareConnection(t, f, "yard")
	status, _, raw := f.call(t, "POST", "/api/admin/connections/yard/connect", map[string]string{"attemptId": "stale"}, f.adminKey, nil)
	suiteHTTPStatus(t, status, 412, raw)
	admin := mustConnectionAdmin(t, f)
	caller := storage.SuiteCaller{UserID: admin.ID}
	claimed, err := f.store.SuiteClaimConnection(context.Background(), caller, "yard", a.AttemptID, false)
	if err != nil {
		t.Fatal(err)
	}
	status, raw = connectAttempt(t, f, "yard", a.AttemptID)
	suiteHTTPStatus(t, status, 409, raw)
	if err = f.store.SuiteUpdateConnection(context.Background(), caller, claimed, true); err != nil {
		t.Fatal(err)
	}
	if err = f.store.SetUserStatus(context.Background(), admin.ID, storage.UserStatusDisabled); err != nil {
		t.Fatal(err)
	}
	status, raw = connectAttempt(t, f, "yard", a.AttemptID)
	if status != 401 && status != 403 {
		t.Fatal("blocked admin resumed setup", status, string(raw))
	}
}

func TestAnotherCurrentAdminCanFinishAcknowledgedSetup(t *testing.T) {
	f := suiteHTTPServer(t)
	p := startConnectionParticipant(t, f, "nox-yard")
	p.mode.Store(0)
	a := prepareConnection(t, f, "yard")
	status, raw := connectAttempt(t, f, "yard", a.AttemptID)
	suiteHTTPStatus(t, status, 200, raw)
	p.mu.Lock()
	intent := p.intents[0]
	p.mu.Unlock()
	p.completeAndAck(t, f, intent)
	original := mustConnectionAdmin(t, f)
	other, err := f.store.UpsertAllowedUser(context.Background(), "other-admin@example.com", "ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := f.store.CurrentAPIKey(context.Background(), other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.SetUserStatus(context.Background(), original.ID, storage.UserStatusDisabled); err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "POST", "/api/admin/connections/yard/connect", map[string]string{"attemptId": a.AttemptID}, otherKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	var done connectionSetupView
	_ = json.Unmarshal(raw, &done)
	if done.State != "completed" || p.deliveries.Load() != 1 {
		t.Fatal("current admin could not finish acknowledged setup")
	}
}

func TestAnotherAdminCanCancelPendingSetupAfterCreatorIsDisabled(t *testing.T) {
	f := suiteHTTPServer(t)
	p := startConnectionParticipant(t, f, "nox-yard")
	p.mode.Store(0)
	a := prepareConnection(t, f, "yard")
	status, raw := connectAttempt(t, f, "yard", a.AttemptID)
	suiteHTTPStatus(t, status, 200, raw)
	original := mustConnectionAdmin(t, f)
	other, err := f.store.UpsertAllowedUser(context.Background(), "cancel-admin@example.com", "ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.store.CurrentAPIKey(context.Background(), other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.SetUserStatus(context.Background(), original.ID, storage.UserStatusDisabled); err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "POST", "/api/admin/connections/yard/cancel", map[string]string{"attemptId": a.AttemptID}, key, nil)
	suiteHTTPStatus(t, status, 200, raw)
	var view connectionSetupView
	_ = json.Unmarshal(raw, &view)
	if view.State != "cancelled" {
		t.Fatal("administrator could not recover abandoned setup")
	}
}
func TestFoundationVerificationRejectsDifferentLocalProduct(t *testing.T) {
	f := suiteHTTPServer(t)
	_ = suiteTestUnixServer(t, f.s.cfg.SuiteSocketDir, "nox-gateway", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"contractVersion":"1.0","productId":"different-product","productVersion":"1","displayName":"Different","capabilities":{}}`))
	}))
	status, _, raw := f.call(t, "PUT", "/api/admin/connections/gateway", map[string]string{"origin": "http://native.invalid"}, f.adminKey, map[string]string{"If-None-Match": "*"})
	suiteHTTPStatus(t, status, 200, raw)
	var saved connectionView
	_ = json.Unmarshal(raw, &saved)
	status, _, raw = f.call(t, "POST", "/api/admin/connections/gateway/verify", map[string]string{"revision": saved.Revision}, f.adminKey, nil)
	suiteHTTPStatus(t, status, 409, raw)
	attempt, err := f.store.SuiteConnectionAttempt(context.Background(), "gateway")
	if err != nil || attempt.ID != "" {
		t.Fatal("wrong local identity became a verified attempt", err)
	}
}
