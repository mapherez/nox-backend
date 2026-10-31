package storage

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

type SuiteKeySet struct {
	ContractVersion string                   `json:"contractVersion"`
	Issuer          string                   `json:"issuer"`
	Revision        string                   `json:"revision"`
	Keys            []noxmcp.SuiteSigningKey `json:"keys"`
}

func (x *suiteTx) newSigningKey(state string) (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	id, err := randomID("key_")
	if err != nil {
		return "", err
	}
	alg, use := "ES256", "sig"
	public := noxmcp.SuiteSigningKey{Kty: "EC", Crv: "P-256", X: base64.RawURLEncoding.EncodeToString(key.X.FillBytes(make([]byte, 32))), Y: base64.RawURLEncoding.EncodeToString(key.Y.FillBytes(make([]byte, 32))), Kid: &id, Alg: &alg, Use: &use}
	body, err := suiteJSON(public)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	handle, err := x.secret(der, time.Time{})
	if err != nil {
		return "", err
	}
	_, err = x.tx.ExecContext(x.ctx, `INSERT INTO suite_signing_keys VALUES (?,?,?,?,0,?)`, id, state, body, handle, x.now.UnixMilli())
	return id, err
}
func (x *suiteTx) changedKeys(revoked []string) (string, error) {
	revision, err := randomID("keys_")
	if err != nil {
		return "", err
	}
	if _, err := x.tx.ExecContext(x.ctx, `UPDATE suite_authority SET revision=? WHERE singleton=1`, revision); err != nil {
		return "", err
	}
	if revoked == nil {
		revoked = []string{}
	}
	return revision, x.event("signing-keys.changed", map[string]any{"revision": revision, "revokedKeyIds": revoked})
}
func (x *suiteTx) keySet() (SuiteKeySet, error) {
	out := SuiteKeySet{ContractVersion: "1.0", Keys: []noxmcp.SuiteSigningKey{}}
	if err := x.tx.QueryRowContext(x.ctx, `SELECT issuer,revision FROM suite_authority WHERE singleton=1`).Scan(&out.Issuer, &out.Revision); err != nil {
		return out, err
	}
	rows, err := x.tx.QueryContext(x.ctx, `SELECT public_json FROM suite_signing_keys WHERE state IN ('active','prepared') OR (state='retiring' AND retire_at>?) ORDER BY created_at,id`, x.now.UnixMilli())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var key noxmcp.SuiteSigningKey
		if err := rows.Scan(&raw); err != nil {
			return out, err
		}
		if err := suiteDecode(raw, &key); err != nil {
			return out, err
		}
		out.Keys = append(out.Keys, key)
	}
	return out, rows.Err()
}
func (s *Store) SuiteKeys(ctx context.Context) (SuiteKeySet, error) {
	var keys SuiteKeySet
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		var err error
		keys, err = x.keySet()
		return SuiteResult{}, err
	})
	return keys, err
}
func (x *suiteTx) checkKeyRevision(expected string) error {
	var revision string
	if err := x.tx.QueryRowContext(x.ctx, `SELECT revision FROM suite_authority WHERE singleton=1`).Scan(&revision); err != nil {
		return err
	}
	if expected != revision {
		return suitePrecondition()
	}
	return nil
}
func (s *Store) SuitePrepareSigningKey(ctx context.Context, c SuiteCaller, expected, key, digest string) (SuiteResult, error) {
	return s.suiteMutation(ctx, c, "keys.prepare", key, digest, suiteAdmin, func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		if err := x.checkKeyRevision(expected); err != nil {
			return SuiteResult{}, err
		}
		var pending int
		if err := x.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM suite_signing_keys WHERE state='prepared'`).Scan(&pending); err != nil {
			return SuiteResult{}, err
		}
		if pending != 0 {
			return SuiteResult{}, suiteConflict()
		}
		id, err := x.newSigningKey("prepared")
		if err != nil {
			return SuiteResult{}, err
		}
		revision, err := x.changedKeys(nil)
		if err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(201, map[string]any{"contractVersion": "1.0", "keyId": id, "state": "prepared", "revision": revision})
	})
}
func (s *Store) SuiteActivateSigningKey(ctx context.Context, c SuiteCaller, id, expected, key, digest string) (SuiteResult, error) {
	return s.suiteMutation(ctx, c, "keys.activate:"+id, key, digest, suiteAdmin, func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		if err := x.checkKeyRevision(expected); err != nil {
			return SuiteResult{}, err
		}
		var state string
		err := x.tx.QueryRowContext(ctx, `SELECT state FROM suite_signing_keys WHERE id=?`, id).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return SuiteResult{}, suiteError(404, "SIGNING_KEY_NOT_FOUND", "Signing key not found.")
		}
		if err != nil {
			return SuiteResult{}, err
		}
		if state != "prepared" {
			return SuiteResult{}, suiteConflict()
		}
		var old, handle string
		if err := x.tx.QueryRowContext(ctx, `SELECT active_key FROM suite_authority WHERE singleton=1`).Scan(&old); err != nil {
			return SuiteResult{}, err
		}
		if old != "" {
			if err := x.tx.QueryRowContext(ctx, `SELECT secret_handle FROM suite_signing_keys WHERE id=?`, old).Scan(&handle); err != nil {
				return SuiteResult{}, err
			}
			if _, err := x.tx.ExecContext(ctx, `UPDATE suite_signing_keys SET state='retiring',secret_handle='',retire_at=? WHERE id=?`, x.now.Add(5*time.Minute).UnixMilli(), old); err != nil {
				return SuiteResult{}, err
			}
			if err := x.eraseSecret(handle); err != nil {
				return SuiteResult{}, err
			}
		}
		if _, err := x.tx.ExecContext(ctx, `UPDATE suite_signing_keys SET state='active' WHERE id=?`, id); err != nil {
			return SuiteResult{}, err
		}
		if _, err := x.tx.ExecContext(ctx, `UPDATE suite_authority SET active_key=? WHERE singleton=1`, id); err != nil {
			return SuiteResult{}, err
		}
		revision, err := x.changedKeys(nil)
		if err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(200, map[string]any{"contractVersion": "1.0", "keyId": id, "state": "active", "revision": revision})
	})
}
func (s *Store) SuiteRevokeSigningKey(ctx context.Context, c SuiteCaller, id, expected, key, digest string) (SuiteResult, error) {
	return s.suiteMutation(ctx, c, "keys.revoke:"+id, key, digest, suiteAdmin, func(x *suiteTx, _ suiteActor) (SuiteResult, error) {
		if err := x.checkKeyRevision(expected); err != nil {
			return SuiteResult{}, err
		}
		var handle, state string
		err := x.tx.QueryRowContext(ctx, `SELECT secret_handle,state FROM suite_signing_keys WHERE id=?`, id).Scan(&handle, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return SuiteResult{}, suiteError(404, "SIGNING_KEY_NOT_FOUND", "Signing key not found.")
		}
		if err != nil {
			return SuiteResult{}, err
		}
		if _, err := x.tx.ExecContext(ctx, `UPDATE suite_signing_keys SET state='revoked',secret_handle='' WHERE id=?`, id); err != nil {
			return SuiteResult{}, err
		}
		if _, err := x.tx.ExecContext(ctx, `UPDATE suite_authority SET active_key='' WHERE active_key=?`, id); err != nil {
			return SuiteResult{}, err
		}
		if err := x.eraseSecret(handle); err != nil {
			return SuiteResult{}, err
		}
		revision, err := x.changedKeys([]string{id})
		if err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(200, map[string]any{"contractVersion": "1.0", "keyId": id, "state": "revoked", "revision": revision})
	})
}

// Private key access stays in Backend storage/issuance; no management route returns it.
func (x *suiteTx) activeSigningKey() (*ecdsa.PrivateKey, string, error) {
	var id, handle string
	err := x.tx.QueryRowContext(x.ctx, `SELECT k.id,k.secret_handle FROM suite_authority a JOIN suite_signing_keys k ON k.id=a.active_key WHERE k.state='active'`).Scan(&id, &handle)
	if err != nil {
		return nil, "", err
	}
	der, err := x.readSecret(handle)
	if err != nil {
		return nil, "", err
	}
	key, err := x509.ParseECPrivateKey(der)
	return key, id, err
}
