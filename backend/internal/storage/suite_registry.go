package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

// Verification is an internal handoff to the HTTP adapter; no credential is sent
// to a product. Commit rechecks the current caller, trust head and precondition.
type SuiteRegistrationVerification struct {
	ServiceID, TrustRevision, Origin, TLSFingerprint string
	Replay                                           *SuiteResult
}

func (x *suiteTx) product(id string) (noxmcp.SuiteRegistrationRecord, error) {
	var v noxmcp.SuiteRegistrationRecord
	var raw string
	err := x.tx.QueryRowContext(x.ctx, `SELECT record_json FROM suite_products WHERE product_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, suiteError(404, "PRODUCT_NOT_REGISTERED", "Product is not registered.")
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}
func suiteSame(a, b any) bool {
	aa, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	av, err := noxmcp.ParseSuiteJSON(aa)
	if err != nil {
		return false
	}
	bv, err := noxmcp.ParseSuiteJSON(bb)
	if err != nil {
		return false
	}
	ac, err := noxmcp.Canonicalize(av)
	if err != nil {
		return false
	}
	bc, err := noxmcp.Canonicalize(bv)
	return err == nil && ac == bc
}
func (x *suiteTx) registryAuthorization(a suiteActor, id string, req noxmcp.SuiteRegistrationRequest) error {
	if !a.admin() && !a.grant("registry.manage-associated") {
		return suiteDenied()
	}
	return x.authorizePairing(a, noxmcp.SuitePairingRequest{ContractVersion: "1.0", ProductID: id, BaseUrl: req.BaseUrl, Purpose: "product", ManagedBy: req.ManagedBy})
}
func (x *suiteTx) registrationTrust(id string, req noxmcp.SuiteRegistrationRequest) (suiteService, error) {
	sid, _, err := x.head(id)
	if err != nil {
		return suiteService{}, err
	}
	if sid == "" {
		return suiteService{}, suiteConflict()
	}
	svc, err := x.service(sid)
	if err != nil {
		return svc, err
	}
	if svc.State != "active" || svc.Origin != req.BaseUrl || !suiteSame(svc.ManagedBy, req.ManagedBy) {
		return svc, suiteConflict()
	}
	// A credential-less/revoked participant cannot regain membership through a read.
	var live int
	if err = x.tx.QueryRowContext(x.ctx, `SELECT COUNT(*) FROM suite_credentials WHERE service_id=? AND state IN ('active','overlap') AND (not_after=0 OR not_after>?)`, svc.ID, x.admissionMillis()).Scan(&live); err != nil {
		return svc, err
	}
	if live == 0 {
		return svc, suiteConflict()
	}

	return svc, nil
}
func (x *suiteTx) registrationCondition(id, expected string) (bool, error) {
	var actual string
	err := x.tx.QueryRowContext(x.ctx, `SELECT revision FROM suite_products WHERE product_id=?`, id).Scan(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		if expected != "" {
			return false, suiteError(404, "PRODUCT_NOT_REGISTERED", "Product is not registered.")
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if actual != expected {
		return true, suitePrecondition()
	}
	return true, nil
}
func normalizeRegistryRequest(req noxmcp.SuiteRegistrationRequest) (noxmcp.SuiteRegistrationRequest, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return req, err
	}
	req, err = noxmcp.LoadSuiteRegistrationRequest(raw)
	if err != nil {
		return req, suiteError(400, "INVALID_REGISTRATION", "Invalid registration request.")
	}
	req.BaseUrl, err = noxmcp.NormalizeSuiteOrigin(req.BaseUrl)
	return req, err
}
func (s *Store) SuitePrepareRegistration(ctx context.Context, caller SuiteCaller, id string, req noxmcp.SuiteRegistrationRequest, expected, key, digest string) (SuiteRegistrationVerification, error) {
	var out SuiteRegistrationVerification
	req, err := normalizeRegistryRequest(req)
	if err != nil {
		return out, err
	}
	_, err = s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		a, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if err = x.registryAuthorization(a, id, req); err != nil {
			return SuiteResult{}, err
		}
		if result, ok, err := x.replay(a.principal, "products.put:"+id, key, digest); ok || err != nil {
			if ok {
				out.Replay = &result
			}
			return SuiteResult{}, err
		}
		if _, err = x.registrationCondition(id, expected); err != nil {
			return SuiteResult{}, err
		}
		svc, err := x.registrationTrust(id, req)
		if err != nil {
			return SuiteResult{}, err
		}
		out = SuiteRegistrationVerification{ServiceID: svc.ID, TrustRevision: svc.Revision, Origin: svc.Origin, TLSFingerprint: svc.TLSFingerprint}
		return SuiteResult{}, nil
	})
	return out, err
}
func (s *Store) SuiteCommitRegistration(ctx context.Context, caller SuiteCaller, id string, req noxmcp.SuiteRegistrationRequest, expected, key, digest string, verified SuiteRegistrationVerification, declaration []byte) (SuiteResult, error) {
	req, err := normalizeRegistryRequest(req)
	if err != nil {
		return SuiteResult{}, err
	}
	authorize := func(x *suiteTx, a suiteActor) error { return x.registryAuthorization(a, id, req) }
	return s.suiteMutation(ctx, caller, "products.put:"+id, key, digest, authorize, func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		exists, err := x.registrationCondition(id, expected)
		if err != nil {
			return SuiteResult{}, err
		}
		svc, err := x.registrationTrust(id, req)
		if err != nil {
			return SuiteResult{}, err
		}
		if svc.ID != verified.ServiceID || svc.Revision != verified.TrustRevision || svc.Origin != verified.Origin || svc.TLSFingerprint != verified.TLSFingerprint {
			return SuiteResult{}, suiteConflict()
		}
		decl, err := noxmcp.LoadSuiteProductDeclaration(declaration)
		if err != nil {
			return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "Invalid product declaration.")
		}
		if decl.ProductID != id {
			return SuiteResult{}, suiteConflict()
		}
		record := noxmcp.SuiteRegistrationRecord{ContractVersion: "1.0", ProductID: id, AssociationID: svc.AssociationID, Declaration: decl, Connection: noxmcp.SuiteRegistrationRecordConnection{BaseUrl: req.BaseUrl}, ManagedBy: req.ManagedBy}
		if len(req.Extra) > 0 {
			raw, _ := json.Marshal(req.Extra)
			record.Extra = map[string]json.RawMessage{"requestExtensions": raw}
		}
		// A successful fresh verification also repairs a previously blocked declaration.
		if _, err = x.tx.ExecContext(ctx, `UPDATE suite_product_admission SET blocked=0 WHERE product_id=?`, id); err != nil {
			return SuiteResult{}, err
		}
		status := 201
		if exists {
			status = 200
			old, err := x.product(id)
			if err != nil {
				return SuiteResult{}, err
			}
			record.Revision = old.Revision
			if suiteSame(old, record) {
				return suiteResponse(status, old)
			}
		}
		record.Revision, err = randomID("registration_")
		if err != nil {
			return SuiteResult{}, err
		}
		raw, err := json.Marshal(record)
		if err != nil {
			return SuiteResult{}, err
		}
		if _, err = noxmcp.LoadSuiteRegistrationRecord(raw); err != nil {
			return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "Accepted record exceeds contract limits.")
		}
		_, err = x.tx.ExecContext(ctx, `INSERT INTO suite_products VALUES(?,?,?,?) ON CONFLICT(product_id) DO UPDATE SET revision=excluded.revision,association_id=excluded.association_id,record_json=excluded.record_json`, id, record.Revision, record.AssociationID, string(raw))
		if err != nil {
			return SuiteResult{}, err
		}
		if err = x.clearObservations(id); err != nil {
			return SuiteResult{}, err
		}
		revision, err := x.changeRegistryRevision()
		if err != nil {
			return SuiteResult{}, err
		}
		if err = x.event("product.upserted", map[string]any{"record": record, "registryRevision": revision}); err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(status, record)
	})
}
func (x *suiteTx) changeRegistryRevision() (string, error) {
	revision, err := randomID("registry_")
	if err != nil {
		return "", err
	}
	_, err = x.tx.ExecContext(x.ctx, `UPDATE suite_registry_state SET revision=? WHERE singleton=1`, revision)
	return revision, err
}
func (x *suiteTx) clearObservations(id string) error {
	if _, err := x.tx.ExecContext(x.ctx, `DELETE FROM suite_observations WHERE product_id=?`, id); err != nil {
		return err
	}
	_, err := x.tx.ExecContext(x.ctx, `DELETE FROM suite_observation_epochs WHERE product_id=?`, id)
	return err
}
func (s *Store) SuiteRemoveProduct(ctx context.Context, caller SuiteCaller, id, expected, key, digest string) (SuiteResult, error) {
	authorize := func(x *suiteTx, a suiteActor) error {
		if a.admin() {
			return nil
		}
		if !a.grant("registry.manage-associated") {
			return suiteDenied()
		}
		// A removal replay remains scoped even after removal or a new registration.
		var raw string
		err := x.tx.QueryRowContext(ctx, `SELECT record_json FROM suite_product_tombstones WHERE revision=? AND product_id=? AND expires_at>?`, expected, id, x.now.UnixMilli()).Scan(&raw)
		var rec noxmcp.SuiteRegistrationRecord
		if errors.Is(err, sql.ErrNoRows) {
			rec, err = x.product(id)
		} else if err == nil {
			err = json.Unmarshal([]byte(raw), &rec)
		}
		if err != nil {
			return suiteDenied()
		}
		return x.registryAuthorization(a, id, noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: rec.Connection.BaseUrl, ManagedBy: rec.ManagedBy})
	}
	return s.suiteMutation(ctx, caller, "products.remove:"+id, key, digest, authorize, func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		if _, err := x.registrationCondition(id, expected); err != nil {
			return SuiteResult{}, err
		}
		rec, err := x.product(id)
		if err != nil {
			return SuiteResult{}, err
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			return SuiteResult{}, err
		}
		if _, err = x.tx.ExecContext(ctx, `INSERT INTO suite_product_tombstones VALUES(?,?,?,?)`, rec.Revision, id, string(raw), x.now.Add(24*time.Hour).UnixMilli()); err != nil {
			return SuiteResult{}, err
		}
		if _, err = x.tx.ExecContext(ctx, `DELETE FROM suite_products WHERE product_id=?`, id); err != nil {
			return SuiteResult{}, err
		}
		if err = x.clearObservations(id); err != nil {
			return SuiteResult{}, err
		}
		// Removal retires only the removed association, never a replacement already paired.
		sid, _, err := x.head(id)
		if err != nil {
			return SuiteResult{}, err
		}
		if sid != "" {
			svc, err := x.service(sid)
			if err != nil {
				return SuiteResult{}, err
			}
			if svc.AssociationID == rec.AssociationID {
				if err = x.revokeService(svc); err != nil {
					return SuiteResult{}, err
				}
				revision, err := randomID("association_revision_")
				if err != nil {
					return SuiteResult{}, err
				}
				svc.State = "revoked"
				svc.Revision = revision
				if err = x.putService(svc); err != nil {
					return SuiteResult{}, err
				}
				if _, err = x.tx.ExecContext(ctx, `UPDATE suite_trust_heads SET revision=? WHERE product_id=?`, revision, id); err != nil {
					return SuiteResult{}, err
				}
			}
		}
		revision, err := x.changeRegistryRevision()
		if err != nil {
			return SuiteResult{}, err
		}
		if err = x.event("product.removed", map[string]any{"productId": id, "retiredRevision": rec.Revision, "associationId": rec.AssociationID, "registryRevision": revision}); err != nil {
			return SuiteResult{}, err
		}
		return SuiteResult{Status: 204}, nil
	})
}

func registryPrivileged(a suiteActor) bool { return a.admin() || a.grant("registry.read") }
func registryReadable(a suiteActor, id string) bool {
	return a.user != nil || registryPrivileged(a) || (a.grant("events.read") && a.service.ProductID == id)
}
func (x *suiteTx) productView(rec noxmcp.SuiteRegistrationRecord) (map[string]any, error) {
	observations, err := x.observationEnvelopes(rec.ProductID)
	if err != nil {
		return nil, err
	}
	availability := map[string]string{"yard": "unknown", "gateway": "unknown"}
	for _, envelope := range observations {
		state, err := noxmcp.SuiteObservationState(envelope, x.now)
		if err != nil {
			return nil, err
		}
		availability[envelope.Observation.Source] = state
	}
	return map[string]any{"productId": rec.ProductID, "displayName": rec.Declaration.DisplayName, "productVersion": rec.Declaration.ProductVersion, "availability": availability}, nil
}
func (s *Store) SuiteReadRegistry(ctx context.Context, caller SuiteCaller, id string) (SuiteResult, error) {
	return s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		a, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if !registryReadable(a, id) && id != "" {
			return SuiteResult{}, suiteDenied()
		}
		if a.user == nil && !registryPrivileged(a) && !a.grant("events.read") {
			return SuiteResult{}, suiteDenied()
		}
		full := registryPrivileged(a) || (a.service != nil && a.grant("events.read"))
		if id != "" {
			rec, err := x.product(id)
			if err != nil {
				return SuiteResult{}, err
			}
			if full {
				result, err := suiteResponse(200, rec)
				result.ETag = rec.Revision
				return result, err
			}
			view, err := x.productView(rec)
			if err != nil {
				return SuiteResult{}, err
			}
			return suiteResponse(200, view)
		}
		rows, err := x.tx.QueryContext(ctx, `SELECT record_json FROM suite_products ORDER BY product_id`)
		if err != nil {
			return SuiteResult{}, err
		}
		records := []noxmcp.SuiteRegistrationRecord{}
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				rows.Close()
				return SuiteResult{}, err
			}
			var rec noxmcp.SuiteRegistrationRecord
			if err = json.Unmarshal([]byte(raw), &rec); err != nil {
				rows.Close()
				return SuiteResult{}, err
			}
			if registryReadable(a, rec.ProductID) {
				records = append(records, rec)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return SuiteResult{}, err
		}
		var revision string
		if err = x.tx.QueryRowContext(ctx, `SELECT revision FROM suite_registry_state WHERE singleton=1`).Scan(&revision); err != nil {
			return SuiteResult{}, err
		}
		if full {
			cursor, err := x.eventCursor(a, -1)
			if err != nil {
				return SuiteResult{}, err
			}
			observations := []noxmcp.SuiteObservationEnvelope{}
			for _, rec := range records {
				items, err := x.observationEnvelopes(rec.ProductID)
				if err != nil {
					return SuiteResult{}, err
				}
				observations = append(observations, items...)
			}
			report, fresh := s.suiteObservationTiming()
			policy, _ := json.Marshal(map[string]int64{"reportIntervalMs": report.Milliseconds(), "freshnessMs": fresh.Milliseconds()})
			return suiteResponse(200, noxmcp.SuiteRegistrySnapshot{ContractVersion: "1.0", RegistryRevision: revision, Cursor: cursor, Products: records, Observations: observations, Extra: map[string]json.RawMessage{"observationPolicy": policy}})
		}
		products := []map[string]any{}
		for _, rec := range records {
			view, err := x.productView(rec)
			if err != nil {
				return SuiteResult{}, err
			}
			products = append(products, view)
		}
		return suiteResponse(200, map[string]any{"contractVersion": "1.0", "registryRevision": revision, "products": products})
	})
}
