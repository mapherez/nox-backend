package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

type issuanceHTTPFixture struct {
	*suiteHTTPFixture
	gateway     noxmcp.SuiteProvisioning
	record      noxmcp.SuiteRegistrationRecord
	product     *httptest.Server
	declaration []byte
	current     *atomic.Value
	member      storage.User
	memberKey   string
}

func issuanceHTTPServer(t *testing.T) *issuanceHTTPFixture {
	t.Helper()
	f := suiteHTTPServer(t)
	gateway, _ := f.paired(t, "http-delegator", "gateway")
	var current atomic.Value
	product, decl, pin := f.product(t, "http-delegated-product", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(current.Load().([]byte))
	})
	var doc map[string]any
	if err := json.Unmarshal(decl, &doc); err != nil {
		t.Fatal(err)
	}
	doc["capabilities"] = map[string]any{"suiteMcp": map[string]string{"path": "/suite/mcp"}}
	decl, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	current.Store([]byte(decl))
	intent := f.create(t, "http-delegated-product", "product", product.URL)
	proof := suiteHTTPProof(t, intent, decl, pin)
	status, _, raw := f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", proof, "", nil)
	suiteHTTPStatus(t, status, 200, raw)
	provision, err := noxmcp.LoadSuiteProvisioning(raw)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/ack", nil, provision.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	status, _, raw = f.call(t, "PUT", "/api/nox/products/http-delegated-product", map[string]any{"contractVersion": "1.0", "baseUrl": product.URL}, f.adminKey, map[string]string{"If-None-Match": "*"})
	suiteHTTPStatus(t, status, 201, raw)
	record, err := noxmcp.LoadSuiteRegistrationRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	member, err := f.store.UpsertAllowedUser(context.Background(), "http-inspector@example.com", "USER")
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.store.CurrentAPIKey(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	return &issuanceHTTPFixture{f, gateway, record, product, decl, &current, member, key}
}
func (f *issuanceHTTPFixture) body() map[string]any {
	return map[string]any{"contractVersion": "1.0", "productId": f.record.ProductID, "registrationRevision": f.record.Revision, "apiKey": f.memberKey}
}
func (f *issuanceHTTPFixture) options(t *testing.T, kind string) noxmcp.SuiteVerificationOptions {
	t.Helper()
	keys, err := f.store.SuiteKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	trusted := map[string]noxmcp.SuiteSigningKey{}
	for _, key := range keys.Keys {
		trusted[*key.Kid] = key
	}
	return noxmcp.SuiteVerificationOptions{Issuer: keys.Issuer, Audience: f.product.URL + "/suite/mcp", ProductID: f.record.ProductID, AssociationID: f.record.AssociationID, RegistrationRevision: f.record.Revision, GatewayServiceID: f.gateway.Service.ServiceID, ServiceCredentialRevision: f.gateway.Service.CredentialRevision, Kind: kind, Keys: trusted, Now: time.Now()}
}
func (f *issuanceHTTPFixture) exchange(t *testing.T, path string, body any) ([]byte, *noxmcp.VerifiedSuiteIdentity) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", f.backend.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.gateway.ServiceCredential)
	req.Header.Set("Content-Type", "application/json")
	// These POSTs are on-demand authority reads, not idempotent management mutations.
	if req.Header.Get("Idempotency-Key") != "" {
		t.Fatal("test unexpectedly supplied an idempotency key")
	}
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err = io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	suiteHTTPStatus(t, res.StatusCode, 200, raw)
	if res.Header.Get("Cache-Control") != "no-store" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("authorization response cache/content policy missing")
	}
	kind := "user"
	if path == "/api/nox/service-authorizations" {
		kind = "probe"
	}
	identity, err := noxmcp.VerifySuiteAuthorizationResponse(raw, f.options(t, kind))
	if err != nil {
		t.Fatal("published SDK rejected HTTP grant", err)
	}
	return raw, identity
}
func TestSuiteLocalIssuanceSDKProfilesAndNoRepeatedProductOrGoogleContact(t *testing.T) {
	f := issuanceHTTPServer(t)
	// Accepted target metadata/trust is local authority; exchange never re-fetches
	// product declarations or contacts Google, even when the product is offline.
	f.product.Close()
	body := f.body()
	body["scopes"] = []string{"nox:write", "nox:sensitive-read"}
	body["userId"] = "injected-user"
	body["role"] = "ADMIN"
	body["expiresIn"] = 999999
	first, identity := f.exchange(t, "/api/nox/authorizations", body)
	if identity.Context()["userId"] != f.member.ID || identity.Claims()["scope"] != "nox:inspect" {
		t.Fatal("request assertions overrode Backend authority")
	}
	second, _ := f.exchange(t, "/api/nox/authorizations", body)
	if bytes.Equal(first, second) {
		t.Fatal("fresh exchange replayed old grant")
	}
	probeBody := map[string]any{"contractVersion": "1.0", "productId": f.record.ProductID, "registrationRevision": f.record.Revision, "apiKey": "ignored-user-assertion", "scope": "nox:write"}
	probeRaw, probe := f.exchange(t, "/api/nox/service-authorizations", probeBody)
	if probe.Kind() != "probe" || probe.Claims()["scope"] != "nox:mcp-probe" {
		t.Fatal("probe acquired user scopes")
	}
	if _, err := noxmcp.VerifySuiteAuthorizationResponse(probeRaw, f.options(t, "user")); err == nil {
		t.Fatal("probe became a user grant")
	}
	for _, token := range []string{identity.AccessToken(), probe.AccessToken()} {
		status, _, raw := f.call(t, "GET", "/v1/auth/check", nil, token, nil)
		if status != 401 {
			t.Fatal("Suite grant became a native personal API key", status, string(raw))
		}
	}
}
func TestSuiteLocalIssuanceRequiresCurrentGatewayAndProtectedBody(t *testing.T) {
	f := issuanceHTTPServer(t)
	body := f.body()
	for _, credential := range []string{"", f.adminKey, f.memberKey} {
		status, _, raw := f.call(t, "POST", "/api/nox/authorizations", body, credential, nil)
		want := 403
		if credential == "" {
			want = 401
		}
		suiteHTTPStatus(t, status, want, raw)
	}
	ordinary, _ := f.paired(t, "ordinary-participant", "product")
	status, _, raw := f.call(t, "POST", "/api/nox/authorizations", body, ordinary.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 403, raw)
	session, err := f.store.CreateWebSession(context.Background(), f.member.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "POST", "/api/nox/authorizations", body, "", map[string]string{"Cookie": "nox_sync_session=" + session, "X-Nox-Request": "1", "Origin": f.backend.URL})
	suiteHTTPStatus(t, status, 403, raw)
	for _, path := range []string{"/api/nox/authorizations?apiKey=not-a-real-credential", "/api/nox/service-authorizations?unexpected=1"} {
		status, _, raw = f.call(t, "POST", path, body, f.gateway.ServiceCredential, nil)
		suiteHTTPStatus(t, status, 400, raw)
	}
	status, _, raw = f.call(t, "POST", "/api/nox/authorizations", json.RawMessage(`{"contractVersion":"1.0","apiKey":"a","apiKey":"b"}`), f.gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 400, raw)
	body["contractVersion"] = "2.0"
	status, _, raw = f.call(t, "POST", "/api/nox/authorizations", body, f.gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 422, raw)
	body = f.body()
	body["apiKey"] = f.gateway.ServiceCredential
	status, _, raw = f.call(t, "POST", "/api/nox/authorizations", body, f.gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 401, raw)
	body = f.body()
	body["registrationRevision"] = "stale"
	status, _, raw = f.call(t, "POST", "/api/nox/authorizations", body, f.gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 409, raw)
	status, _, raw = f.call(t, "GET", "/api/nox/authorizations", nil, f.gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 405, raw)
	// A freshly provisioned/unacknowledged Gateway is authenticated for ACK only.
	product, decl, pin := f.productFixture(t)
	intent := f.create(t, "pending-delegator", "gateway", product.URL)
	proof := suiteHTTPProof(t, intent, decl, pin)
	status, _, raw = f.call(t, "POST", "/api/nox/pairings/"+intent.PairingID+"/complete", proof, "", nil)
	suiteHTTPStatus(t, status, 200, raw)
	pending, err := noxmcp.LoadSuiteProvisioning(raw)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "POST", "/api/nox/authorizations", f.body(), pending.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 403, raw)
}
func (f *issuanceHTTPFixture) productFixture(t *testing.T) (*httptest.Server, json.RawMessage, string) {
	return f.suiteHTTPFixture.product(t, "pending-delegator", nil)
}
func TestSuiteHTTPNativeRoleChangeInvalidatesSDKCacheAndLateRenewal(t *testing.T) {
	f := issuanceHTTPServer(t)
	ctx := context.Background()
	_, identity := f.exchange(t, "/api/nox/authorizations", f.body())
	options := f.options(t, "user")
	digest, err := noxmcp.SuiteCredentialPartition(f.memberKey, bytes.Repeat([]byte{42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	partition := noxmcp.SuiteAuthorizationPartition{CredentialPartition: digest, Issuer: options.Issuer, Audience: options.Audience, GatewayServiceID: options.GatewayServiceID, ServiceCredentialRevision: options.ServiceCredentialRevision, ProductID: options.ProductID, AssociationID: options.AssociationID, RegistrationRevision: options.RegistrationRevision, Kind: "user"}
	cache, err := noxmcp.NewSuiteAuthorizationCache(noxmcp.SuiteCacheOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cache.Refresh(ctx, partition, func(context.Context) (*noxmcp.VerifiedSuiteIdentity, error) { return identity, nil }); err != nil {
		t.Fatal(err)
	}
	status, _, raw := f.call(t, "GET", "/api/nox/products", nil, f.gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	snapshot, err := noxmcp.LoadSuiteRegistrySnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = f.call(t, "PUT", "/api/admin/users/"+f.member.ID+"/role", map[string]string{"role": "ADMIN"}, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	status, _, raw = f.call(t, "GET", "/api/nox/events?waitMs=0&after="+url.QueryEscape(snapshot.Cursor), nil, f.gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	page, err := noxmcp.LoadSuiteEventPage(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range page.Events {
		body, _ := json.Marshal(event)
		if err = cache.ApplyEvent(body); err != nil {
			t.Fatal(err)
		}
	}
	cached, err := cache.Get(partition)
	if err != nil || cached != nil {
		t.Fatal("native role event left cached authority", err)
	}
	_, admin := f.exchange(t, "/api/nox/authorizations", f.body())
	if admin.Claims()["scope"] != "nox:inspect nox:write nox:sensitive-read" {
		t.Fatal("promotion did not affect renewal")
	}
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := cache.Refresh(ctx, partition, func(context.Context) (*noxmcp.VerifiedSuiteIdentity, error) {
			close(started)
			<-release
			return admin, nil
		})
		done <- err
	}()
	<-started
	status, _, raw = f.call(t, "PUT", "/api/admin/users/"+f.member.ID+"/role", map[string]string{"role": "USER"}, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	status, _, raw = f.call(t, "GET", "/api/nox/events?waitMs=0&after="+url.QueryEscape(page.Cursor), nil, f.gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 200, raw)
	page, err = noxmcp.LoadSuiteEventPage(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range page.Events {
		body, _ := json.Marshal(event)
		if err = cache.ApplyEvent(body); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	if err = <-done; err == nil {
		t.Fatal("late renewal repopulated an invalidated cache")
	}
	_, member := f.exchange(t, "/api/nox/authorizations", f.body())
	if member.Claims()["scope"] != "nox:inspect" {
		t.Fatal("demotion retained administrator scopes")
	}
}
func TestSuiteLocalIncompatibleUpdatePreservesMembershipAndDeniesIssuance(t *testing.T) {
	f := issuanceHTTPServer(t)
	f.current.Store(bytes.Replace(f.declaration, []byte(`"contractVersion":"1.0"`), []byte(`"contractVersion":"2.0"`), 1))
	body := map[string]string{"contractVersion": "1.0", "baseUrl": f.product.URL}
	path := "/api/nox/products/" + f.record.ProductID
	status, _, raw := f.call(t, "PUT", path, body, f.adminKey, map[string]string{"If-Match": `"` + f.record.Revision + `"`})
	suiteHTTPStatus(t, status, 422, raw)
	status, _, raw = f.call(t, "POST", "/api/nox/authorizations", f.body(), f.gateway.ServiceCredential, nil)
	suiteHTTPStatus(t, status, 422, raw)
	status, _, raw = f.call(t, "GET", path, nil, f.adminKey, nil)
	suiteHTTPStatus(t, status, 200, raw)
	record, err := noxmcp.LoadSuiteRegistrationRecord(raw)
	if err != nil || record.Revision != f.record.Revision {
		t.Fatal("incompatible update removed accepted membership", err)
	}
	f.current.Store(f.declaration)
	status, _, raw = f.call(t, "PUT", path, body, f.adminKey, map[string]string{"If-Match": `"` + f.record.Revision + `"`})
	suiteHTTPStatus(t, status, 200, raw)
	_, identity := f.exchange(t, "/api/nox/authorizations", f.body())
	if !strings.Contains(identity.Claims()["scope"].(string), "nox:inspect") {
		t.Fatal("verified update did not restore authorization")
	}
}
