package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type backendSuiteFixture struct {
	*suiteHTTPFixture
	gateway   noxmcp.SuiteProvisioning
	record    noxmcp.SuiteRegistrationRecord
	member    storage.User
	memberKey string
}

func backendSuiteServer(t *testing.T) *backendSuiteFixture {
	t.Helper()
	f := suiteHTTPServer(t, true)
	gateway, _ := f.paired(t, "suite-execution-gateway", "gateway")
	status, _, raw := f.call(t, "GET", "/nox/integration", nil, "", nil)
	suiteHTTPStatus(t, status, 200, raw)
	decl, err := noxmcp.LoadSuiteProductDeclaration(raw)
	if err != nil || decl.ProductID != serviceName || decl.Capabilities.SuiteMcp.Path != backendSuiteMCPPath {
		t.Fatal("Backend declaration incompatible", err)
	}
	intent := f.create(t, serviceName, "product", f.backend.URL)
	proof := suiteHTTPProof(t, intent, raw, "")
	status, _, raw = f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", proof, "", nil)
	suiteHTTPStatus(t, status, 200, raw)
	provision, err := noxmcp.LoadSuiteProvisioning(raw)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/ack", nil, provision.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	status, _, raw = f.call(t, "PUT", "/api/nox/products/"+serviceName, map[string]string{"contractVersion": "1.0", "baseUrl": f.backend.URL}, f.adminKey, map[string]string{"If-None-Match": "*"})
	suiteHTTPStatus(t, status, 201, raw)
	record, err := noxmcp.LoadSuiteRegistrationRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	member, err := f.store.UpsertAllowedUser(context.Background(), "suite-reader@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.store.CurrentAPIKey(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &backendSuiteFixture{f, gateway, record, member, key}
}
func (f *backendSuiteFixture) token(t *testing.T, key string, probe bool) string {
	t.Helper()
	body := map[string]string{"contractVersion": "1.0", "productId": serviceName, "registrationRevision": f.record.Revision, "apiKey": key}
	path := "/api/nox/authorizations"
	if probe {
		path = "/api/nox/service-authorizations"
		delete(body, "apiKey")
	}
	status, _, raw := f.call(t, "POST", path, body, f.gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	var result struct {
		AccessToken string `json:"accessToken"`
	}
	if json.Unmarshal(raw, &result) != nil || result.AccessToken == "" {
		t.Fatal("no access token")
	}
	return result.AccessToken
}

type backendSuiteTransport struct {
	base  http.RoundTripper
	token string
}

func (rt backendSuiteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+rt.token)
	r.Header.Set("Nox-Request-Id", "2c6f7a3a-5f55-4d57-8703-c5597b85a9cb")
	r.Header.Set("Nox-Timeout-Ms", "20000")
	r.Header.Set("Nox-Deadline-At", time.Now().UTC().Add(20*time.Second).Format("2006-01-02T15:04:05.000Z"))
	return rt.base.RoundTrip(r)
}
func (f *backendSuiteFixture) connect(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "suite-protocol-test", Version: "test"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.backend.URL + backendSuiteMCPPath, DisableStandaloneSSE: true, MaxRetries: -1, HTTPClient: &http.Client{Transport: backendSuiteTransport{f.client.Transport, token}}}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		t.Fatal("Suite MCP initialize", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
func TestBackendSuiteLocalCatalogCapabilitiesAndOwnership(t *testing.T) {
	f := backendSuiteServer(t)
	ctx := context.Background()
	reader := f.connect(t, f.token(t, f.memberKey, false))
	admin := f.connect(t, f.token(t, f.adminKey, false))
	for _, tc := range []struct {
		session *mcp.ClientSession
		count   int
	}{{reader, 5}, {admin, 10}} {
		listed, err := tc.session.ListTools(ctx, &mcp.ListToolsParams{})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed.Tools) != tc.count {
			t.Fatalf("catalog has %d tools, want %d", len(listed.Tools), tc.count)
		}
		for _, tool := range listed.Tools {
			if tool.Meta["cli"] == nil || tool.Meta["nox/authorization"] == nil {
				t.Fatal("tool missing policy/CLI metadata", tool.Name)
			}
		}
	}
	if got := callBackendMCP(t, reader, "backend_whoami", nil, ""); got["user"] != f.member.Email {
		t.Fatal("wrong delegated identity", got)
	}
	suiteForbidden(t, reader, "backend_health", nil)
	suiteForbidden(t, reader, "backend_vault_create", map[string]any{"name": "forbidden"})
	// Legacy direct personal-key policy remains independently usable by USER.
	direct := connectBackendMCP(t, f.backend, f.memberKey)
	mine := callBackendMCP(t, direct, "backend_vault_create", map[string]any{"name": "reader-owned"}, "")
	mineID := mine["vaultId"].(string)
	callBackendMCP(t, reader, "backend_vault_status", map[string]any{"vaultId": mineID}, "")
	callBackendMCP(t, reader, "backend_file_list", map[string]any{"vaultId": mineID}, "")
	for _, name := range []string{"backend_vault_status", "backend_file_list", "backend_vault_delete", "backend_vault_restore", "backend_vault_purge"} {
		callBackendMCP(t, admin, name, map[string]any{"vaultId": mineID}, "NOT_FOUND")
	}
	created := callBackendMCP(t, admin, "backend_vault_create", map[string]any{"name": "admin-owned"}, "")
	id := created["vaultId"].(string)
	callBackendMCP(t, reader, "backend_file_list", map[string]any{"vaultId": id}, "NOT_FOUND")
	for _, name := range []string{"backend_vault_delete", "backend_vault_restore", "backend_vault_delete", "backend_vault_purge"} {
		callBackendMCP(t, admin, name, map[string]any{"vaultId": id}, "")
	}
	callBackendMCP(t, admin, "backend_vault_status", map[string]any{"vaultId": id}, "NOT_FOUND")
	callBackendMCP(t, admin, "backend_health", nil, "")
}
func suiteRPC(method string, params any) []byte {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	return body
}
func (f *backendSuiteFixture) rpc(t *testing.T, token string, body []byte, headers map[string]string) (int, []byte) {
	t.Helper()
	h := map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	for k, v := range headers {
		h[k] = v
	}
	status, _, raw := f.call(t, "POST", backendSuiteMCPPath, json.RawMessage(body), token, h)
	return status, raw
}
func TestBackendSuiteProfileTransportAndLimits(t *testing.T) {
	f := backendSuiteServer(t)
	init := suiteRPC("initialize", map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "probe", "version": "test"}})
	for _, token := range []string{"", f.memberKey, f.adminKey, f.gateway.ServiceCredential, "not.a.jwt"} {
		status, raw := f.rpc(t, token, init, nil)
		suiteHTTPStatus(t, status, 401, raw)
	}
	probe := f.token(t, "", true)
	status, raw := f.rpc(t, probe, init, nil)
	suiteHTTPStatus(t, status, 200, raw)
	for _, method := range []string{"tools/list", "tools/call", "nox/tools/catalog", "nox/tools/definitions", "resources/list", "prompts/list", "tasks/get"} {
		status, _ = f.rpc(t, probe, suiteRPC(method, map[string]any{"name": "backend_info", "arguments": map[string]any{}}), nil)
		if status != 403 {
			t.Fatal("probe admitted business method", method, status)
		}
	}
	batch := append([]byte("["), init...)
	batch = append(batch, ',')
	batch = append(batch, suiteRPC("tools/list", map[string]any{})...)
	batch = append(batch, ']')
	status, _ = f.rpc(t, probe, batch, nil)
	if status != 403 {
		t.Fatal("probe batch bypass", status)
	}
	token := f.token(t, f.adminKey, false)
	call := suiteRPC("tools/call", map[string]any{"name": "backend_vault_create", "arguments": map[string]any{"name": "expired-write"}})
	status, _ = f.rpc(t, token, call, nil)
	if status != 400 {
		t.Fatal("missing controls accepted", status)
	}
	controls := map[string]string{"Nox-Request-Id": "2c6f7a3a-5f55-4d57-8703-c5597b85a9cb", "Nox-Timeout-Ms": "20000", "Nox-Deadline-At": time.Now().UTC().Add(-time.Second).Format("2006-01-02T15:04:05.000Z")}
	status, _ = f.rpc(t, token, call, controls)
	if status != 408 {
		t.Fatal("expired write admitted", status)
	}
	status, _ = f.rpc(t, token, bytes.Repeat([]byte("x"), noxmcp.SuiteMaxPayloadBytes+1), nil)
	if status != 413 {
		t.Fatal("payload limit not enforced", status)
	}
	// Trusted transport cannot be asserted by an ordinary HTTP caller.
	insecure := httptest.NewServer(f.s.Routes())
	defer insecure.Close()
	request, _ := http.NewRequest("POST", insecure.URL+backendSuiteMCPPath, bytes.NewReader(init))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Nox-Suite-Transport", "https")
	response, err := insecure.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("forged HTTPS admitted", response.StatusCode)
	}
	status, _, raw = f.call(t, "POST", backendSuiteMCPPath+"?api_key="+f.memberKey, json.RawMessage(init), token, nil)
	suiteHTTPStatus(t, status, 400, raw)
	// The declaration is public metadata, not automatic membership or authority.
	unregistered := suiteHTTPServer(t, true)
	status, _, raw = unregistered.call(t, "GET", "/nox/integration", nil, "", nil)
	suiteHTTPStatus(t, status, 200, raw)
	status, _, raw = unregistered.call(t, "POST", backendSuiteMCPPath, json.RawMessage(init), token, nil)
	suiteHTTPStatus(t, status, 401, raw)
}
func TestBackendSuiteRevocationAndSharedMutationGuard(t *testing.T) {
	f := backendSuiteServer(t)
	ctx := context.Background()
	// A secondary ADMIN can mutate its own data. A prepared operation cannot commit
	// after its role changes, even if it bypasses the HTTP admission already done.
	err := f.store.SetUserRole(ctx, f.member.ID, "ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	token := f.token(t, f.memberKey, false)
	admitted, err := f.store.SuiteAcceptBackendToken(ctx, token, f.backend.URL, backendSuiteMCPPath)
	if err != nil {
		t.Fatal(err)
	}
	guarded := storage.WithSuiteBackendIdentity(ctx, admitted, f.backend.URL, backendSuiteMCPPath)
	if _, err = f.store.CreateVault(guarded, "another-user", "wrong owner"); err == nil {
		t.Fatal("guard allowed identity substitution")
	}
	cancelled, cancel := context.WithCancel(guarded)
	cancel()
	if _, err = f.store.CreateVault(cancelled, f.member.ID, "cancelled"); err == nil {
		t.Fatal("cancelled write accepted")
	}
	err = f.store.SetUserRole(ctx, f.member.ID, "USER")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateVault(guarded, f.member.ID, "stale grant"); err == nil {
		t.Fatal("stale admitted grant committed")
	}
	status, raw := f.rpc(t, token, suiteRPC("ping", map[string]any{}), nil)
	suiteHTTPStatus(t, status, 401, raw)
	fresh := f.token(t, f.memberKey, false)
	session := f.connect(t, fresh)
	suiteForbidden(t, session, "backend_vault_create", map[string]any{"name": "no write"})
	_, err = f.store.RotateAPIKey(ctx, f.member.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, raw = f.rpc(t, fresh, suiteRPC("ping", map[string]any{}), nil)
	suiteHTTPStatus(t, status, 401, raw)
	adminToken := f.token(t, f.adminKey, false)
	status, _, raw = f.call(t, "DELETE", "/api/nox/products/"+serviceName, nil, f.adminKey, map[string]string{"If-Match": fmt.Sprintf("%q", f.record.Revision)})
	if status != 204 {
		t.Fatal("remove own registration", status, string(raw))
	}
	status, raw = f.rpc(t, adminToken, suiteRPC("ping", map[string]any{}), nil)
	suiteHTTPStatus(t, status, 401, raw)
	vaults, err := f.store.ListVaults(ctx, f.member.ID)
	if err != nil || len(vaults) != 0 {
		t.Fatal("rejected writes changed storage", vaults, err)
	}
}
func TestBackendSuiteIndependentMCPConfiguration(t *testing.T) {
	for _, suiteEnabled := range []bool{false, true} {
		for _, directEnabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("suite=%v/direct=%v", suiteEnabled, directEnabled), func(t *testing.T) {
				s, _, _, _ := newTestServer(t)
				s.cfg.SuiteMCPEnabled = suiteEnabled
				s.cfg.DirectMCPDisabled = !directEnabled
				httpServer := httptest.NewTLSServer(s.Routes())
				defer httpServer.Close()
				res, err := httpServer.Client().Get(httpServer.URL + "/nox/integration")
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := io.ReadAll(res.Body)
				res.Body.Close()
				decl, err := noxmcp.LoadSuiteProductDeclaration(raw)
				if err != nil || (decl.Capabilities.SuiteMcp != nil) != suiteEnabled {
					t.Fatal("capability does not match configuration", err)
				}
				for path, enabled := range map[string]bool{"/mcp": directEnabled, backendSuiteMCPPath: suiteEnabled} {
					req, _ := http.NewRequest("POST", httpServer.URL+path, strings.NewReader(`{}`))
					res, err = httpServer.Client().Do(req)
					if err != nil {
						t.Fatal(err)
					}
					res.Body.Close()
					if (res.StatusCode != 404) != enabled {
						t.Fatal("route does not match configuration", path, res.StatusCode)
					}
				}
			})
		}
	}
}

func suiteForbidden(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	_, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err == nil || !strings.Contains(err.Error(), "FORBIDDEN") {
		t.Fatal("filtered tool bypassed authorization", name, err)
	}
}
func TestBackendSuiteConcurrentIdentitiesAndGlobalExecutionLifetime(t *testing.T) {
	f := backendSuiteServer(t)
	ctx := context.Background()
	adminToken, readerToken := f.token(t, f.adminKey, false), f.token(t, f.memberKey, false)
	admin, reader := f.connect(t, adminToken), f.connect(t, readerToken)
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			session, email := reader, f.member.Email
			if i%2 == 0 {
				session = admin
				email = "suite-admin@example.com"
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "backend_whoami", Arguments: map[string]any{}})
			if err != nil {
				failures <- err
				return
			}
			raw, _ := json.Marshal(result.StructuredContent)
			var got map[string]any
			_ = json.Unmarshal(raw, &got)
			if result.IsError || got["user"] != email {
				failures <- fmt.Errorf("identity leaked: %s", raw)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	// A non-cooperating executor must retain capacity after its runtime times out.
	// Two distinct SDK factories share the Backend execution gate.
	f.s.suiteMCPExecutions = make(chan struct{}, 1)
	admitted, err := f.store.SuiteAcceptBackendToken(ctx, adminToken, f.backend.URL, backendSuiteMCPPath)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := f.s.mcpTools()
	if err != nil {
		t.Fatal(err)
	}
	entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		select {
		case <-exited:
		case <-time.After(time.Second):
			t.Error("executor did not exit")
		}
	}()
	tool := tools[1]
	tool.Execute = func(noxmcp.ExecutionContext, map[string]any) (map[string]any, error) {
		close(entered)
		<-release
		defer close(exited)
		return nil, nil
	}
	tool = f.s.bindSuiteMCPTool(tool, admitted, f.backend.URL)
	config := noxmcp.SuiteRuntimeOptions{Verification: func() noxmcp.SuiteVerificationOptions { return admitted.Verification }, Policies: backendSuitePolicies}
	first, err := noxmcp.NewSuite(noxmcp.Options{AppID: serviceName, Name: serviceName, Tools: []noxmcp.Tool{tool}}, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := noxmcp.NewSuite(noxmcp.Options{AppID: serviceName, Name: serviceName, Tools: []noxmcp.Tool{tool}}, config)
	if err != nil {
		t.Fatal(err)
	}
	info, err := noxmcp.SuiteTokenInfo(admitted.Identity)
	if err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := first.Execute(bounded, tool.Name, map[string]any{}, info); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	err = <-done
	var mcpErr *noxmcp.Error
	if !errors.As(err, &mcpErr) || mcpErr.Code != "TIMEOUT" {
		t.Fatal("missing timeout", err)
	}
	_, err = second.Execute(ctx, tool.Name, map[string]any{}, info)
	if !errors.As(err, &mcpErr) || mcpErr.Code != "OVERLOADED" {
		t.Fatal("factory lost retained capacity", err)
	}
	close(release)
	<-exited
	// The defer holding the capacity runs just after the executor signals exit.
	end := time.Now().Add(time.Second)
	for len(f.s.suiteMCPExecutions) != 0 && time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	if len(f.s.suiteMCPExecutions) != 0 {
		t.Fatal("capacity not released after actual exit")
	}
}

func TestBackendSuiteCurrentSourceKeyAndAccountAdmission(t *testing.T) {
	for _, scenario := range []string{"gateway-revoked", "account-disabled", "signing-key-revoked", "bad-signature", "wrong-target", "incompatible-target"} {
		t.Run(scenario, func(t *testing.T) {
			f := backendSuiteServer(t)
			token := f.token(t, f.memberKey, false)
			ctx := context.Background()
			switch scenario {
			case "gateway-revoked":
				status, _, raw := f.call(t, "GET", "/api/nox/services/"+f.gateway.Service.ServiceID, nil, f.adminKey, nil)
				suiteHTTPStatus(t, status, 200, raw)
				var view struct {
					Revision string `json:"revision"`
				}
				if err := json.Unmarshal(raw, &view); err != nil {
					t.Fatal(err)
				}
				status, _, raw = f.call(t, "DELETE", "/api/nox/services/"+f.gateway.Service.ServiceID, nil, f.adminKey, map[string]string{"If-Match": fmt.Sprintf("%q", view.Revision)})
				if status != 200 && status != 204 {
					t.Fatal("revoke Gateway", status, string(raw))
				}
			case "account-disabled":
				if err := f.store.SetUserStatus(ctx, f.member.ID, "DISABLED"); err != nil {
					t.Fatal(err)
				}
			case "signing-key-revoked":
				keys, err := f.store.SuiteKeys(ctx)
				if err != nil {
					t.Fatal(err)
				}
				status, _, raw := f.call(t, "DELETE", "/api/nox/keys/"+*keys.Keys[0].Kid, nil, f.adminKey, map[string]string{"If-Match": fmt.Sprintf("%q", keys.Revision)})
				suiteHTTPStatus(t, status, 200, raw)
			case "bad-signature":
				parts := strings.Split(token, ".")
				sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
				sig[0] ^= 1
				parts[2] = base64.RawURLEncoding.EncodeToString(sig)
				token = strings.Join(parts, ".")
			case "wrong-target":
				other := issuanceHTTPServer(t)
				status, _, raw := other.call(t, "POST", "/api/nox/authorizations", other.body(), other.gateway.ServiceCredential, nil)
				suiteHTTPStatus(t, status, 200, raw)
				var result struct {
					AccessToken string `json:"accessToken"`
				}
				_ = json.Unmarshal(raw, &result)
				token = result.AccessToken
			case "incompatible-target":
				// A verified incompatible update retains membership but blocks admission.
				// Use the current persisted verification binding via public preparation.
				admin, authErr := f.s.apiUser(ctx, f.adminKey)
				if authErr != nil {
					t.Fatal(authErr)
				}
				req := noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: f.backend.URL}
				verified, err := f.store.SuitePrepareRegistration(ctx, storage.SuiteCaller{UserID: admin.ID}, serviceName, req, f.record.Revision, "block-preparation", "block-digest")
				if err != nil {
					t.Fatal(err)
				}
				if err = f.store.SuiteBlockIncompatibleRegistration(ctx, storage.SuiteCaller{UserID: admin.ID}, serviceName, req, f.record.Revision, verified); err != nil {
					t.Fatal(err)
				}
			}
			status, raw := f.rpc(t, token, suiteRPC("ping", map[string]any{}), nil)
			suiteHTTPStatus(t, status, 401, raw)
		})
	}
}
