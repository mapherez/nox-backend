package storage

import (
	"context"
)

func (x *suiteTx) ids(query string, args ...any) ([]string, error) {
	rows, err := x.tx.QueryContext(x.ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (x *suiteTx) maintain() error {
	ids, err := x.ids(`SELECT id FROM suite_pairings WHERE expires_at<=? AND state IN ('pending','provisioned')`, x.admissionMillis())
	if err != nil {
		return err
	}
	for _, id := range ids {
		p, err := x.pairing(id)
		if err != nil {
			return err
		}
		if p.State == "provisioned" {
			service, err := x.service(p.ServiceID)
			if err != nil {
				return err
			}
			if err := x.revokeService(service); err != nil {
				return err
			}
		}
		for _, h := range []string{p.SecretHandle, p.IntentHandle, p.ProvisioningHandle} {
			if err := x.eraseSecret(h); err != nil {
				return err
			}
		}
		p.State = "expired"
		p.SecretHandle = ""
		p.IntentHandle = ""
		p.ProvisioningHandle = ""
		p.Revision, err = randomID("pairing_revision_")
		if err != nil {
			return err
		}
		if err := x.putPairing(p); err != nil {
			return err
		}
	}
	ids, err = x.ids(`SELECT id FROM suite_credentials WHERE not_after>0 AND not_after<=? AND state<>'revoked'`, x.admissionMillis())
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
	ids, err = x.ids(`SELECT id FROM suite_signing_keys WHERE state='retiring' AND retire_at<=?`, x.now.UnixMilli())
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := x.tx.ExecContext(x.ctx, `UPDATE suite_signing_keys SET state='revoked' WHERE id=?`, id); err != nil {
			return err
		}
	}
	if len(ids) > 0 {
		if _, err := x.changedKeys(ids); err != nil {
			return err
		}
	}
	handles, err := x.ids(`SELECT handle FROM suite_secret_handles WHERE expires_at>0 AND expires_at<=?`, x.admissionMillis())
	if err != nil {
		return err
	}
	for _, h := range handles {
		if err := x.eraseSecret(h); err != nil {
			return err
		}
	}
	_, err = x.tx.ExecContext(x.ctx, `DELETE FROM suite_idempotency WHERE expires_at<=?`, x.now.UnixMilli())
	if err != nil {
		return err
	}
	return x.pruneEvents()
}

// Expiry is enforced at admission regardless of this housekeeping cadence.
func (s *Store) MaintainSuiteTrust(ctx context.Context) error {
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) { return SuiteResult{}, x.maintain() })
	return err
}
