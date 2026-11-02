package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// FoundationConnection records an administrator's connection intent, never trust.
type FoundationConnection struct {
	Service   string `json:"service"`
	Origin    string `json:"origin"`
	Revision  string `json:"revision"`
	UpdatedAt string `json:"updatedAt"`
}

func (s *Store) FoundationConnections(ctx context.Context) ([]FoundationConnection, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT service, origin, revision, updated_at FROM foundation_connections ORDER BY service`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []FoundationConnection{}
	for rows.Next() {
		var item FoundationConnection
		if err := rows.Scan(&item.Service, &item.Origin, &item.Revision, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// SaveFoundationConnection requires the revision read by the administrator.
// An empty expected revision means create only, never overwrite an existing row.
func (s *Store) SaveFoundationConnection(ctx context.Context, service, origin, expectedRevision string) (FoundationConnection, error) {
	if (service != "yard" && service != "gateway") || origin == "" {
		return FoundationConnection{}, ErrBadRequest
	}
	revision, err := randomID("connection_")
	if err != nil {
		return FoundationConnection{}, err
	}
	item := FoundationConnection{service, origin, revision, timestamp(time.Now())}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FoundationConnection{}, err
	}
	defer rollback(tx)
	var current string
	err = tx.QueryRowContext(ctx, `SELECT revision FROM foundation_connections WHERE service = ?`, service).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return FoundationConnection{}, err
	}
	if current != expectedRevision {
		return FoundationConnection{}, ErrPreconditionFailed
	}
	if err := foundationSetupPending(ctx, tx, service); err != nil {
		return FoundationConnection{}, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO foundation_connections (service, origin, revision, updated_at) VALUES (?, ?, ?, ?)`, service, origin, revision, item.UpdatedAt)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE foundation_connections SET origin = ?, revision = ?, updated_at = ? WHERE service = ?`, origin, revision, item.UpdatedAt, service)
	}
	if err != nil {
		return FoundationConnection{}, fmt.Errorf("save connection configuration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return FoundationConnection{}, err
	}
	return item, nil
}

func (s *Store) DeleteFoundationConnection(ctx context.Context, service, revision string) error {
	if (service != "yard" && service != "gateway") || revision == "" {
		return ErrBadRequest
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err = foundationSetupPending(ctx, tx, service); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM foundation_connections WHERE service=? AND revision=?`, service, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrPreconditionFailed
	}
	return tx.Commit()
}
func foundationSetupPending(ctx context.Context, tx *sql.Tx, service string) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT record_json FROM suite_connection_attempts WHERE service=?`, service).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var a SuiteConnectionAttempt
	if err = suiteDecode(raw, &a); err != nil {
		return err
	}
	if a.State != "completed" && a.State != "cancelled" && (a.PairingID != "" || a.LeaseUntil > time.Now().UnixMilli()) {
		return fmt.Errorf("%w: resume or cancel the pending Suite connection before changing its address", ErrBadRequest)
	}
	return nil
}
