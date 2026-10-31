package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

type SuiteError struct {
	Status        int
	Code, Message string
	Retryable     bool
}

func (e *SuiteError) Error() string { return e.Code + ": " + e.Message }
func suiteError(status int, code, message string) error {
	return &SuiteError{status, code, message, status == 503}
}
func suiteConflict() error {
	return suiteError(409, "REGISTRATION_CONFLICT", "The operation conflicts with current trust state. Reconcile before retrying.")
}
func suitePrecondition() error {
	return suiteError(412, "REGISTRATION_PRECONDITION_FAILED", "Trust configuration changed. Read current state and reconcile.")
}
func suiteDenied() error {
	return suiteError(403, "FORBIDDEN", "This identity is not authorized for the requested trust operation.")
}
func suiteUnauthenticated() error {
	return suiteError(401, "UNAUTHENTICATED", "A current service credential or authorized administrator is required.")
}
func suiteRecoveryClosed() error {
	return suiteError(409, "PAIRING_CONFLICT", "Secret recovery has closed. Reconcile pairing or credential state; do not replay the mutation.")
}

// User IDs come only from an authenticated Backend adapter. A service token is
// always reauthenticated against local state inside each mutation transaction.
type SuiteCaller struct{ UserID, ServiceToken string }

func (SuiteCaller) String() string { return "SuiteCaller(credentials redacted)" }

type SuiteResult struct {
	Status       int
	Body         json.RawMessage
	secretHandle string
}

func (SuiteResult) String() string { return "SuiteResult(body redacted)" }

type suiteActor struct {
	principal  string
	user       *User
	service    *suiteService
	credential *suiteCredential
}

func (a suiteActor) admin() bool {
	return a.user != nil && a.user.Role == UserRoleAdmin && a.user.Status == UserStatusActive
}
func (a suiteActor) grant(grant string) bool {
	if a.service == nil || a.service.State != "active" || a.credential == nil || a.credential.State == "provisioned" {
		return false
	}
	for _, allowed := range a.service.Grants {
		if grant == allowed {
			return true
		}
	}
	return false
}

type suiteTx struct {
	store            *Store
	tx               *sql.Tx
	ctx              context.Context
	now              time.Time
	created, removed []string
}

func suiteRandom() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func suiteTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
func suiteHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func suiteJSON(value any) (string, error)       { b, err := json.Marshal(value); return string(b), err }
func suiteDecode(data string, target any) error { return json.Unmarshal([]byte(data), target) }
func (x *suiteTx) secret(data []byte, until time.Time) (string, error) {
	handle, err := randomID("secret_")
	if err != nil {
		return "", err
	}
	if err := x.store.suiteVault.put(handle, data); err != nil {
		return "", err
	}
	x.created = append(x.created, handle)
	expiry := int64(0)
	if !until.IsZero() {
		expiry = until.UnixMilli()
	}
	_, err = x.tx.ExecContext(x.ctx, `INSERT INTO suite_secret_handles VALUES (?,?)`, handle, expiry)
	return handle, err
}
func (x *suiteTx) readSecret(handle string) ([]byte, error) {
	var expiry int64
	if err := x.tx.QueryRowContext(x.ctx, `SELECT expires_at FROM suite_secret_handles WHERE handle=?`, handle).Scan(&expiry); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, suiteRecoveryClosed()
		}
		return nil, err
	}
	if x.deadlineExpired(expiry) {
		return nil, suiteRecoveryClosed()
	}
	data, err := x.store.suiteVault.get(handle)
	if err != nil {
		return nil, fmt.Errorf("read Suite secret store: %w", err)
	}
	return data, nil
}
func (x *suiteTx) eraseSecret(handle string) error {
	if handle == "" {
		return nil
	}
	if _, err := x.tx.ExecContext(x.ctx, `DELETE FROM suite_secret_handles WHERE handle=?`, handle); err != nil {
		return err
	}
	x.removed = append(x.removed, handle)
	return nil
}
func (s *Store) suiteTransaction(ctx context.Context, fn func(*suiteTx) (SuiteResult, error)) (SuiteResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SuiteResult{}, err
	}
	// Acquire SQLite's write reservation before creating any encrypted file. This
	// also serializes secret-file GC with another process opening this same store.
	if _, err := tx.ExecContext(ctx, `UPDATE suite_authority SET last_clock_ms=last_clock_ms WHERE singleton=1`); err != nil {
		_ = tx.Rollback()
		return SuiteResult{}, err
	}
	now := time.Now().UTC()
	if s.suiteClock != nil {
		now = s.suiteClock().UTC()
	} else if !s.suiteMonoStart.IsZero() {
		monotonicWall := s.suiteWallStart.Add(time.Since(s.suiteMonoStart))
		if now.Before(monotonicWall.Add(-5 * time.Second)) {
			_ = tx.Rollback()
			return SuiteResult{}, suiteError(503, "INTEGRATION_UNAVAILABLE", "Suite clock rollback detected. Restore synchronized time before continuing.")
		}
		if monotonicWall.After(now) {
			now = monotonicWall
		}
	}
	var last int64
	clockErr := tx.QueryRowContext(ctx, `SELECT last_clock_ms FROM suite_authority WHERE singleton=1`).Scan(&last)
	if clockErr != nil && !errors.Is(clockErr, sql.ErrNoRows) {
		_ = tx.Rollback()
		return SuiteResult{}, clockErr
	}
	if now.UnixMilli() < last-5000 {
		_ = tx.Rollback()
		return SuiteResult{}, suiteError(503, "INTEGRATION_UNAVAILABLE", "Suite clock rollback detected. Restore synchronized time before continuing.")
	}
	if last > now.UnixMilli() {
		now = time.UnixMilli(last)
	}
	x := &suiteTx{store: s, tx: tx, ctx: ctx, now: now}
	defer rollback(tx)
	result, err := fn(x)
	if err != nil {
		for _, file := range x.created {
			s.suiteVault.remove(file)
		}
		return SuiteResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE suite_authority SET last_clock_ms=MAX(last_clock_ms,?) WHERE singleton=1`, now.UnixMilli()); err != nil {
		for _, file := range x.created {
			s.suiteVault.remove(file)
		}
		return SuiteResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return SuiteResult{}, err
	}
	for _, file := range x.removed {
		s.suiteVault.remove(file)
	}
	return result, nil
}
func (x *suiteTx) actor(c SuiteCaller) (suiteActor, error) {
	if c.ServiceToken != "" {
		service, credential, err := x.serviceToken(c.ServiceToken)
		if err != nil {
			return suiteActor{}, err
		}
		return suiteActor{principal: "service:" + service.ID, service: &service, credential: &credential}, nil
	}
	if c.UserID != "" {
		var user User
		err := x.tx.QueryRowContext(x.ctx, userSelectSQL()+` WHERE users.id=?`, c.UserID).Scan(userScanDest(&user)...)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return suiteActor{}, suiteUnauthenticated()
			}
			return suiteActor{}, err
		}
		if user.Status != UserStatusActive {
			return suiteActor{}, suiteUnauthenticated()
		}
		return suiteActor{principal: "user:" + user.ID, user: &user}, nil
	}
	return suiteActor{}, suiteUnauthenticated()
}
func (x *suiteTx) event(kind string, data any) error {
	id, err := randomID("event_")
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"eventId": id, "type": kind, "occurredAt": suiteTime(x.now), "data": data})
	if err != nil {
		return err
	}
	if _, err := noxmcp.LoadSuiteEvent(body); err != nil {
		return err
	}
	_, err = x.tx.ExecContext(x.ctx, `INSERT INTO suite_trust_events(event_id,type,event_json,occurred_at) VALUES (?,?,?,?)`, id, kind, string(body), x.now.UnixMilli())
	return err
}
func (x *suiteTx) replay(principal, operation, key, digest string) (SuiteResult, bool, error) {
	if len(key) < 1 || len(key) > 128 || strings.IndexFunc(key, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return SuiteResult{}, false, suiteError(400, "INVALID_REGISTRATION", "A bounded printable Idempotency-Key is required.")
	}
	var savedHash, body, handle string
	var status int
	var expiry int64
	err := x.tx.QueryRowContext(x.ctx, `SELECT request_hash,status,response_json,secret_handle,expires_at FROM suite_idempotency WHERE principal=? AND operation=? AND key_hash=?`, principal, operation, suiteHash(key)).Scan(&savedHash, &status, &body, &handle, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return SuiteResult{}, false, nil
	}
	if err != nil {
		return SuiteResult{}, false, err
	}
	if expiry <= x.now.UnixMilli() {
		return SuiteResult{}, false, suiteConflict()
	}
	if savedHash != digest {
		return SuiteResult{}, false, suiteConflict()
	}
	result := SuiteResult{Status: status, Body: json.RawMessage(body), secretHandle: handle}
	if handle != "" {
		raw, err := x.readSecret(handle)
		if err != nil {
			return SuiteResult{}, false, err
		}
		result.Body = raw
	}
	return result, true, nil
}
func (x *suiteTx) remember(principal, operation, key, digest string, result SuiteResult) error {
	body := string(result.Body)
	if result.secretHandle != "" {
		body = "{}"
	}
	_, err := x.tx.ExecContext(x.ctx, `INSERT INTO suite_idempotency VALUES (?,?,?,?,?,?,?,?)`, principal, operation, suiteHash(key), digest, result.Status, body, result.secretHandle, x.now.Add(24*time.Hour).UnixMilli())
	return err
}
func (s *Store) suiteMutation(ctx context.Context, caller SuiteCaller, operation, key, digest string, authorize func(*suiteTx, suiteActor) error, fn func(*suiteTx, suiteActor) (SuiteResult, error)) (SuiteResult, error) {
	return s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		if err := x.maintain(); err != nil {
			return SuiteResult{}, err
		}
		actor, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if err := authorize(x, actor); err != nil {
			return SuiteResult{}, err
		}
		if result, ok, err := x.replay(actor.principal, operation, key, digest); ok || err != nil {
			return result, err
		}
		result, err := fn(x, actor)
		if err != nil {
			return SuiteResult{}, err
		}
		return result, x.remember(actor.principal, operation, key, digest, result)
	})
}
func suiteAdmin(_ *suiteTx, a suiteActor) error {
	if !a.admin() {
		return suiteDenied()
	}
	return nil
}
func suiteResponse(status int, body any) (SuiteResult, error) {
	raw, err := json.Marshal(body)
	return SuiteResult{Status: status, Body: raw}, err
}
func (x *suiteTx) secretResponse(status int, body any, expiry time.Time) (SuiteResult, error) {
	result, err := suiteResponse(status, body)
	if err != nil {
		return result, err
	}
	handle, err := x.secret(result.Body, expiry)
	result.secretHandle = handle
	return result, err
}

func (s *Store) initializeSuiteTrust(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM suite_authority`).Scan(&count); err != nil {
		return err
	}
	vault, err := openSuiteSecretVault(s.dataDir, count > 0)
	if err != nil {
		return err
	}
	s.suiteVault = vault
	_, err = s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		if count == 0 {
			issuer, err := randomID("urn:nox:backend:")
			if err != nil {
				return SuiteResult{}, err
			}
			revision, err := randomID("keys_")
			if err != nil {
				return SuiteResult{}, err
			}
			if _, err := x.tx.ExecContext(ctx, `INSERT INTO suite_authority VALUES (1,?,'',?,?)`, issuer, revision, x.now.UnixMilli()); err != nil {
				return SuiteResult{}, err
			}
			key, err := x.newSigningKey("active")
			if err != nil {
				return SuiteResult{}, err
			}
			if _, err := x.tx.ExecContext(ctx, `UPDATE suite_authority SET active_key=? WHERE singleton=1`, key); err != nil {
				return SuiteResult{}, err
			}
		}
		return suiteResponse(200, map[string]bool{"ok": true})
	})
	if err != nil {
		return err
	}

	// Validate references and collect file orphans under the same write reservation
	// used by secret creators, so an in-flight file can never be collected as orphan.
	_, err = s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		rows, err := x.tx.QueryContext(ctx, `SELECT handle,expires_at FROM suite_secret_handles`)
		if err != nil {
			return SuiteResult{}, err
		}
		references := map[string]bool{}
		for rows.Next() {
			var h string
			var expiry int64
			if err := rows.Scan(&h, &expiry); err != nil {
				rows.Close()
				return SuiteResult{}, err
			}
			references[h] = true
			if expiry == 0 || expiry > x.now.UnixMilli() {
				if _, err := vault.get(h); err != nil {
					rows.Close()
					return SuiteResult{}, fmt.Errorf("Suite trust secret is unavailable; restore complete backup: %w", err)
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return SuiteResult{}, err
		}
		entries, err := os.ReadDir(vault.dir)
		if err != nil {
			return SuiteResult{}, err
		}
		for _, entry := range entries {
			if (suiteSecretName.MatchString(entry.Name()) && !references[entry.Name()]) || strings.HasPrefix(entry.Name(), ".pending-") {
				if err := os.Remove(vault.dir + string(os.PathSeparator) + entry.Name()); err != nil {
					return SuiteResult{}, err
				}
			}
		}
		return SuiteResult{}, nil
	})
	if err != nil {
		return err
	}

	return s.MaintainSuiteTrust(ctx)
}

// The shared clock safety margin prevents tolerated clock uncertainty from
// lengthening a pairing or credential-overlap admission window.
func (x *suiteTx) admissionMillis() int64 {
	return x.now.UnixMilli() + int64(noxmcp.SuiteClockSafetyMS)
}
func (x *suiteTx) deadlineExpired(deadline int64) bool {
	return deadline != 0 && x.admissionMillis() >= deadline
}
