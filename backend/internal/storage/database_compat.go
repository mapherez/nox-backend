package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func resolveDatabasePath(dataDir string) (string, error) {
	current := filepath.Join(dataDir, DatabaseFileName)
	legacy := filepath.Join(dataDir, LegacyDatabaseFileName)
	currentExists, err := databaseExists(current)
	if err != nil {
		return "", err
	}
	legacyExists, err := databaseExists(legacy)
	if err != nil {
		return "", err
	}
	if currentExists && legacyExists {
		return "", fmt.Errorf("both %s and %s exist; refusing to choose a database", current, legacy)
	}
	if legacyExists {
		return legacy, nil
	}
	if !currentExists {
		archiveExists, err := databaseExists(legacy + ".legacy")
		if err != nil {
			return "", err
		}
		if archiveExists {
			return "", fmt.Errorf("database migration interrupted: legacy archive exists but no active database; restore the complete backup before starting")
		}
	}
	return current, nil
}

func databaseExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("database %s must be a regular file", path)
	}
	return true, nil
}

// A populated database must already have the current schema. In particular,
// never run the historical single-vault reset as an automatic upgrade.
func (s *Store) validateExistingSchema(ctx context.Context) error {
	var tables int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		return fmt.Errorf("inspect database schema: %w", err)
	}
	if tables == 0 {
		return nil
	}
	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("existing database has no valid migration history; refusing automatic reset: %w", err)
	}
	index := 0
	for rows.Next() {
		var version int
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			rows.Close()
			return err
		}
		if index >= len(migrations) || version != migrations[index].version || name != migrations[index].name {
			rows.Close()
			return fmt.Errorf("unsupported migration history at version %d; database left unchanged", version)
		}
		index++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if index != len(migrations) {
		return fmt.Errorf("existing database has pending migrations; refusing historical reset (expected versions 1, 2, 3)")
	}
	// Compare with the embedded schema, not just migration records that could
	// have been incorrectly marked as applied. Extra user indexes are harmless.
	reference, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer reference.Close()
	for _, migration := range migrations {
		body, err := migrationFiles.ReadFile(migration.path)
		if err != nil {
			return err
		}
		if _, err := reference.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("build reference schema: %w", err)
		}
	}
	expected, err := databaseSchema(ctx, reference)
	if err != nil {
		return err
	}
	actual, err := databaseSchema(ctx, s.db)
	if err != nil {
		return err
	}
	for name, definition := range expected {
		if actual[name] != definition {
			return fmt.Errorf("unsupported database schema for %s; database left unchanged", name)
		}
	}
	for name := range actual {
		if strings.HasPrefix(name, "table:") {
			if _, ok := expected[name]; !ok {
				return fmt.Errorf("unknown database table %s; database left unchanged", name)
			}
		}
	}
	return nil
}

func databaseSchema(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT type, name, sql FROM sqlite_master WHERE type IN ('table','index') AND name NOT LIKE 'sqlite_%' AND sql IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var kind, name, definition string
		if err := rows.Scan(&kind, &name, &definition); err != nil {
			return nil, err
		}
		result[kind+":"+name] = strings.Join(strings.Fields(definition), " ")
	}
	return result, rows.Err()
}

// MigrateDatabaseFile is an explicit offline operation. Stop the backend and
// take a complete /data backup first. VACUUM INTO copies committed WAL data;
// no vault, key, session, revision or blob is regenerated.
func MigrateDatabaseFile(ctx context.Context, dataDir string) error {
	source := filepath.Join(dataDir, LegacyDatabaseFileName)
	target := filepath.Join(dataDir, DatabaseFileName)
	archive := source + ".legacy"
	for _, path := range []string{target, archive} {
		exists, err := databaseExists(path)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("%s already exists; migration will not overwrite it", path)
		}
	}
	exists, err := databaseExists(source)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("legacy database %s not found", source)
	}
	db, err := sql.Open("sqlite", source)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	store := &Store{db: db, dataDir: dataDir, dbPath: source}
	if err := store.validateExistingSchema(ctx); err != nil {
		return err
	}
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("source database integrity check failed: %s", integrity)
	}
	// Hold an exclusive connection through the copy. Fail rather than migrate
	// while an existing server connection is using the database.
	for _, query := range []string{"PRAGMA busy_timeout = 0", "PRAGMA synchronous = FULL", "PRAGMA locking_mode = EXCLUSIVE", "BEGIN EXCLUSIVE", "COMMIT"} {
		if _, err := db.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("stop the backend before migrating: %w", err)
		}
	}
	var busy, logFrames, checkpointed int
	if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("database is busy; stop the backend before migrating")
	}
	temp, err := os.CreateTemp(dataDir, ".nox-backend-migrate-*.db")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		return err
	}
	defer os.Remove(tempPath)
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tempPath); err != nil {
		return fmt.Errorf("copy database: %w", err)
	}
	copyDB, err := sql.Open("sqlite", tempPath)
	if err != nil {
		return err
	}
	err = copyDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity)
	copyDB.Close()
	if err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("copied database integrity check failed: %s", integrity)
	}
	if err := db.Close(); err != nil {
		return err
	}
	// All SQLite handles are closed before changing filesystem names.
	if err := os.Rename(source, archive); err != nil {
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		restoreErr := os.Rename(archive, source)
		return fmt.Errorf("promote database: %w (restore legacy name: %v)", err, restoreErr)
	}
	return nil
}
