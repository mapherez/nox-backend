package storage

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDatabaseNameMigrationPreservesDataAndKeys(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	seedTestUserAndVault(t, store)
	content := []byte("# Existing content\n")
	hash := sha256Hex(content)
	begin, err := store.BeginSync(ctx, testUserID, BeginSyncRequest{ClientID: "existing-client", ClientName: "Existing", VaultID: testVaultID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PlanSync(ctx, testUserID, ManifestRequest{SessionID: begin.SessionID, ClientID: "existing-client", VaultID: testVaultID, Files: []ManifestFile{{Path: "keep.md", Hash: hash, Size: int64(len(content))}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StageUpload(ctx, testUserID, begin.SessionID, "existing-client", "keep.md", hash, int64(len(content)), bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitSync(ctx, testUserID, CommitRequest{SessionID: begin.SessionID, ClientID: "existing-client"}); err != nil {
		t.Fatal(err)
	}
	webSession, err := store.CreateWebSession(ctx, testUserID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CurrentAPIKey(ctx, testUserID)
	if err != nil {
		t.Fatal(err)
	}
	var migrationsBefore string
	if err := store.db.QueryRowContext(ctx, "SELECT group_concat(version || ':' || name || ':' || applied_at) FROM schema_migrations").Scan(&migrationsBefore); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, DatabaseFileName), filepath.Join(dir, LegacyDatabaseFileName)); err != nil {
		t.Fatal(err)
	}
	// Old deployments are usable without a physical migration.
	legacy, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.DBPath() != filepath.Join(dir, LegacyDatabaseFileName) {
		t.Fatal("legacy path not retained")
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	if err := MigrateDatabaseFile(ctx, dir); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	if migrated.DBPath() != filepath.Join(dir, DatabaseFileName) {
		t.Fatal("canonical database not selected")
	}
	user, valid, err := migrated.AuthenticateAPIKey(ctx, key)
	if err != nil || !valid || user.ID != testUserID {
		t.Fatalf("existing key lost: %v, %v", valid, err)
	}
	vault, err := migrated.VaultByID(ctx, testUserID, testVaultID)
	if err != nil || vault.ID != testVaultID || vault.Revision != 1 {
		t.Fatalf("vault lost: %v", err)
	}
	metadata, err := migrated.DownloadFile(ctx, testUserID, testVaultID, "keep.md")
	if err != nil || metadata.Hash != hash || metadata.Revision != 1 || metadata.Size != int64(len(content)) {
		t.Fatalf("file metadata changed: %#v, %v", metadata, err)
	}
	gotContent, err := os.ReadFile(migrated.BlobPath(hash))
	if err != nil || !bytes.Equal(gotContent, content) {
		t.Fatal("blob content changed", err)
	}
	if _, valid, err := migrated.UserBySessionToken(ctx, webSession); err != nil || !valid {
		t.Fatal("web session lost", err)
	}
	var migrationsAfter string
	if err := migrated.db.QueryRowContext(ctx, "SELECT group_concat(version || ':' || name || ':' || applied_at) FROM schema_migrations").Scan(&migrationsAfter); err != nil {
		t.Fatal(err)
	}
	if migrationsBefore != migrationsAfter {
		t.Fatal("migration records changed")
	}
	if _, err := os.Stat(filepath.Join(dir, LegacyDatabaseFileName) + ".legacy"); err != nil {
		t.Fatal("legacy archive missing", err)
	}
	if err := MigrateDatabaseFile(ctx, dir); err == nil {
		t.Fatal("repeated migration must not overwrite data")
	}
}

func TestInterruptedDatabaseMigrationDoesNotCreateEmptyStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, LegacyDatabaseFileName)+".legacy", []byte("retained data"), 0600); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(context.Background(), dir); err == nil {
		store.Close()
		t.Fatal("empty store created after interrupted migration")
	}
	if _, err := os.Stat(filepath.Join(dir, DatabaseFileName)); !os.IsNotExist(err) {
		t.Fatal("replacement database created")
	}
}

func TestDatabaseMigrationRefusesActiveConnection(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	seedTestUserAndVault(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, DatabaseFileName), filepath.Join(dir, LegacyDatabaseFileName)); err != nil {
		t.Fatal(err)
	}
	active, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if err := MigrateDatabaseFile(ctx, dir); err == nil {
		t.Fatal("migration accepted active connection")
	}
	if _, err := os.Stat(filepath.Join(dir, DatabaseFileName)); !os.IsNotExist(err) {
		t.Fatal("destination created during active connection")
	}
}

func TestOldDatabaseRefusedWithoutReset(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, LegacyDatabaseFileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:2] {
		body, _ := migrationFiles.ReadFile(migration.path)
		if _, err := db.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (?, ?, 'original')", migration.version, migration.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO files(path, current_hash, revision, updated_at) VALUES ('keep.md', 'original-hash', 17, 'original')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if store, err := Open(ctx, dir); err == nil {
		store.Close()
		t.Fatal("old schema accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("rejected database was modified")
	}
	if err := MigrateDatabaseFile(ctx, dir); err == nil {
		t.Fatal("unsupported database migrated")
	}
}

func TestAmbiguousDatabaseNamesRefused(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{DatabaseFileName, LegacyDatabaseFileName} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if store, err := Open(context.Background(), dir); err == nil {
		store.Close()
		t.Fatal("ambiguous databases accepted")
	}
}

func TestMigrationHistoryCannotHideWrongSchema(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "ALTER TABLE files ADD COLUMN unexpected TEXT"); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if reopened, err := Open(ctx, dir); err == nil {
		reopened.Close()
		t.Fatal("incorrect schema accepted")
	}
}
