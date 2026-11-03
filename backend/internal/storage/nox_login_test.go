package storage

import (
	"context"
	noxauth "github.com/mapherez/nox-auth/client"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNoXLoginPreservesExistingOwnershipKeysAndDomainRole(t *testing.T) {
	ctx := context.Background()
	data, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	previous, err := data.UpsertAllowedUser(ctx, "owner@example.test", UserRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	previous, err = data.CompleteGoogleLogin(ctx, OAuthProfile{Sub: "trusted-google-subject", Email: previous.Email, DisplayName: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := data.CurrentAPIKey(ctx, previous.ID)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := data.CreateVault(ctx, previous.ID, "Existing data")
	if err != nil {
		t.Fatal(err)
	}
	identity := noxauth.Identity{Subject: "auth-subject", Email: previous.Email, EmailVerified: true, Admin: true, GoogleSubject: previous.GoogleSub, GoogleIssuer: "https://accounts.google.com", Name: "Owner"}
	for range 2 {
		current, err := data.CompleteNoXLogin(ctx, "https://auth.example.test", identity)
		if err != nil || current.ID != previous.ID || current.Role != previous.Role {
			t.Fatal("existing identity/role changed", err, current)
		}
		foundKey, err := data.CurrentAPIKey(ctx, current.ID)
		if err != nil || foundKey != key {
			t.Fatal("existing key changed", err)
		}
		owner, err := data.VaultOwnerID(ctx, vault.ID)
		if err != nil || owner != current.ID {
			t.Fatal("ownership changed", err)
		}
	}
	users, err := data.ListUsers(ctx)
	if err != nil || len(users) != 1 {
		t.Fatal("duplicate user created", err)
	}
	conflicting := identity
	conflicting.GoogleSubject = "another-google-subject"
	if _, err := data.CompleteNoXLogin(ctx, "https://auth.example.test", conflicting); err == nil {
		t.Fatal("different Google account linked by email")
	}
	conflicting = identity
	conflicting.Subject = "another-auth-subject"
	if _, err := data.CompleteNoXLogin(ctx, "https://auth.example.test", conflicting); err == nil {
		t.Fatal("Auth identity replaced silently")
	}
	metadata, err := os.Stat(filepath.Join(data.dataDir, "nox-login.db"))
	if err != nil || metadata.Mode().Perm() != 0600 {
		t.Fatal("Auth database is not private", err)
	}
}
func TestNoXLoginNewAccountAndPersistentSession(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	data, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	identity := noxauth.Identity{Subject: "auth-subject", Email: "new@example.test", EmailVerified: true, Admin: true, GoogleSubject: "verified-google-subject", GoogleIssuer: "https://accounts.google.com", Name: "New Account"}
	user, err := data.CompleteNoXLogin(ctx, "https://auth.example.test", identity)
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != UserRoleAdmin || !data.NoXManager(user.ID) {
		t.Fatal("centrally admitted new administrator not established")
	}
	credentials := noxauth.Credentials{Identity: identity, AccessToken: "private-session", ExpiresAt: time.Now().Add(365 * 24 * time.Hour), BrowserRenewAt: time.Now().Add(30 * 24 * time.Hour)}
	token, err := data.CreateNoXSession(ctx, user.ID, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if err = data.Close(); err != nil {
		t.Fatal(err)
	}
	data, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	restored, stored, valid, err := data.NoXSession(ctx, token)
	if err != nil || !valid || restored.ID != user.ID || stored.AccessToken != credentials.AccessToken {
		t.Fatal("NoX session lost on restart", err)
	}
	var migrations int
	if err := data.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&migrations); err != nil || migrations != 8 {
		t.Fatal("existing schema history changed", migrations, err)
	}
}

func TestNoXManagerBindingIsNotInheritedByRecreatedEmail(t *testing.T) {
	ctx := context.Background()
	data, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	identity := noxauth.Identity{Subject: "stable-auth-subject", Email: "manager@example.test", EmailVerified: true, Admin: true, GoogleSubject: "stable-google-subject", GoogleIssuer: "https://accounts.google.com"}
	previous, err := data.CompleteNoXLogin(ctx, "https://auth.example.test", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err = data.DeleteUser(ctx, previous.ID); err != nil {
		t.Fatal(err)
	}
	next, err := data.UpsertAllowedUser(ctx, previous.Email, UserRoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if next.ID == previous.ID || data.NoXManager(next.ID) {
		t.Fatal("new account inherited old verified binding by email")
	}
	next, err = data.CompleteNoXLogin(ctx, "https://auth.example.test", identity)
	if err != nil || !data.NoXManager(next.ID) {
		t.Fatal("verified re-login could not establish new binding", err)
	}
}
