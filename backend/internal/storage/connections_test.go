package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestFoundationConnectionPersistsAndConcurrentEditsDoNotOverwrite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	saved, err := store.SaveFoundationConnection(ctx, "yard", "https://yard.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.FoundationConnections(ctx)
	if err != nil || len(rows) != 1 || rows[0] != saved {
		t.Fatal("connection intent changed after restart", rows, err)
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, origin := range []string{"https://yard-a.example.com", "https://yard-b.example.com"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.SaveFoundationConnection(ctx, "yard", origin, saved.Revision)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrPreconditionFailed) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("a stale edit overwrote another administrator", successes, conflicts)
	}
	if err := store.DeleteFoundationConnection(ctx, "yard", saved.Revision); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale removal was accepted", err)
	}
	rows, err = store.FoundationConnections(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if err := store.DeleteFoundationConnection(ctx, "yard", rows[0].Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveFoundationConnection(ctx, "yard", "https://new.example.com", rows[0].Revision); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("deleted revision was reused", err)
	}
	replacement, err := store.SaveFoundationConnection(ctx, "yard", "https://new.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Revision == saved.Revision || replacement.Revision == rows[0].Revision {
		t.Fatal("configuration revision was recycled")
	}
}

func TestAdditiveConnectionMigrationPreservesInstalledBaseline(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	seedTestUserAndVault(t, store)
	key, err := store.CurrentAPIKey(ctx, testUserID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateWebSession(ctx, testUserID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var history string
	if err := store.db.QueryRowContext(ctx, `SELECT group_concat(version || ':' || name || ':' || applied_at) FROM schema_migrations WHERE version <= 3`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the actual pre-point-3 schema in this disposable fixture only.
	if _, err := store.db.ExecContext(ctx, `DROP TABLE foundation_connections; DELETE FROM schema_migrations WHERE version = 4;`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, dir)
	if err != nil {
		t.Fatal("supported installed baseline could not upgrade", err)
	}
	if user, valid, err := store.AuthenticateAPIKey(ctx, key); err != nil || !valid || user.ID != testUserID {
		t.Fatal("migration changed existing key", err)
	}
	if user, valid, err := store.UserBySessionToken(ctx, session); err != nil || !valid || user.ID != testUserID {
		t.Fatal("migration changed existing session", err)
	}
	if vault, err := store.VaultByID(ctx, testUserID, testVaultID); err != nil || vault.ID != testVaultID {
		t.Fatal("migration changed existing vault", err)
	}
	var after string
	if err := store.db.QueryRowContext(ctx, `SELECT group_concat(version || ':' || name || ':' || applied_at) FROM schema_migrations WHERE version <= 3`).Scan(&after); err != nil || after != history {
		t.Fatal("historical migration records changed", err)
	}
	if _, err := store.FoundationConnections(ctx); err != nil {
		t.Fatal("additive table not available", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, dir)
	if err != nil {
		t.Fatal("second startup failed", err)
	}
}

func TestManagedWritesCannotOverwriteConcurrentPromotion(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		email := fmt.Sprintf("race-%d@example.com", i)
		target, err := store.UpsertManagedUser(ctx, email, UserRoleUser)
		if err != nil {
			t.Fatal(err)
		}
		var wait sync.WaitGroup
		failures := make(chan error, 2)
		wait.Add(2)
		go func() { defer wait.Done(); failures <- store.UpdateManagedUser(ctx, target.ID, "role", UserRoleAdmin) }()
		go func() {
			defer wait.Done()
			_, err := store.UpsertManagedUser(ctx, email, UserRoleUser)
			if errors.Is(err, ErrBadRequest) {
				err = nil
			}
			failures <- err
		}()
		wait.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		promoted, err := store.UserByID(ctx, target.ID)
		if err != nil || promoted.Role != UserRoleAdmin {
			t.Fatal("concurrent upsert demoted admin", err)
		}
		if err := store.UpdateManagedUser(ctx, target.ID, "status", UserStatusDisabled); !errors.Is(err, ErrBadRequest) {
			t.Fatal("atomic status guard failed", err)
		}
		if err := store.UpdateManagedUser(ctx, target.ID, "role", UserRoleUser); !errors.Is(err, ErrBadRequest) {
			t.Fatal("atomic role guard failed", err)
		}
		if err := store.DeleteManagedUser(ctx, target.ID); !errors.Is(err, ErrBadRequest) {
			t.Fatal("atomic deletion guard failed", err)
		}
	}
}
