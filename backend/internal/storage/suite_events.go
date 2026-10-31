package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

const suiteEventMaximum = 100000
const suiteEventRetention = 7 * 24 * time.Hour

func (s *Store) suiteEventSignal() <-chan struct{} {
	s.suiteEventMu.Lock()
	defer s.suiteEventMu.Unlock()
	if s.suiteEventWake == nil {
		s.suiteEventWake = make(chan struct{})
	}
	return s.suiteEventWake
}
func (s *Store) notifySuiteEvents() {
	s.suiteEventMu.Lock()
	defer s.suiteEventMu.Unlock()
	if s.suiteEventWake != nil {
		close(s.suiteEventWake)
	}
	s.suiteEventWake = make(chan struct{})
}
func (x *suiteTx) pruneEvents() error {
	var high int64
	if err := x.tx.QueryRowContext(x.ctx, `SELECT COALESCE((SELECT seq FROM sqlite_sequence WHERE name='suite_trust_events'),0)`).Scan(&high); err != nil {
		return err
	}
	var floor int64
	if err := x.tx.QueryRowContext(x.ctx, `SELECT COALESCE(MAX(sequence),0) FROM suite_trust_events WHERE occurred_at<? OR sequence<=?`, x.now.Add(-suiteEventRetention).UnixMilli(), high-suiteEventMaximum).Scan(&floor); err != nil {
		return err
	}
	if floor > 0 {
		if _, err := x.tx.ExecContext(x.ctx, `UPDATE suite_registry_state SET event_floor=MAX(event_floor,?) WHERE singleton=1`, floor); err != nil {
			return err
		}
		if _, err := x.tx.ExecContext(x.ctx, `DELETE FROM suite_trust_events WHERE sequence<=?`, floor); err != nil {
			return err
		}
	}
	_, err := x.tx.ExecContext(x.ctx, `DELETE FROM suite_product_tombstones WHERE expires_at<=?`, x.now.UnixMilli())
	return err
}
func suiteCursorScope(a suiteActor) string {
	// Scope changes (including privilege/association changes) require reconciliation.
	if a.user != nil {
		return a.principal + ":" + a.user.Role
	}
	return a.principal + ":" + a.service.AssociationID + fmt.Sprint(a.service.Grants)
}
func (x *suiteTx) eventBounds() (string, int64, int64, error) {
	var gen string
	var floor, high int64
	err := x.tx.QueryRowContext(x.ctx, `SELECT generation,event_floor,COALESCE((SELECT seq FROM sqlite_sequence WHERE name='suite_trust_events'),0) FROM suite_registry_state WHERE singleton=1`).Scan(&gen, &floor, &high)
	return gen, floor, high, err
}
func (x *suiteTx) eventCursor(a suiteActor, sequence int64) (string, error) {
	gen, _, high, err := x.eventBounds()
	if err != nil {
		return "", err
	}
	if sequence < 0 {
		sequence = high
	}
	raw, err := json.Marshal(struct {
		Generation string `json:"g"`
		Sequence   int64  `json:"s"`
	}{gen, sequence})
	if err != nil {
		return "", err
	}
	aead := x.store.suiteVault.aead
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	encoded := aead.Seal(nonce, nonce, raw, []byte("NoX Suite cursor v1:"+suiteCursorScope(a)))
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}
func cursorExpired() error {
	return suiteError(410, "EVENT_CURSOR_EXPIRED", "The event cursor cannot be resumed. Replace the complete authorized snapshot and cursor before continuing.")
}
func (x *suiteTx) openEventCursor(a suiteActor, token string) (int64, error) {
	if len(token) < 1 || len(token) > 256 {
		return 0, cursorExpired()
	}
	encoded, err := base64.RawURLEncoding.DecodeString(token)
	aead := x.store.suiteVault.aead
	if err != nil || len(encoded) < aead.NonceSize()+aead.Overhead() {
		return 0, cursorExpired()
	}
	raw, err := aead.Open(nil, encoded[:aead.NonceSize()], encoded[aead.NonceSize():], []byte("NoX Suite cursor v1:"+suiteCursorScope(a)))
	if err != nil {
		return 0, cursorExpired()
	}
	var v struct {
		Generation string `json:"g"`
		Sequence   int64  `json:"s"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return 0, cursorExpired()
	}
	gen, floor, high, err := x.eventBounds()
	if err != nil {
		return 0, err
	}
	if v.Generation != gen || v.Sequence < floor || v.Sequence > high {
		return 0, cursorExpired()
	}
	return v.Sequence, nil
}
func suiteEventPermitted(a suiteActor, e noxmcp.SuiteEvent) bool {
	if registryPrivileged(a) {
		return true
	}
	switch e.Type {
	case "authorization.invalidated", "service.invalidated", "signing-keys.changed":
		return true
	case "product.upserted":
		var record noxmcp.SuiteRegistrationRecord
		return json.Unmarshal(e.Data["record"], &record) == nil && record.ProductID == a.service.ProductID
	case "observation.updated":
		var obs noxmcp.SuiteObservation
		return json.Unmarshal(e.Data["observation"], &obs) == nil && obs.ProductID == a.service.ProductID
	default:
		var id string
		return json.Unmarshal(e.Data["productId"], &id) == nil && id == a.service.ProductID
	}
}
func (s *Store) suiteEventPage(ctx context.Context, caller SuiteCaller, after string, limit int) (SuiteResult, bool, error) {
	var progress bool
	out, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		a, err := x.actor(caller)
		if err != nil {
			return SuiteResult{}, err
		}
		if !a.grant("events.read") {
			return SuiteResult{}, suiteDenied()
		}
		if err = x.pruneEvents(); err != nil {
			return SuiteResult{}, err
		}
		sequence, err := x.openEventCursor(a, after)
		if err != nil {
			return SuiteResult{}, err
		}
		initial := sequence
		rows, err := x.tx.QueryContext(ctx, `SELECT sequence,event_json FROM suite_trust_events WHERE sequence>? ORDER BY sequence LIMIT ?`, sequence, limit+1)
		if err != nil {
			return SuiteResult{}, err
		}
		page := noxmcp.SuiteEventPage{ContractVersion: "1.0", Events: []noxmcp.SuiteEvent{}}
		bytes := 0
		scanned := 0
		for rows.Next() {
			var next int64
			var raw string
			if err = rows.Scan(&next, &raw); err != nil {
				rows.Close()
				return SuiteResult{}, err
			}
			if scanned == limit {
				page.HasMore = true
				break
			}
			var event noxmcp.SuiteEvent
			if err = json.Unmarshal([]byte(raw), &event); err != nil {
				rows.Close()
				return SuiteResult{}, err
			}
			if suiteEventPermitted(a, event) {
				// A page is bounded independently of its event count. An event validated at
				// commit is never silently discarded; permit one complete event per page.
				if len(page.Events) > 0 && bytes+len(raw) > noxmcp.SuiteMaxPayloadBytes-1024 {
					page.HasMore = true
					break
				}
				page.Events = append(page.Events, event)
				bytes += len(raw)
			}
			sequence = next
			scanned++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return SuiteResult{}, err
		}
		progress = sequence != initial
		if progress {
			page.Cursor, err = x.eventCursor(a, sequence)
		} else {
			page.Cursor = after
		}
		if err != nil {
			return SuiteResult{}, err
		}
		return suiteResponse(200, page)
	})
	return out, progress, err
}

// Waiting never holds a SQLite transaction. Local commits wake subscribers;
// bounded periodic checks also observe commits by another process and revocations.
func (s *Store) SuiteReadEvents(ctx context.Context, caller SuiteCaller, after string, wait time.Duration, limit int) (SuiteResult, error) {
	if wait < 0 || wait > 25*time.Second || limit < 1 || limit > 256 {
		return SuiteResult{}, suiteError(400, "INVALID_REGISTRATION", "Invalid bounded event query.")
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		wake := s.suiteEventSignal()
		result, progress, err := s.suiteEventPage(ctx, caller, after, limit)
		if err != nil || progress || wait == 0 {
			return result, err
		}
		tick := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			tick.Stop()
			return SuiteResult{}, ctx.Err()
		case <-deadline.C:
			tick.Stop()
			result, _, err = s.suiteEventPage(ctx, caller, after, limit)
			return result, err
		case <-wake:
			tick.Stop()
		case <-tick.C:
		}
	}
}

// ResetSuiteEventCursors is an explicit offline restore operation. Normal startup
// never changes the event generation. It leaves registry, trust and events intact.
func (s *Store) ResetSuiteEventCursors(ctx context.Context) error {
	_, err := s.suiteTransaction(ctx, func(x *suiteTx) (SuiteResult, error) {
		generation, err := suiteRandom()
		if err != nil {
			return SuiteResult{}, err
		}
		_, err = x.tx.ExecContext(ctx, `UPDATE suite_registry_state SET generation=? WHERE singleton=1`, generation)
		x.eventsWritten = true
		return SuiteResult{}, err
	})
	return err
}
