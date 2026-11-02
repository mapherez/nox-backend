package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLocalSetupMigrationPreservesVersionSevenInstallation(t *testing.T) {
	f := newTrustFixture(t)
	ctx := context.Background()
	seedTestUserAndVault(t, f.store)
	content := []byte("# Existing consumer data\n")
	commitFileForTest(t, f.store, "existing-client", 0, "keep.md", content)
	key, err := f.store.CurrentAPIKey(ctx, testUserID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := f.store.CreateWebSession(ctx, testUserID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := f.store.SaveFoundationConnection(ctx, "yard", "https://existing.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	provisioning := f.provision(t, "existing-product", "product", true)
	identity, err := f.store.SuiteAuthenticateService(ctx, provisioning.ServiceCredential)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := f.store.SuiteKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	masterPath := filepath.Join(f.dir, "secrets", "suite", "master.key")
	master, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatal(err)
	}
	// Only disposable fixture data is rewound. Migrations 1–7 are unchanged;
	// removing the new empty journal reconstructs the previous release schema.
	if _, err := f.store.db.ExecContext(ctx, `DROP TABLE suite_connection_attempts;
DELETE FROM schema_migrations WHERE version=8`); err != nil {
		t.Fatal(err)
	}
	const historyQuery = `SELECT group_concat(version || ':' || name || ':' || applied_at) FROM (SELECT * FROM schema_migrations WHERE version <= 7 ORDER BY version)`
	var history string
	if err := f.store.db.QueryRowContext(ctx, historyQuery).Scan(&history); err != nil {
		t.Fatal(err)
	}
	for startup := 0; startup < 2; startup++ {
		f.reopen(t)
		if user, valid, err := f.store.AuthenticateAPIKey(ctx, key); err != nil || !valid || user.ID != testUserID {
			t.Fatal("upgrade changed the existing personal key", err)
		}
		if user, valid, err := f.store.UserBySessionToken(ctx, session); err != nil || !valid || user.ID != testUserID {
			t.Fatal("upgrade changed the existing browser session", err)
		}
		file, err := f.store.DownloadFile(ctx, testUserID, testVaultID, "keep.md")
		if err != nil || file.Revision != 1 || file.Hash != sha256Hex(content) {
			t.Fatal("upgrade changed committed file metadata", err)
		}
		blob, err := os.ReadFile(f.store.BlobPath(file.Hash))
		if err != nil || !bytes.Equal(blob, content) {
			t.Fatal("upgrade changed existing file contents", err)
		}
		connections, err := f.store.FoundationConnections(ctx)
		if err != nil || len(connections) != 1 || connections[0] != connection {
			t.Fatal("upgrade rewrote a saved native address or revision", err)
		}
		currentIdentity, err := f.store.SuiteAuthenticateService(ctx, provisioning.ServiceCredential)
		if err != nil || !reflect.DeepEqual(currentIdentity, identity) {
			t.Fatal("upgrade replaced an existing association or credential", err)
		}
		currentKeys, err := f.store.SuiteKeys(ctx)
		if err != nil || !reflect.DeepEqual(currentKeys, keys) {
			t.Fatal("upgrade changed the Suite issuer or signing keys", err)
		}
		currentMaster, err := os.ReadFile(masterPath)
		if err != nil || !bytes.Equal(currentMaster, master) {
			t.Fatal("upgrade replaced the secret-store master key", err)
		}
		var currentHistory string
		if err := f.store.db.QueryRowContext(ctx, historyQuery).Scan(&currentHistory); err != nil || currentHistory != history {
			t.Fatal("upgrade rewrote historical migration records", err)
		}
		attempts, err := f.store.SuiteConnectionAttempts(ctx)
		if err != nil || len(attempts) != 0 {
			t.Fatal("upgrade fabricated a new local association", err)
		}
	}
}
