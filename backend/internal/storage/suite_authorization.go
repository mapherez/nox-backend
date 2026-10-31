package storage

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

func suiteCapabilities(role string) []string {
	if role == UserRoleAdmin {
		return []string{"nox:inspect", "nox:write", "nox:sensitive-read"}
	}
	return []string{"nox:inspect"}
}
func (x *suiteTx) issuingGateway(caller SuiteCaller, grant string) (suiteActor, error) {
	a, err := x.actor(caller)
	if err != nil {
		return a, err
	}
	if a.service == nil || a.service.Purpose != "gateway" || !a.grant(grant) {
		return a, suiteDenied()
	}
	id, _, err := x.head(a.service.ProductID)
	if err != nil {
		return a, err
	}
	if id != a.service.ID {
		return a, suiteDenied()
	}
	return a, nil
}
func (x *suiteTx) delegationTarget(product, revision string) (noxmcp.SuiteRegistrationRecord, string, error) {
	record, err := x.product(product)
	if err != nil {
		return record, "", err
	}
	if record.Revision != revision {
		return record, "", suiteError(409, "REGISTRATION_CONFLICT", "Reconcile the current product registration before requesting authorization.")
	}
	var blocked int
	if err = x.tx.QueryRowContext(x.ctx, `SELECT COUNT(*) FROM suite_product_admission WHERE product_id=? AND revision=? AND association_id=? AND blocked=1`, product, record.Revision, record.AssociationID).Scan(&blocked); err != nil {
		return record, "", err
	}
	if blocked > 0 {
		return record, "", suiteError(422, "UNSUPPORTED_INTEGRATION", "The current product declaration failed verification. Complete an authorized verified update before requesting authorization.")
	}
	if record.Declaration.Capabilities.SuiteMcp == nil {
		return record, "", suiteError(422, "UNSUPPORTED_INTEGRATION", "This registration does not advertise an active Suite MCP endpoint.")
	}
	service, err := x.registrationTrust(product, noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: record.Connection.BaseUrl, ManagedBy: record.ManagedBy})
	if err != nil {
		return record, "", err
	}
	if service.AssociationID != record.AssociationID {
		return record, "", suiteConflict()
	}
	resource, err := noxmcp.ResolveSuiteEndpoint(record.Connection.BaseUrl, record.Declaration.Capabilities.SuiteMcp.Path)
	return record, resource, err
}
func (x *suiteTx) personalAuthority(submitted string) (user, keyID, revision, role string, err error) {
	// No coercion/trimming of a submitted credential into another cache partition.
	if !strings.HasPrefix(submitted, APIKeyPrefix) || len(submitted) > 4096 {
		return "", "", "", "", suiteUnauthenticated()
	}
	err = x.tx.QueryRowContext(x.ctx, `SELECT u.id,k.key_id,a.revision,u.role
 FROM api_keys p JOIN users u ON u.id=p.user_id
 JOIN suite_personal_keys k ON k.user_id=p.user_id AND k.key_hash=p.key_hash
 JOIN suite_user_authority a ON a.user_id=u.id
 WHERE p.key_hash=? AND u.status='ACTIVE'`, hashAPIKey(submitted)).Scan(&user, &keyID, &revision, &role)
	if errors.Is(err, sql.ErrNoRows) {
		err = suiteUnauthenticated()
	}
	return
}
func (x *suiteTx) grantTimes(a suiteActor) (uint64, uint64, error) {
	iat := x.now.Unix()
	expiry := iat + 300
	if a.credential.NotAfter != 0 && a.credential.NotAfter/1000 < expiry {
		expiry = a.credential.NotAfter / 1000
	}
	if iat < 0 || expiry*1000 <= x.admissionMillis() {
		return 0, 0, suiteUnauthenticated()
	}
	return uint64(iat), uint64(expiry), nil
}
func (x *suiteTx) signGrant(kind string, claims any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	schema, typ := "delegation-claims", "nox-delegation+jwt"
	if kind == "probe" {
		schema, typ = "probe-claims", "nox-service+jwt"
	}
	if _, err = noxmcp.LoadSuiteDocument(schema, payload); err != nil {
		return "", err
	}
	private, kid, err := x.activeSigningKey()
	if err != nil {
		return "", suiteError(503, "INTEGRATION_UNAVAILABLE", "No active Suite signing key is available. Restore or activate a prepared key.")
	}
	header, err := json.Marshal(map[string]string{"alg": "ES256", "typ": typ, "kid": kid})
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, private, digest[:])
	if err != nil {
		return "", err
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
func (s *Store) SuiteIssueAuthorization(ctx context.Context, caller SuiteCaller, request noxmcp.SuiteAuthorizationRequest) (SuiteResult, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return SuiteResult{}, err
	}
	request, err = noxmcp.LoadSuiteAuthorizationRequest(raw)
	if err != nil {
		return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "Invalid authorization request.")
	}
	return s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		a, err := x.issuingGateway(caller, "authorization.delegate")
		if err != nil {
			return SuiteResult{}, err
		}
		user, keyID, revision, role, err := x.personalAuthority(request.ApiKey)
		if err != nil {
			return SuiteResult{}, err
		}
		record, audience, err := x.delegationTarget(request.ProductID, request.RegistrationRevision)
		if err != nil {
			return SuiteResult{}, err
		}
		iat, exp, err := x.grantTimes(a)
		if err != nil {
			return SuiteResult{}, err
		}
		var issuer string
		if err = x.tx.QueryRowContext(ctx, `SELECT issuer FROM suite_authority WHERE singleton=1`).Scan(&issuer); err != nil {
			return SuiteResult{}, err
		}
		jti, err := randomID("delegation_")
		if err != nil {
			return SuiteResult{}, err
		}
		scopes := suiteCapabilities(role)
		claims := noxmcp.SuiteDelegationClaims{Iss: issuer, Aud: audience, Sub: user, Act: noxmcp.SuiteDelegationClaimsAct{Sub: a.service.ID}, Iat: iat, Nbf: iat, Exp: exp, Jti: jti,
			Nox: noxmcp.SuiteDelegationClaimsNox{ContractVersion: "1.0", KeyID: keyID, AuthRevision: revision, ServiceCredentialRevision: a.credential.Revision, ProductID: record.ProductID, RegistrationRevision: record.Revision, AssociationID: record.AssociationID}, Scope: strings.Join(scopes, " ")}
		token, err := x.signGrant("user", claims)
		if err != nil {
			return SuiteResult{}, err
		}
		out := noxmcp.SuiteAuthorizationResponse{ContractVersion: "1.0", TokenType: "Bearer", AccessToken: token, ExpiresAt: suiteTime(time.Unix(int64(exp), 0)),
			Context: noxmcp.SuiteAuthorizationContext{ContractVersion: "1.0", UserID: user, KeyID: keyID, AuthRevision: revision, GatewayServiceID: a.service.ID, ServiceCredentialRevision: a.credential.Revision, ProductID: record.ProductID, RegistrationRevision: record.Revision, AssociationID: record.AssociationID, Scopes: scopes}}
		return suiteResponse(200, out)
	})
}
func (s *Store) SuiteIssueProbe(ctx context.Context, caller SuiteCaller, request noxmcp.SuiteProbeRequest) (SuiteResult, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return SuiteResult{}, err
	}
	request, err = noxmcp.LoadSuiteProbeRequest(raw)
	if err != nil {
		return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "Invalid service authorization request.")
	}
	return s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		a, err := x.issuingGateway(caller, "mcp.probe")
		if err != nil {
			return SuiteResult{}, err
		}
		record, audience, err := x.delegationTarget(request.ProductID, request.RegistrationRevision)
		if err != nil {
			return SuiteResult{}, err
		}
		iat, exp, err := x.grantTimes(a)
		if err != nil {
			return SuiteResult{}, err
		}
		var issuer string
		if err = x.tx.QueryRowContext(ctx, `SELECT issuer FROM suite_authority WHERE singleton=1`).Scan(&issuer); err != nil {
			return SuiteResult{}, err
		}
		jti, err := randomID("probe_")
		if err != nil {
			return SuiteResult{}, err
		}
		claims := noxmcp.SuiteProbeClaims{Iss: issuer, Aud: audience, Sub: a.service.ID, Iat: iat, Nbf: iat, Exp: exp, Jti: jti, Scope: "nox:mcp-probe",
			Nox: noxmcp.SuiteProbeClaimsNox{ContractVersion: "1.0", ServiceCredentialRevision: a.credential.Revision, ProductID: record.ProductID, RegistrationRevision: record.Revision, AssociationID: record.AssociationID, Purpose: "mcp-probe"}}
		token, err := x.signGrant("probe", claims)
		if err != nil {
			return SuiteResult{}, err
		}
		out := noxmcp.SuiteProbeResponse{ContractVersion: "1.0", TokenType: "Bearer", AccessToken: token, ExpiresAt: suiteTime(time.Unix(int64(exp), 0)),
			Context: noxmcp.SuiteProbeContext{ContractVersion: "1.0", ServiceID: a.service.ID, ServiceCredentialRevision: a.credential.Revision, ProductID: record.ProductID, RegistrationRevision: record.Revision, AssociationID: record.AssociationID, Purpose: "mcp-probe"}}
		return suiteResponse(200, out)
	})
}

func (s *Store) SuiteCheckIssuer(ctx context.Context, caller SuiteCaller, grant string) error {
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		_, err := x.issuingGateway(caller, grant)
		return SuiteResult{}, err
	})
	return err
}

// A failed verified update does not delete or replace accepted membership. It
// records admission failure only for the exact still-current registration/head.
func (s *Store) SuiteBlockIncompatibleRegistration(ctx context.Context, caller SuiteCaller, id string, req noxmcp.SuiteRegistrationRequest, expected string, verified SuiteRegistrationVerification) error {
	var normalizeErr error
	req, normalizeErr = normalizeRegistryRequest(req)
	if normalizeErr != nil {
		return normalizeErr
	}
	if expected == "" {
		return nil
	} // A rejected create has no membership to invalidate.
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		a, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if err = x.registryAuthorization(a, id, req); err != nil {
			return SuiteResult{}, err
		}
		if _, err = x.registrationCondition(id, expected); err != nil {
			return SuiteResult{}, err
		}
		service, err := x.registrationTrust(id, req)
		if err != nil {
			return SuiteResult{}, err
		}
		if service.ID != verified.ServiceID || service.Revision != verified.TrustRevision {
			return SuiteResult{}, suiteConflict()
		}
		record, err := x.product(id)
		if err != nil {
			return SuiteResult{}, err
		}
		var current int
		if err = x.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM suite_product_admission WHERE product_id=? AND revision=? AND association_id=? AND blocked=1`, id, record.Revision, record.AssociationID).Scan(&current); err != nil {
			return SuiteResult{}, err
		}
		if current > 0 {
			return SuiteResult{}, nil
		}
		if _, err = x.tx.ExecContext(ctx, `INSERT INTO suite_product_admission VALUES(?,?,?,1) ON CONFLICT(product_id) DO UPDATE SET revision=excluded.revision,association_id=excluded.association_id,blocked=1`, id, record.Revision, record.AssociationID); err != nil {
			return SuiteResult{}, err
		}
		return SuiteResult{}, x.event("authorization.invalidated", map[string]any{"productId": id, "associationId": record.AssociationID})
	})
	return err
}
