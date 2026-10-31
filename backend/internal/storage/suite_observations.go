package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

// Configure at startup; atomic fields also keep concurrent reads race-free.
func (s *Store) ConfigureSuiteObservationTiming(report, freshness time.Duration) {
	if report == 0 {
		report = 15 * time.Second
	}
	if freshness == 0 {
		freshness = 45 * time.Second
	}
	s.suiteReportInterval.Store(int64(report))
	s.suiteFreshness.Store(int64(freshness))
}
func (s *Store) suiteObservationTiming() (time.Duration, time.Duration) {
	report, fresh := time.Duration(s.suiteReportInterval.Load()), time.Duration(s.suiteFreshness.Load())
	if report == 0 {
		report = 15 * time.Second
	}
	if fresh == 0 {
		fresh = 45 * time.Second
	}
	return report, fresh
}
func (x *suiteTx) observationEnvelopes(product string) ([]noxmcp.SuiteObservationEnvelope, error) {
	rows, err := x.tx.QueryContext(x.ctx, `SELECT envelope_json FROM suite_observations WHERE product_id=? ORDER BY source,producer_id`, product)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []noxmcp.SuiteObservationEnvelope{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v noxmcp.SuiteObservationEnvelope
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (x *suiteTx) observationAuthorization(a suiteActor, id, revision, association, producer, source string, managedBy *noxmcp.SuiteRegistrationRecordManagedBy) (noxmcp.SuiteRegistrationRecord, error) {
	var rec noxmcp.SuiteRegistrationRecord
	if a.service == nil || a.service.ID != producer {
		return rec, suiteDenied()
	}
	grant := "mcp.report"
	if source == "yard" {
		grant = "lifecycle.report"
	} else if source != "gateway" {
		return rec, suiteDenied()
	}
	if !a.grant(grant) {
		return rec, suiteDenied()
	}
	sid, _, err := x.head(a.service.ProductID)
	if err != nil {
		return rec, err
	}
	if sid != a.service.ID {
		return rec, suiteDenied()
	}
	rec, err = x.product(id)
	if err != nil {
		return rec, err
	}
	if rec.Revision != revision || rec.AssociationID != association {
		return rec, suiteError(409, "STALE_OBSERVATION", "Observation does not match the current registration and association.")
	}
	target, err := x.registrationTrust(id, noxmcp.SuiteRegistrationRequest{ContractVersion: "1.0", BaseUrl: rec.Connection.BaseUrl, ManagedBy: rec.ManagedBy})
	if err != nil {
		return rec, err
	}
	if target.AssociationID != association {
		return rec, suiteConflict()
	}
	if source == "yard" {
		if rec.ManagedBy == nil || rec.ManagedBy.ProductID != a.service.ProductID || (managedBy != nil && !suiteSame(rec.ManagedBy, managedBy)) {
			return rec, suiteDenied()
		}
		if err = x.deploymentAllowed(*a.service, noxmcp.SuitePairingRequest{ContractVersion: "1.0", ProductID: id, Purpose: "product", BaseUrl: rec.Connection.BaseUrl, ManagedBy: rec.ManagedBy}); err != nil {
			return rec, err
		}
	}
	return rec, nil
}
func (s *Store) SuiteCreateObservationEpoch(ctx context.Context, caller SuiteCaller, req noxmcp.SuiteObservationEpochRequest, key, digest string) (SuiteResult, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return SuiteResult{}, err
	}
	req, err = noxmcp.LoadSuiteObservationEpochRequest(raw)
	if err != nil {
		return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "Invalid observation epoch request.")
	}
	authorize := func(x *suiteTx, a suiteActor) error {
		_, err := x.observationAuthorization(a, req.ProductID, req.RegistrationRevision, req.AssociationID, req.ProducerID, req.Source, nil)
		return err
	}
	return s.suiteMutation(ctx, caller, "observations.epoch:"+req.ProductID, key, digest, authorize, func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		epoch, err := randomID("epoch_")
		if err != nil {
			return SuiteResult{}, err
		}
		_, err = x.tx.ExecContext(ctx, `INSERT INTO suite_observation_epochs VALUES(?,?,?,?,?) ON CONFLICT(product_id,association_id,producer_id,source) DO UPDATE SET epoch=excluded.epoch`, req.ProductID, req.AssociationID, req.ProducerID, req.Source, epoch)
		if err != nil {
			return SuiteResult{}, err
		}
		if _, err = x.tx.ExecContext(ctx, `DELETE FROM suite_observations WHERE product_id=? AND association_id=? AND producer_id=? AND source=?`, req.ProductID, req.AssociationID, req.ProducerID, req.Source); err != nil {
			return SuiteResult{}, err
		}
		out := noxmcp.SuiteObservationEpoch{ContractVersion: "1.0", ProductID: req.ProductID, AssociationID: req.AssociationID, ProducerID: req.ProducerID, Source: req.Source, ProducerEpoch: epoch}
		if err = x.event("producer.reassociated", out); err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(201, out)
	})
}
func (s *Store) SuiteSubmitObservation(ctx context.Context, caller SuiteCaller, input []byte, key, digest string) (SuiteResult, error) {
	obs, err := noxmcp.LoadSuiteObservation(input)
	if err != nil {
		return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "Invalid observation document.")
	}
	authorize := func(x *suiteTx, a suiteActor) error {
		_, err := x.observationAuthorization(a, obs.ProductID, obs.RegistrationRevision, obs.AssociationID, obs.ProducerID, obs.Source, obs.ManagedBy)
		return err
	}
	return s.suiteMutation(ctx, caller, "observations.submit:"+obs.ProductID, key, digest, authorize, func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		var epoch string
		err := x.tx.QueryRowContext(ctx, `SELECT epoch FROM suite_observation_epochs WHERE product_id=? AND association_id=? AND producer_id=? AND source=?`, obs.ProductID, obs.AssociationID, obs.ProducerID, obs.Source).Scan(&epoch)
		if errors.Is(err, sql.ErrNoRows) || err == nil && epoch != obs.ProducerEpoch {
			return SuiteResult{}, suiteError(409, "STALE_OBSERVATION", "Obtain and persist a current producer epoch before reporting.")
		}
		if err != nil {
			return SuiteResult{}, err
		}
		var previous *noxmcp.SuiteObservationEnvelope
		var raw string
		err = x.tx.QueryRowContext(ctx, `SELECT envelope_json FROM suite_observations WHERE product_id=? AND association_id=? AND producer_id=? AND source=?`, obs.ProductID, obs.AssociationID, obs.ProducerID, obs.Source).Scan(&raw)
		if err == nil {
			previous = &noxmcp.SuiteObservationEnvelope{}
			if err = json.Unmarshal([]byte(raw), previous); err != nil {
				return SuiteResult{}, err
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return SuiteResult{}, err
		}
		rec, err := x.product(obs.ProductID)
		if err != nil {
			return SuiteResult{}, err
		}
		binding := noxmcp.SuiteObservationBinding{ProductID: obs.ProductID, RegistrationRevision: rec.Revision, AssociationID: rec.AssociationID, Source: obs.Source, ProducerID: obs.ProducerID, ProducerEpoch: epoch, ManagedBy: rec.ManagedBy}
		_, freshness := s.suiteObservationTiming()
		accepted, err := noxmcp.AcceptSuiteObservation(binding, input, x.now, previous, freshness)
		if err != nil {
			status := 400
			if err.Error() == "OBSERVATION_CONFLICT" || err.Error() == "STALE_OBSERVATION" {
				status = 409
			}
			return SuiteResult{}, suiteError(status, err.Error(), "The observation conflicts with the producer state or timestamp rules.")
		}
		if accepted.Accepted {
			body, err := json.Marshal(accepted.Envelope)
			if err != nil {
				return SuiteResult{}, err
			}
			if _, err = x.tx.ExecContext(ctx, `INSERT INTO suite_observations VALUES(?,?,?,?,?) ON CONFLICT(product_id,association_id,producer_id,source) DO UPDATE SET envelope_json=excluded.envelope_json`, obs.ProductID, obs.AssociationID, obs.ProducerID, obs.Source, string(body)); err != nil {
				return SuiteResult{}, err
			}
			if err = x.event("observation.updated", accepted.Envelope); err != nil {
				return SuiteResult{}, err
			}
		}
		return suiteResponse(200, accepted)
	})
}
