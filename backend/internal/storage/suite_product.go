package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

// SuiteBackendAdmission is a request-local snapshot, never a shared mutable
// verifier. All identity and policy fields are derived from current local trust.
type SuiteBackendAdmission struct {
	admittedAt   time.Time
	Identity     *noxmcp.VerifiedSuiteIdentity
	User         User
	Verification noxmcp.SuiteVerificationOptions
}

// SuiteAcceptBackendToken uses unverified claims only as lookup selectors. The
// released SDK verifies signature, profile, time and every target/source binding
// before any identity reaches domain operations. No HTTP request to self is needed.
func (s *Store) SuiteAcceptBackendToken(ctx context.Context, token, origin, path string) (SuiteBackendAdmission, error) {
	var out SuiteBackendAdmission
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		var err error
		out, err = x.acceptBackendToken(token, origin, path)
		return SuiteResult{}, err
	})
	return out, err
}

func (x *suiteTx) acceptBackendToken(token, origin, path string) (SuiteBackendAdmission, error) {
	return x.acceptBackendTokenAt(token, origin, path, x.now)
}

func (x *suiteTx) acceptBackendTokenAt(token, origin, path string, verificationTime time.Time) (SuiteBackendAdmission, error) {
	out := SuiteBackendAdmission{admittedAt: verificationTime}
	invalid := func() (SuiteBackendAdmission, error) { return out, suiteUnauthenticated() }
	if len(token) > noxmcp.SuiteMaxTokenBytes {
		return invalid()
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return invalid()
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return invalid()
	}
	value, err := noxmcp.ParseSuiteJSON(raw)
	if err != nil {
		return invalid()
	}
	claims, ok := value.(map[string]any)
	if !ok {
		return invalid()
	}
	gateway, _ := claims["sub"].(string)
	if act, ok := claims["act"].(map[string]any); ok {
		gateway, _ = act["sub"].(string)
	}
	bound, ok := claims["nox"].(map[string]any)
	if !ok || gateway == "" {
		return invalid()
	}
	revision, _ := bound["serviceCredentialRevision"].(string)
	service, err := x.service(gateway)
	if err != nil {
		return out, backendAdmissionError(err)
	}
	if service.State != "active" || service.Purpose != "gateway" {
		return invalid()
	}
	head, _, err := x.head(service.ProductID)
	if err != nil {
		return out, backendAdmissionError(err)
	}
	if head != service.ID {
		return invalid()
	}
	rows, err := x.tx.QueryContext(x.ctx, `SELECT record_json FROM suite_credentials WHERE service_id=? AND state IN ('active','overlap')`, gateway)
	if err != nil {
		return out, err
	}
	var credential *suiteCredential
	for rows.Next() {
		var encoded string
		var c suiteCredential
		if err = rows.Scan(&encoded); err == nil {
			err = suiteDecode(encoded, &c)
		}
		if err != nil {
			break
		}
		if c.Revision == revision && !x.deadlineExpired(c.NotAfter) {
			credential = &c
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if credential == nil {
		return invalid()
	}
	record, audience, err := x.delegationTarget("nox-backend", stringField(bound, "registrationRevision"))
	if err != nil {
		return out, backendAdmissionError(err)
	}
	expected, err := noxmcp.ResolveSuiteEndpoint(origin, path)
	if err != nil || audience != expected {
		return invalid()
	}
	keys, err := x.keySet()
	if err != nil {
		return out, err
	}
	trusted := map[string]noxmcp.SuiteSigningKey{}
	for _, key := range keys.Keys {
		if key.Kid != nil {
			trusted[*key.Kid] = key
		}
	}
	out.Verification = noxmcp.SuiteVerificationOptions{Issuer: keys.Issuer, Audience: expected, ProductID: "nox-backend", AssociationID: record.AssociationID, RegistrationRevision: record.Revision, GatewayServiceID: gateway, ServiceCredentialRevision: credential.Revision, Keys: trusted, Now: verificationTime}
	out.Identity, err = noxmcp.VerifySuiteToken(token, out.Verification)
	if err != nil {
		return invalid()
	}
	grant := "authorization.delegate"
	if out.Identity.Kind() == "probe" {
		grant = "mcp.probe"
	}
	if !(suiteActor{service: &service, credential: credential}).grant(grant) {
		return invalid()
	}
	if out.Identity.Kind() == "probe" {
		return out, nil
	}
	verified := out.Identity.Context()
	var keyID, authRevision string
	err = x.tx.QueryRowContext(x.ctx, `SELECT u.id,u.email,u.role,u.status,k.key_id,a.revision
 FROM users u JOIN api_keys p ON p.user_id=u.id
 JOIN suite_personal_keys k ON k.user_id=u.id AND k.key_hash=p.key_hash
 JOIN suite_user_authority a ON a.user_id=u.id WHERE u.id=? AND u.status='ACTIVE'`, verified["userId"]).Scan(&out.User.ID, &out.User.Email, &out.User.Role, &out.User.Status, &keyID, &authRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return invalid()
	}
	if err != nil {
		return out, err
	}
	if keyID != verified["keyId"] || authRevision != verified["authRevision"] {
		return invalid()
	}
	if !slices.Equal(strings.Fields(out.Identity.Claims()["scope"].(string)), suiteCapabilities(out.User.Role)) {
		return invalid()
	}
	return out, nil
}
func stringField(value map[string]any, key string) string { s, _ := value[key].(string); return s }
func backendAdmissionError(err error) error {
	var e *SuiteError
	if errors.As(err, &e) && e.Status != 503 {
		return suiteUnauthenticated()
	}
	return err
}

type suiteBackendContextKey struct{}
type suiteBackendContext struct {
	admission    SuiteBackendAdmission
	origin, path string
}

// WithSuiteBackendIdentity carries a locally admitted SDK-branded identity to shared storage
// operations. Mutations revalidate it inside their SQLite write transaction.
func WithSuiteBackendIdentity(ctx context.Context, admission SuiteBackendAdmission, origin, path string) context.Context {
	return context.WithValue(ctx, suiteBackendContextKey{}, suiteBackendContext{admission, origin, path})
}
func (x *suiteTx) checkBackendMutation(user string, binding suiteBackendContext) error {
	// A grant expiring after admission does not truncate an accepted operation.
	// Its context deadline still applies; current revocations/ownership are checked
	// in this write transaction, with token validity anchored to actual admission.
	if binding.admission.admittedAt.IsZero() {
		return ErrForbidden
	}
	admitted, err := x.acceptBackendTokenAt(binding.admission.Identity.AccessToken(), binding.origin, binding.path, binding.admission.admittedAt)
	if err != nil {
		return err
	}
	if admitted.Identity.Kind() != "user" || admitted.User.ID != user || !slices.Contains(suiteCapabilities(admitted.User.Role), "nox:write") {
		return ErrForbidden
	}
	return x.ctx.Err()
}
func (s *Store) vaultMutationTransaction(ctx context.Context, user string, fn func(*sql.Tx) error) error {
	if binding, ok := ctx.Value(suiteBackendContextKey{}).(suiteBackendContext); ok {
		_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
			if err := x.checkBackendMutation(user, binding); err != nil {
				return SuiteResult{}, err
			}
			if err := fn(x.tx); err != nil {
				return SuiteResult{}, err
			}
			return SuiteResult{}, ctx.Err()
		})
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = fn(tx); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) vaultMutationExec(ctx context.Context, user, query string, args ...any) (sql.Result, error) {
	if _, ok := ctx.Value(suiteBackendContextKey{}).(suiteBackendContext); !ok {
		return s.db.ExecContext(ctx, query, args...)
	}
	var result sql.Result
	err := s.vaultMutationTransaction(ctx, user, func(tx *sql.Tx) error { var err error; result, err = tx.ExecContext(ctx, query, args...); return err })
	return result, err
}
