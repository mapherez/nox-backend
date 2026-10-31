package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

func TestSuiteBackendAcceptedOperationLifetimeAndCurrentRevocation(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	f.store.suiteClock = func() time.Time { return now }
	gateway := f.provision(t, "backend-local-gateway", "gateway", true)
	f.provision(t, "nox-backend", "product", true)
	raw := registryDeclaration(t, f, "nox-backend")
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["capabilities"] = map[string]any{"suiteMcp": map[string]string{"path": "/mcp/suite"}}
	record := registryPut(t, f, f.caller, "nox-backend", "", "register-backend", mustRegistryJSON(t, doc), nil)
	key, err := f.store.CurrentAPIKey(ctx, f.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.SuiteIssueAuthorization(ctx, SuiteCaller{ServiceToken: gateway.ServiceCredential}, noxmcp.SuiteAuthorizationRequest{ContractVersion: "1.0", ApiKey: key, ProductID: "nox-backend", RegistrationRevision: record.Revision})
	grant := trustDecode[noxmcp.SuiteAuthorizationResponse](t, result, err)
	now = now.Add(294 * time.Second)
	admitted, err := f.store.SuiteAcceptBackendToken(ctx, grant.AccessToken, record.Connection.BaseUrl, "/mcp/suite")
	if err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	guarded := WithSuiteBackendIdentity(bounded, admitted, record.Connection.BaseUrl, "/mcp/suite")
	now = now.Add(2 * time.Second)
	// Expired tokens cannot start another operation; an accepted operation follows
	// its own deadline while retaining current source/account/membership checks.
	_, err = f.store.SuiteAcceptBackendToken(ctx, grant.AccessToken, record.Connection.BaseUrl, "/mcp/suite")
	trustExpectedError(t, err, 401)
	if _, err = f.store.CreateVault(guarded, f.owner.ID, "accepted before expiry"); err != nil {
		t.Fatal("grant expiry truncated accepted operation", err)
	}
	if _, err = f.store.RotateAPIKey(ctx, f.owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateVault(guarded, f.owner.ID, "revoked after admission"); err == nil {
		t.Fatal("current revocation ignored after admission")
	}
	f.store.suiteClock = func() time.Time { return now.Add(-time.Hour) }
	_, err = f.store.SuiteAcceptBackendToken(ctx, grant.AccessToken, record.Connection.BaseUrl, "/mcp/suite")
	trustExpectedError(t, err, 503)
}
