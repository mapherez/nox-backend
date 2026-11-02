package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Only non-secret setup metadata is journaled here. Intent/provisioning secrets
// remain in the existing encrypted Suite vault and never enter browser views.
type SuiteConnectionAttempt struct {
	Service, ConfigurationRevision, ID, ActorID                string
	Origin, PeerOrigin, ProductID, DisplayName, ProductVersion string
	DeclarationFingerprint, TLSFingerprint                     string
	ExpectedTrustRevision, RegistrationRevision, PairingID     string
	State, ErrorCode, Message                                  string
	ExpiresAt                                                  int64
	Lease, UpdatedAt                                           string
	LeaseUntil                                                 int64
}

func (s *Store) SuiteConnectionAttempt(ctx context.Context, service string) (SuiteConnectionAttempt, error) {
	var a SuiteConnectionAttempt
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT record_json FROM suite_connection_attempts WHERE service=?`, service).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	err = suiteDecode(raw, &a)
	return a, err
}
func (s *Store) SuiteConnectionAttempts(ctx context.Context) ([]SuiteConnectionAttempt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT record_json FROM suite_connection_attempts ORDER BY service`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SuiteConnectionAttempt{}
	for rows.Next() {
		var raw string
		var a SuiteConnectionAttempt
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = suiteDecode(raw, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (x *suiteTx) connectionAttempt(service string) (SuiteConnectionAttempt, error) {
	var a SuiteConnectionAttempt
	var raw string
	err := x.tx.QueryRowContext(x.ctx, `SELECT record_json FROM suite_connection_attempts WHERE service=?`, service).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	err = suiteDecode(raw, &a)
	return a, err
}
func (x *suiteTx) putConnectionAttempt(a SuiteConnectionAttempt) error {
	a.UpdatedAt = suiteTime(x.now)
	raw, err := suiteJSON(a)
	if err != nil {
		return err
	}
	_, err = x.tx.ExecContext(x.ctx, `INSERT INTO suite_connection_attempts VALUES(?,?,?) ON CONFLICT(service) DO UPDATE SET attempt_id=excluded.attempt_id,record_json=excluded.record_json`, a.Service, a.ID, raw)
	return err
}
func (x *suiteTx) currentConnection(a SuiteConnectionAttempt) error {
	var revision, origin string
	err := x.tx.QueryRowContext(x.ctx, `SELECT revision,origin FROM foundation_connections WHERE service=?`, a.Service).Scan(&revision, &origin)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) || revision != a.ConfigurationRevision || origin != a.Origin {
		return suitePrecondition()
	}
	return err
}
func (x *suiteTx) connectionActor(caller SuiteCaller, a SuiteConnectionAttempt) error {
	actor, err := x.actor(caller)
	if err != nil {
		return err
	}
	if !actor.admin() || actor.user.ID != a.ActorID {
		return suiteDenied()
	}
	return x.currentConnection(a)
}
func (s *Store) SuitePrepareConnection(ctx context.Context, caller SuiteCaller, a SuiteConnectionAttempt) (SuiteConnectionAttempt, error) {
	var out SuiteConnectionAttempt
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		if err := x.connectionActor(caller, a); err != nil {
			return SuiteResult{}, err
		}
		current, err := x.connectionAttempt(a.Service)
		if err != nil {
			return SuiteResult{}, err
		}
		if current.ID != "" && current.ConfigurationRevision == a.ConfigurationRevision && (current.PairingID != "" && current.State != "cancelled" && current.State != "completed") {
			return SuiteResult{}, suiteError(409, "REGISTRATION_CONFLICT", "Resume or cancel the pending connection before starting another verification.")
		}
		a.ID, err = randomID("connection_setup_")
		if err != nil {
			return SuiteResult{}, err
		}
		_, a.ExpectedTrustRevision, err = x.head(a.ProductID)
		if err != nil {
			return SuiteResult{}, err
		}
		if rec, e := x.product(a.ProductID); e == nil {
			a.RegistrationRevision = rec.Revision
		} else {
			var failure *SuiteError
			if !errors.As(e, &failure) || failure.Code != "PRODUCT_NOT_REGISTERED" {
				return SuiteResult{}, e
			}
		}
		a.State = "verified"
		a.ExpiresAt = x.now.Add(10 * time.Minute).UnixMilli()
		a.UpdatedAt = suiteTime(x.now)
		out = a
		return SuiteResult{}, x.putConnectionAttempt(a)
	})
	return out, err
}
func (s *Store) SuiteClaimConnection(ctx context.Context, caller SuiteCaller, service, id string, cancelOtherAdmin bool) (SuiteConnectionAttempt, error) {
	var out SuiteConnectionAttempt
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		a, err := x.connectionAttempt(service)
		if err != nil {
			return SuiteResult{}, err
		}
		if a.ID == "" || a.ID != id {
			return SuiteResult{}, suitePrecondition()
		}
		actor, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if !actor.admin() {
			return SuiteResult{}, suiteDenied()
		}
		if actor.user.ID != a.ActorID && !cancelOtherAdmin {
			if a.PairingID == "" {
				return SuiteResult{}, suiteDenied()
			}
			p, err := x.pairing(a.PairingID)
			if err != nil {
				return SuiteResult{}, err
			}
			if p.State != "acknowledged" {
				return SuiteResult{}, suiteDenied()
			}
		}
		a.ActorID = actor.user.ID
		if err = x.currentConnection(a); err != nil {
			return SuiteResult{}, err
		}
		if cancelOtherAdmin {
			a.ActorID = actor.user.ID
		}
		if a.State == "cancelled" {
			return SuiteResult{}, suiteRecoveryClosed()
		}
		if a.LeaseUntil > x.now.UnixMilli() {
			return SuiteResult{}, suiteError(409, "REGISTRATION_CONFLICT", "Connection work is already in progress. Refresh its state before retrying.")
		}
		if a.PairingID == "" && x.deadlineExpired(a.ExpiresAt) {
			return SuiteResult{}, suiteError(409, "PAIRING_RECOVERY_CLOSED", "Verification expired. Verify the address again.")
		}
		a.Lease, err = randomID("connection_lease_")
		if err != nil {
			return SuiteResult{}, err
		}
		a.LeaseUntil = x.now.Add(30 * time.Second).UnixMilli()
		out = a
		return SuiteResult{}, x.putConnectionAttempt(a)
	})
	return out, err
}
func (s *Store) SuiteUpdateConnection(ctx context.Context, caller SuiteCaller, a SuiteConnectionAttempt, release bool) error {
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		if err := x.connectionActor(caller, a); err != nil {
			return SuiteResult{}, err
		}
		current, err := x.connectionAttempt(a.Service)
		if err != nil {
			return SuiteResult{}, err
		}
		if current.ID != a.ID || a.Lease == "" || current.Lease != a.Lease || current.LeaseUntil <= x.now.UnixMilli() {
			return SuiteResult{}, suitePrecondition()
		}
		if release {
			a.Lease = ""
			a.LeaseUntil = 0
		}
		return SuiteResult{}, x.putConnectionAttempt(a)
	})
	return err
}

type suiteConnectionGuardKey struct{}

// Internal transaction guard; callers cannot manufacture it through HTTP JSON.
func SuiteConnectionContext(ctx context.Context, a SuiteConnectionAttempt) context.Context {
	return context.WithValue(ctx, suiteConnectionGuardKey{}, a)
}
func (x *suiteTx) checkConnectionGuard() error {
	if a, ok := x.ctx.Value(suiteConnectionGuardKey{}).(SuiteConnectionAttempt); ok {
		if err := x.currentConnection(a); err != nil {
			return err
		}
		current, err := x.connectionAttempt(a.Service)
		if err != nil {
			return err
		}
		if current.ID != a.ID || current.Lease != a.Lease || current.LeaseUntil <= x.now.UnixMilli() {
			return suitePrecondition()
		}
	}
	return nil
}
func (x *suiteTx) checkConnectionPairing(id string) error {
	rows, err := x.tx.QueryContext(x.ctx, `SELECT record_json FROM suite_connection_attempts`)
	if err != nil {
		return err
	}
	var found *SuiteConnectionAttempt
	for rows.Next() {
		var raw string
		var a SuiteConnectionAttempt
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if err = suiteDecode(raw, &a); err != nil {
			rows.Close()
			return err
		}
		if a.PairingID == id {
			found = &a
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if found == nil {
		return nil
	}
	if found.State == "cancelled" {
		return suiteRecoveryClosed()
	}
	return x.currentConnection(*found)
}
func (s *Store) SuiteConnectionPairing(ctx context.Context, caller SuiteCaller, a SuiteConnectionAttempt) (SuiteAdminPairing, error) {
	var out SuiteAdminPairing
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		if err := x.connectionActor(caller, a); err != nil {
			return SuiteResult{}, err
		}
		p, err := x.pairing(a.PairingID)
		if err != nil {
			return SuiteResult{}, err
		}
		if p.Request.ProductID != a.ProductID || p.Request.BaseUrl != a.PeerOrigin {
			return SuiteResult{}, suiteDenied()
		}
		out = SuiteAdminPairing{PairingID: p.ID, ProductID: p.Request.ProductID, BaseURL: p.Request.BaseUrl, Purpose: p.Request.Purpose, State: p.State, Revision: p.Revision, ExpiresAt: suiteTime(time.UnixMilli(p.ExpiresAt)), ServiceID: p.ServiceID}
		return SuiteResult{}, nil
	})
	return out, err
}

// Associate setup and pairing in the same transaction, before any intent leaves
// Backend. A crash cannot leave an issued setup intent outside its config guard.
func (x *suiteTx) bindConnectionPairing(id string) error {
	a, ok := x.ctx.Value(suiteConnectionGuardKey{}).(SuiteConnectionAttempt)
	if !ok {
		return nil
	}
	current, err := x.connectionAttempt(a.Service)
	if err != nil {
		return err
	}
	if err = x.checkConnectionGuard(); err != nil {
		return err
	}
	current.PairingID = id
	current.State = "connecting"
	return x.putConnectionAttempt(current)
}

// An acknowledged setup may be resumed by another current administrator. A
// stale attempt cannot register a replacement association created elsewhere.
func (s *Store) SuiteConnectionRegistered(ctx context.Context, caller SuiteCaller, a SuiteConnectionAttempt) (bool, error) {
	registered := false
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		if err := x.connectionActor(caller, a); err != nil {
			return SuiteResult{}, err
		}
		p, err := x.pairing(a.PairingID)
		if err != nil {
			return SuiteResult{}, err
		}
		head, _, err := x.head(a.ProductID)
		if err != nil {
			return SuiteResult{}, err
		}
		if p.State != "acknowledged" || head != p.ServiceID {
			return SuiteResult{}, suiteConflict()
		}
		service, err := x.service(p.ServiceID)
		if err != nil {
			return SuiteResult{}, err
		}
		record, err := x.product(a.ProductID)
		if err != nil {
			var failure *SuiteError
			if errors.As(err, &failure) && failure.Code == "PRODUCT_NOT_REGISTERED" {
				return SuiteResult{}, nil
			}
			return SuiteResult{}, err
		}
		registered = record.AssociationID == service.AssociationID
		return SuiteResult{}, nil
	})
	return registered, err
}
