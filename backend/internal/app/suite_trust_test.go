package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

type suiteHTTPFixture struct {
	s        *Server
	store    *storage.Store
	backend  *httptest.Server
	client   *http.Client
	adminKey string
	counter  atomic.Int64
	dir      string
}

func suiteHTTPServer(t *testing.T, suiteEnabled ...bool) *suiteHTTPFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := store.UpsertAllowedUser(ctx, "suite-admin@example.com", "ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CurrentAPIKey(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	socketDir := shortSuiteDirectory(t)
	s := NewServer(Config{SuiteSocketDir: socketDir, Version: "test", SuiteMCPEnabled: len(suiteEnabled) > 0 && suiteEnabled[0], AdminEmails: []string{admin.Email}}, store)
	backend := suiteTestBackend(t, s)
	s.cfg.PublicURL = backend.URL
	f := &suiteHTTPFixture{s: s, store: store, backend: backend, client: backend.Client(), adminKey: key, dir: dir}
	t.Cleanup(func() { f.backend.Close(); _ = f.store.Close() })
	return f
}
func (f *suiteHTTPFixture) call(t *testing.T, method, path string, body any, credential string, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	var raw []byte
	var err error
	if body != nil {
		if value, ok := body.(json.RawMessage); ok {
			raw = value
		} else {
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	request, err := http.NewRequest(method, f.backend.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if method != "GET" && request.Header.Get("Idempotency-Key") == "" {
		request.Header.Set("Idempotency-Key", fmt.Sprintf("request-%d", f.counter.Add(1)))
	}
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err = io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.Get("Cache-Control") != "no-store" && strings.HasPrefix(path, "/api/nox/") {
		t.Fatal("Suite response cached")
	}
	if response.Header.Get("Location") != "" {
		t.Fatal("Suite management returned a browser redirect")
	}
	return response.StatusCode, response.Header, raw
}
func suiteHTTPStatus(t *testing.T, status, want int, raw []byte) {
	t.Helper()
	if status != want {
		t.Fatalf("expected HTTP %d, got %d: %s", want, status, raw)
	}
	if !json.Valid(raw) {
		t.Fatal("non-JSON Suite response", string(raw))
	}
}
func (f *suiteHTTPFixture) product(t *testing.T, product string, handler http.HandlerFunc) (*httptest.Server, json.RawMessage, string) {
	t.Helper()
	decl := json.RawMessage(fmt.Sprintf(`{"contractVersion":"1.0","productId":%q,"productVersion":"fixture","displayName":"Local protocol fixture","capabilities":{},"optionalExtension":{"n":1}}`, product))
	server := suiteTestUnixServer(t, f.s.cfg.SuiteSocketDir, product, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("Backend forwarded client credentials to product")
		}
		if r.URL.Path != "/nox/integration" {
			t.Error("unexpected bootstrap path")
		}
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(decl)
	}))
	t.Cleanup(server.Close)
	return server, decl, ""
}
func suiteHTTPProof(t *testing.T, intent noxmcp.SuitePairingIntent, decl []byte, pin string) noxmcp.SuitePairingProof {
	t.Helper()
	doc, err := noxmcp.ParseSuiteJSON(decl)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := noxmcp.Fingerprint(doc)
	if err != nil {
		t.Fatal(err)
	}
	binding := noxmcp.SuitePairingBinding{ContractVersion: "1.0", PairingID: intent.PairingID, Nonce: intent.Nonce, ProductID: intent.ProductID, DeclarationFingerprint: fp, BaseUrl: intent.BaseUrl, TlsPublicKeyFingerprint: nil}
	if pin != "" {
		binding.TlsPublicKeyFingerprint = suiteTestStringPointer(pin)
	}
	secret, err := base64.RawURLEncoding.DecodeString(intent.Secret)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := noxmcp.CreateSuitePairingProof(secret, binding)
	if err != nil {
		t.Fatal(err)
	}
	return noxmcp.SuitePairingProof{ContractVersion: "1.0", Binding: binding, Proof: proof}
}
func (f *suiteHTTPFixture) create(t *testing.T, product, purpose, origin string) noxmcp.SuitePairingIntent {
	t.Helper()
	status, _, raw := f.call(t, "POST", "/api/nox/pairings", map[string]any{"contractVersion": "1.0", "productId": product, "purpose": purpose, "baseUrl": origin, "grants": []string{"authorization.delegate"}}, f.adminKey, map[string]string{"If-None-Match": "*"})
	suiteHTTPStatus(t, status, 201, raw)
	intent, err := noxmcp.LoadSuitePairingIntent(raw)
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func TestSuiteLocalPairingRecoveryAndServiceCredentialBoundaries(t *testing.T) {
	f := suiteHTTPServer(t)
	product, decl, pin := f.product(t, "future-product", nil)
	intent := f.create(t, "future-product", "product", product.URL)
	proof := suiteHTTPProof(t, intent, decl, pin)
	status, _, first := f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", proof, "", map[string]string{"Idempotency-Key": "complete"})
	suiteHTTPStatus(t, status, 200, first)
	provision, err := noxmcp.LoadSuiteProvisioning(first)
	if err != nil {
		t.Fatal(err)
	}
	if len(provision.Service.Grants) != 1 || provision.Service.Grants[0] != "events.read" {
		t.Fatal("unknown grants request escalated product authority")
	}
	status, _, again := f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", proof, "", map[string]string{"Idempotency-Key": "complete"})
	suiteHTTPStatus(t, status, 200, again)
	if !bytes.Equal(first, again) {
		t.Fatal("response loss minted different provisioning")
	}
	status, _, raw := f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/ack", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 403, raw)
	status, _, raw = f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/ack", nil, provision.ServiceCredential, map[string]string{"Idempotency-Key": "ack"})
	suiteHTTPStatus(t, status, 200, raw)
	status, _, raw = f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", proof, "", map[string]string{"Idempotency-Key": "complete"})
	suiteHTTPStatus(t, status, 409, raw)
	status, _, raw = f.call(t, "GET", "/api/nox/services/"+provision.Service.ServiceID, nil, provision.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	if bytes.Contains(raw, []byte(provision.ServiceCredential)) {
		t.Fatal("service metadata leaked credential")
	}
	status, _, raw = f.call(t, "GET", "/api/nox/services", nil, provision.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 403, raw)
	status, _, raw = f.call(t, "GET", "/api/admin/users", nil, provision.ServiceCredential, nil)
	if status != 401 {
		t.Fatalf("service credential accepted as personal admin key: %d %s", status, raw)
	}
	status, _, raw = f.call(t, "GET", "/v1/auth/check", nil, provision.ServiceCredential, nil)
	if status != 401 {
		t.Fatal("service credential accepted by existing client API")
	}
	// An ordinary product credential cannot issue user delegations.
	status, _, raw = f.call(t, "POST", "/api/nox/authorizations", map[string]any{"contractVersion": "1.0"}, provision.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 403, raw)
}

func TestSuiteLocalVerificationRejectsIdentityProofRedirectAndTargetChanges(t *testing.T) {
	cases := []string{"wrong-pin", "redirect", "wrong-declaration", "denied-address", "denied-port", "bad-proof", "duplicate-json", "unsupported-version"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			f := suiteHTTPServer(t)
			var hits atomic.Int64
			var handler http.HandlerFunc
			if name == "redirect" {
				handler = func(w http.ResponseWriter, _ *http.Request) {
					hits.Add(1)
					w.Header().Set("Location", "https://unapproved.example.com/steal")
					w.WriteHeader(302)
				}
			}
			product, decl, pin := f.product(t, "verified-product", handler)
			intent := f.create(t, "verified-product", "product", product.URL)

			if name == "wrong-declaration" {
				decl = []byte(`{"contractVersion":"1.0","productId":"different-product","productVersion":"1","displayName":"other","capabilities":{}}`)
			}
			proof := suiteHTTPProof(t, intent, decl, pin)
			var body any = proof
			want := 503
			switch name {
			case "wrong-pin":
				proof.Binding.TlsPublicKeyFingerprint = suiteTestStringPointer(strings.Repeat("a", 43))
				body = proof
				want = 400
			case "redirect":
				want = 403
			case "wrong-declaration":
				want = 409
			case "denied-address":
				f.s.cfg.SuiteSocketDir = t.TempDir()
				want = 503
			case "denied-port":
				proof.Binding.BaseUrl = "http://verified-product:8095"
				body = proof
				want = 403
			case "bad-proof":
				proof.Proof = strings.Repeat("a", 43)
				body = proof
				want = 403
			case "duplicate-json":
				body = json.RawMessage(`{"contractVersion":"1.0","contractVersion":"1.0"}`)
				want = 400
			case "unsupported-version":
				body = json.RawMessage(`{"contractVersion":"99.0"}`)
				want = 422
			}
			status, _, raw := f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", body, "", nil)
			suiteHTTPStatus(t, status, want, raw)
			if name == "redirect" && hits.Load() != 1 {
				t.Fatal("declaration redirect followed")
			}
			status, _, raw = f.call(t, "GET", "/api/nox/services", nil, f.adminKey, nil)
			suiteHTTPStatus(t, status, 200, raw)
			if string(raw) != "[]" {
				t.Fatal("failed verification still provisioned service", string(raw))
			}
		})
	}
}

func TestSuiteTransportCannotBeSpoofedByForwardedHeaders(t *testing.T) {
	f := suiteHTTPServer(t)
	request := httptest.NewRequest("GET", "http://backend/api/nox/keys", nil)
	request.RemoteAddr = "192.0.2.10:44000"
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Nox-Suite-Transport", "https")
	response := httptest.NewRecorder()
	f.s.Routes().ServeHTTP(response, request)
	suiteHTTPStatus(t, response.Code, 403, response.Body.Bytes())
	request.Header.Del("X-Nox-Suite-Transport")
	response = httptest.NewRecorder()
	f.s.Routes().ServeHTTP(response, request)
	suiteHTTPStatus(t, response.Code, 403, response.Body.Bytes())
}

func TestSuiteKeyManagementUsesJSONPreconditionsAndCurrentAdmin(t *testing.T) {
	f := suiteHTTPServer(t)
	status, headers, raw := f.call(t, "GET", "/api/nox/keys", nil, "", nil)
	suiteHTTPStatus(t, status, 200, raw)
	if strings.Contains(string(raw), `"d"`) {
		t.Fatal("private key exposed")
	}
	status, _, raw = f.call(t, "POST", "/api/nox/keys", nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 428, raw)
	status, _, raw = f.call(t, "POST", "/api/nox/keys", nil, f.adminKey, map[string]string{"If-Match": headers.Get("ETag"), "Idempotency-Key": "prepare"})
	suiteHTTPStatus(t, status, 201, raw)
	var prepared struct {
		ID       string `json:"keyId"`
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(raw, &prepared); err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "POST", "/api/nox/keys/"+prepared.ID+"/activate", nil, f.adminKey, map[string]string{"If-Match": `"` + prepared.Revision + `"`})
	suiteHTTPStatus(t, status, 200, raw)
	// A member cannot inspect trust management, regardless of a claimed role flag.
	member, err := f.store.UpsertAllowedUser(context.Background(), "member@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.store.CurrentAPIKey(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "GET", "/api/nox/services?role=ADMIN", nil, key, nil)
	suiteHTTPStatus(t, status, 403, raw)
}

func TestSuiteCertificateAndConfigFailureAreExplicit(t *testing.T) {
	f := suiteHTTPServer(t)
	f.s.cfg.SuiteSocketDir = ""
	status, _, raw := f.call(t, "GET", "/api/nox/keys", nil, "", nil)
	suiteHTTPStatus(t, status, 503, raw)
	if err := validateSuiteConfig(Config{TLSCertFile: "only-cert"}); err == nil {
		t.Fatal("half TLS configuration accepted")
	}
	// A private address does not establish same-host membership.
	if err := f.s.suiteIntentTarget("https://192.168.10.10"); err == nil {
		t.Fatal("implicit private-network trust")
	}
}

// The same SQLite and secret store must be used together after process restart.
func TestSuiteHTTPRecoverySurvivesBackendProcessRestart(t *testing.T) {
	f := suiteHTTPServer(t)
	product, decl, pin := f.product(t, "restart-product", nil)
	intent := f.create(t, "restart-product", "product", product.URL)
	proof := suiteHTTPProof(t, intent, decl, pin)
	path := "/api/nox/pairings/" + intent.PairingID + "/complete"
	status, _, first := f.call(t, "POST", path, proof, "", map[string]string{"Idempotency-Key": "recover"})
	suiteHTTPStatus(t, status, 200, first)
	// Restart preserves the configured native URL and the local protocol identity.
	origin := f.s.cfg.PublicURL
	config := f.s.cfg
	f.backend.Close()
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(context.Background(), f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.store = reopened
	f.s = NewServer(config, reopened)
	f.s.cfg.PublicURL = origin
	f.backend = suiteTestBackend(t, f.s)
	f.client = f.backend.Client()
	status, _, again := f.call(t, "POST", path, proof, "", map[string]string{"Idempotency-Key": "recover"})
	suiteHTTPStatus(t, status, 200, again)
	if !bytes.Equal(first, again) {
		t.Fatal("restart changed recoverable provisioning")
	}
	raw, err := os.ReadFile(filepath.Join(f.dir, "secrets", "suite", "master.key"))
	if err != nil || len(raw) != 32 {
		t.Fatal("missing persisted secret-store key", err)
	}
}

func suiteTestStringPointer(value string) *string { return &value }
