package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

type suiteService struct {
	ID                     string                                   `json:"serviceId"`
	ProductID              string                                   `json:"productId"`
	AssociationID          string                                   `json:"associationId"`
	Purpose                string                                   `json:"purpose"`
	Origin                 string                                   `json:"baseUrl"`
	TLSFingerprint         string                                   `json:"tlsPublicKeyFingerprint"`
	DeclarationFingerprint string                                   `json:"declarationFingerprint"`
	Declaration            json.RawMessage                          `json:"declaration"`
	ManagedBy              *noxmcp.SuiteRegistrationRecordManagedBy `json:"managedBy,omitempty"`
	Grants                 []string                                 `json:"grants"`
	State                  string                                   `json:"state"`
	Revision               string                                   `json:"revision"`
	CurrentCredentialID    string                                   `json:"credentialId"`
}
type suiteCredential struct {
	ID, ServiceID, Revision, State           string
	NotAfter                                 int64
	PredecessorID, ResponseHandle, PairingID string
}

func (x *suiteTx) service(id string) (suiteService, error) {
	var value suiteService
	var raw string
	err := x.tx.QueryRowContext(x.ctx, `SELECT record_json FROM suite_services WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return value, suiteError(404, "SERVICE_NOT_FOUND", "Service not found.")
	}
	if err != nil {
		return value, err
	}
	err = suiteDecode(raw, &value)
	return value, err
}
func (x *suiteTx) credential(id string) (suiteCredential, error) {
	var value suiteCredential
	var raw string
	err := x.tx.QueryRowContext(x.ctx, `SELECT record_json FROM suite_credentials WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return value, suiteError(404, "CREDENTIAL_NOT_FOUND", "Credential not found.")
	}
	if err != nil {
		return value, err
	}
	err = suiteDecode(raw, &value)
	return value, err
}
func (x *suiteTx) putService(v suiteService) error {
	raw, err := suiteJSON(v)
	if err != nil {
		return err
	}
	_, err = x.tx.ExecContext(x.ctx, `INSERT INTO suite_services VALUES (?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,record_json=excluded.record_json`, v.ID, v.ProductID, v.State, raw)
	return err
}
func (x *suiteTx) putCredential(v suiteCredential) error {
	raw, err := suiteJSON(v)
	if err != nil {
		return err
	}
	_, err = x.tx.ExecContext(x.ctx, `UPDATE suite_credentials SET state=?,not_after=?,record_json=? WHERE id=?`, v.State, v.NotAfter, raw, v.ID)
	return err
}
func (x *suiteTx) newCredential(serviceID, state string, expiry int64) (suiteCredential, string, error) {
	id, err := randomID("credential_")
	if err != nil {
		return suiteCredential{}, "", err
	}
	revision, err := randomID("credential_revision_")
	if err != nil {
		return suiteCredential{}, "", err
	}
	secret, err := suiteRandom()
	if err != nil {
		return suiteCredential{}, "", err
	}
	token := "noxsvc_" + secret
	value := suiteCredential{ID: id, ServiceID: serviceID, Revision: revision, State: state, NotAfter: expiry}
	raw, err := suiteJSON(value)
	if err != nil {
		return value, "", err
	}
	_, err = x.tx.ExecContext(x.ctx, `INSERT INTO suite_credentials VALUES (?,?,?,?,?,?)`, id, serviceID, suiteHash(token), state, expiry, raw)
	return value, token, err
}
func (x *suiteTx) serviceToken(token string) (suiteService, suiteCredential, error) {
	if !strings.HasPrefix(token, "noxsvc_") || len(token) != 50 {
		return suiteService{}, suiteCredential{}, suiteUnauthenticated()
	}
	var id string
	err := x.tx.QueryRowContext(x.ctx, `SELECT id FROM suite_credentials WHERE verifier=?`, suiteHash(token)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return suiteService{}, suiteCredential{}, suiteUnauthenticated()
	}
	if err != nil {
		return suiteService{}, suiteCredential{}, err
	}
	credential, err := x.credential(id)
	if err != nil {
		return suiteService{}, credential, err
	}
	if credential.State == "revoked" || x.deadlineExpired(credential.NotAfter) {
		return suiteService{}, credential, suiteUnauthenticated()
	}
	service, err := x.service(credential.ServiceID)
	if err != nil {
		return service, credential, err
	}
	if service.State == "revoked" {
		return service, credential, suiteUnauthenticated()
	}
	return service, credential, nil
}
func (x *suiteTx) identity(s suiteService, c suiteCredential) noxmcp.SuiteServiceIdentity {
	v := noxmcp.SuiteServiceIdentity{ContractVersion: "1.0", ServiceID: s.ID, CredentialID: c.ID, CredentialRevision: c.Revision, ProductID: s.ProductID, AssociationID: s.AssociationID, Grants: s.Grants}
	if c.NotAfter != 0 {
		expiry := suiteTime(time.UnixMilli(c.NotAfter))
		v.ExpiresAt = &expiry
	}
	return v
}
func (x *suiteTx) revokeCredential(c suiteCredential) error {
	if c.State == "revoked" {
		return nil
	}
	c.State = "revoked"
	if err := x.eraseSecret(c.ResponseHandle); err != nil {
		return err
	}
	c.ResponseHandle = ""
	if err := x.putCredential(c); err != nil {
		return err
	}
	return x.event("service.invalidated", map[string]any{"serviceId": c.ServiceID, "credentialRevision": c.Revision})
}
func (x *suiteTx) revokeService(s suiteService) error {
	s.State = "revoked"
	if err := x.putService(s); err != nil {
		return err
	}
	rows, err := x.tx.QueryContext(x.ctx, `SELECT id FROM suite_credentials WHERE service_id=?`, s.ID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		credential, err := x.credential(id)
		if err != nil {
			return err
		}
		if err := x.revokeCredential(credential); err != nil {
			return err
		}
	}
	return x.event("service.invalidated", map[string]any{"serviceId": s.ID})
}
func suiteOwnService(id string) func(*suiteTx, suiteActor) error {
	return func(_ *suiteTx, a suiteActor) error {
		if a.admin() {
			return nil
		}
		if a.service == nil || a.service.ID != id || a.service.State != "active" || a.credential.State == "provisioned" {
			return suiteDenied()
		}
		return nil
	}
}
func (s *Store) SuiteRotateCredential(ctx context.Context, caller SuiteCaller, id, expected, key, digest string) (SuiteResult, error) {
	return s.suiteMutation(ctx, caller, "credentials.rotate:"+id, key, digest, suiteOwnService(id), func(x *suiteTx, a suiteActor) (SuiteResult, error) {
		service, err := x.service(id)
		if err != nil {
			return SuiteResult{}, err
		}
		if service.State != "active" {
			return SuiteResult{}, suiteConflict()
		}
		old, err := x.credential(service.CurrentCredentialID)
		if err != nil {
			return SuiteResult{}, err
		}
		if old.Revision != expected {
			return SuiteResult{}, suitePrecondition()
		}
		if old.State == "provisioned" && old.NotAfter > x.admissionMillis() {
			return SuiteResult{}, suiteConflict()
		}
		if !a.admin() && a.credential.ID != old.ID {
			return SuiteResult{}, suiteDenied()
		}
		expiry := x.now.Add(5 * time.Minute)
		credential, token, err := x.newCredential(id, "provisioned", expiry.UnixMilli())
		if err != nil {
			return SuiteResult{}, err
		}
		credential.PredecessorID = old.ID
		if old.State != "revoked" {
			old.State = "overlap"
			old.NotAfter = expiry.UnixMilli()
			if err := x.putCredential(old); err != nil {
				return SuiteResult{}, err
			}
		}
		result, err := x.secretResponse(201, map[string]any{"contractVersion": "1.0", "service": x.identity(service, credential), "serviceCredential": token, "acknowledgeBy": suiteTime(expiry)}, expiry)
		if err != nil {
			return result, err
		}
		credential.ResponseHandle = result.secretHandle
		if err := x.putCredential(credential); err != nil {
			return SuiteResult{}, err
		}
		service.CurrentCredentialID = credential.ID
		if err := x.putService(service); err != nil {
			return SuiteResult{}, err
		}
		return result, nil
	})
}
func (s *Store) SuiteAcknowledgeCredential(ctx context.Context, caller SuiteCaller, id, credentialID, key, digest string) (SuiteResult, error) {
	authorize := func(_ *suiteTx, a suiteActor) error {
		if a.service == nil || a.service.ID != id || a.credential.ID != credentialID {
			return suiteDenied()
		}
		return nil
	}
	return s.suiteMutation(ctx, caller, "credentials.ack:"+credentialID, key, digest, authorize, func(x *suiteTx, a suiteActor) (SuiteResult, error) {
		credential := *a.credential
		if a.service.State != "active" || a.service.CurrentCredentialID != credentialID || credential.PairingID != "" {
			return SuiteResult{}, suiteConflict()
		}
		if credential.State != "provisioned" {
			return SuiteResult{}, suiteConflict()
		}
		old, err := x.credential(credential.PredecessorID)
		if err != nil {
			return SuiteResult{}, err
		}
		if err := x.revokeCredential(old); err != nil {
			return SuiteResult{}, err
		}
		if err := x.eraseSecret(credential.ResponseHandle); err != nil {
			return SuiteResult{}, err
		}
		credential.ResponseHandle = ""
		credential.State = "active"
		credential.NotAfter = 0
		if err := x.putCredential(credential); err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(200, map[string]any{"acknowledged": true, "service": x.identity(*a.service, credential)})
	})
}
func (s *Store) SuiteRevokeCredential(ctx context.Context, caller SuiteCaller, id, credentialID, expected, key, digest string) (SuiteResult, error) {
	return s.suiteMutation(ctx, caller, "credentials.revoke:"+credentialID, key, digest, suiteOwnService(id), func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		credential, err := x.credential(credentialID)
		if err != nil {
			return SuiteResult{}, err
		}
		if credential.ServiceID != id {
			return SuiteResult{}, suiteDenied()
		}
		if credential.Revision != expected {
			return SuiteResult{}, suitePrecondition()
		}
		if err := x.revokeCredential(credential); err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(200, map[string]bool{"revoked": true})
	})
}
func (s *Store) SuiteReadServices(ctx context.Context, caller SuiteCaller, id string) (SuiteResult, error) {
	return s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		actor, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if id == "" && !actor.admin() {
			return SuiteResult{}, suiteDenied()
		}
		if id != "" && !actor.admin() && (actor.service == nil || actor.service.ID != id) {
			return SuiteResult{}, suiteDenied()
		}
		rows, err := x.tx.QueryContext(ctx, `SELECT id FROM suite_services WHERE (?='' OR id=?) ORDER BY id`, id, id)
		if err != nil {
			return SuiteResult{}, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return SuiteResult{}, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return SuiteResult{}, err
		}
		result := []map[string]any{}
		for _, id := range ids {
			service, err := x.service(id)
			if err != nil {
				return SuiteResult{}, err
			}
			credential, err := x.credential(service.CurrentCredentialID)
			if err != nil {
				return SuiteResult{}, err
			}
			result = append(result, map[string]any{"contractVersion": "1.0", "serviceId": service.ID, "productId": service.ProductID, "associationId": service.AssociationID, "baseUrl": service.Origin, "purpose": service.Purpose, "state": service.State, "revision": service.Revision, "service": x.identity(service, credential), "credentialState": credential.State})
		}
		if id != "" {
			if len(result) != 1 {
				return SuiteResult{}, suiteError(404, "SERVICE_NOT_FOUND", "Service not found.")
			}
			return suiteResponse(200, result[0])
		}
		return suiteResponse(200, result)
	})
}

func (s *Store) SuiteFoundationStatus(ctx context.Context, purpose, origin string) (string, error) {
	state := "not_paired"
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		rows, err := x.tx.QueryContext(ctx, `SELECT s.record_json FROM suite_trust_heads h JOIN suite_services s ON s.id=h.service_id WHERE s.state='active'`)
		if err != nil {
			return SuiteResult{}, err
		}
		services := []suiteService{}
		for rows.Next() {
			var raw string
			var service suiteService
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return SuiteResult{}, err
			}
			if err := suiteDecode(raw, &service); err != nil {
				rows.Close()
				return SuiteResult{}, err
			}
			services = append(services, service)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return SuiteResult{}, err
		}
		for _, service := range services {
			if service.Purpose == purpose && service.Origin == origin {
				var count int
				if err := x.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM suite_credentials WHERE service_id=? AND state IN ('active','overlap') AND (not_after=0 OR not_after>?)`, service.ID, x.admissionMillis()).Scan(&count); err != nil {
					return SuiteResult{}, err
				}
				if count > 0 {
					state = "paired"
					var registered int
					if err := x.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM suite_products WHERE product_id=? AND association_id=?`, service.ProductID, service.AssociationID).Scan(&registered); err != nil {
						return SuiteResult{}, err
					}
					if registered > 0 {
						state = "registered"
					}
				} else {
					state = "recovery_required"
				}
				return SuiteResult{}, nil
			}
		}
		rows, err = x.tx.QueryContext(ctx, `SELECT record_json FROM suite_pairings WHERE state IN ('pending','provisioned') AND expires_at>?`, x.admissionMillis())
		if err != nil {
			return SuiteResult{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var p suitePairing
			if err := rows.Scan(&raw); err != nil {
				return SuiteResult{}, err
			}
			if err := suiteDecode(raw, &p); err != nil {
				return SuiteResult{}, err
			}
			if p.Request.Purpose == purpose && p.Request.BaseUrl == origin {
				state = "pairing_pending"
			}
		}
		return SuiteResult{}, rows.Err()
	})
	return state, err
}

// Authentication alone confers no grant; every adapter still applies operation
// authorization and mutations recheck this same generation inside their commit.
func (s *Store) SuiteAuthenticateService(ctx context.Context, token string) (noxmcp.SuiteServiceIdentity, error) {
	var identity noxmcp.SuiteServiceIdentity
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		service, credential, err := x.serviceToken(token)
		if err != nil {
			return SuiteResult{}, err
		}
		identity = x.identity(service, credential)
		return SuiteResult{}, nil
	})
	return identity, err
}

func (s *Store) SuiteRevokeService(ctx context.Context, caller SuiteCaller, id, expected, key, digest string) (SuiteResult, error) {
	return s.suiteMutation(ctx, caller, "services.revoke:"+id, key, digest, suiteOwnService(id), func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		service, err := x.service(id)
		if err != nil {
			return SuiteResult{}, err
		}
		if service.Revision != expected {
			return SuiteResult{}, suitePrecondition()
		}
		if service.State == "provisioned" {
			return SuiteResult{}, suiteConflict()
		}
		if err := x.revokeService(service); err != nil {
			return SuiteResult{}, err
		}
		service.State = "revoked"
		service.Revision, err = randomID("association_revision_")
		if err != nil {
			return SuiteResult{}, err
		}
		if err := x.putService(service); err != nil {
			return SuiteResult{}, err
		}
		if _, err := x.tx.ExecContext(ctx, `UPDATE suite_trust_heads SET revision=? WHERE product_id=? AND service_id=?`, service.Revision, service.ProductID, service.ID); err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(200, map[string]any{"revoked": true, "revision": service.Revision})
	})
}
