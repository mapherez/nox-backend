package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestListFilesCommittedContentsAndZIPMetadata(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel() // Also detects accidental s.db calls inside a transaction.
	empty, err := store.ListFiles(ctx, testUserID, testVaultID)
	if err != nil || empty.VaultID != testVaultID || empty.ServerRevision != 0 || empty.Files == nil || len(empty.Files) != 0 {
		t.Fatalf("empty vault: %#v, %v", empty, err)
	}
	contents := map[string][]byte{
		"World/Humans.md":        []byte("# Humans\r\n"),
		"World/Deep/Humans.md":   []byte("nested"),
		"Images/Árvore azul.png": {0x89, 'P', 'N', 'G', 0, 0xff},
		"Empty file.bin":         {},
		"other/Humans.md":        []byte("same name, different folder"),
	}
	commitReadState(t, store, contents)
	list, err := store.ListFiles(ctx, testUserID, testVaultID)
	if err != nil || list.ServerRevision != 1 || len(list.Files) != len(contents) {
		t.Fatalf("committed list: %#v, %v", list, err)
	}
	for i, file := range list.Files {
		if i > 0 && list.Files[i-1].Path >= file.Path {
			t.Fatalf("paths not sorted: %#v", list.Files)
		}
		content, exists := contents[file.Path]
		if !exists || file.Hash != sha256Hex(content) || file.Size != int64(len(content)) || file.Revision != 1 {
			t.Fatalf("unexpected metadata: %#v", file)
		}
		blob, err := os.ReadFile(store.BlobPath(file.Hash))
		if err != nil || !bytes.Equal(blob, content) {
			t.Fatalf("blob bytes for %s: %v", file.Path, err)
		}
	}
	zipFiles, err := store.CurrentVaultFiles(ctx, testUserID, testVaultID)
	if err != nil || !reflect.DeepEqual(zipFiles, list.Files) {
		t.Fatalf("ZIP metadata differs: %#v, %v", zipFiles, err)
	}
}

func TestReadsDuringStagingDoNotModifySyncState(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	commitReadState(t, store, map[string][]byte{"note.md": []byte("confirmed"), "removed.md": []byte("remove")})
	commitReadState(t, store, map[string][]byte{"note.md": []byte("confirmed")})
	beforeList, err := store.ListFiles(ctx, testUserID, testVaultID)
	if err != nil {
		t.Fatal(err)
	}
	begin := stageReadState(t, store, map[string][]byte{"note.md": []byte("pending change"), "pending.png": {0, 1, 2}})
	// Even an expired lock must not be reaped by these read endpoints.
	if _, err := store.db.ExecContext(ctx, `UPDATE sync_locks SET expires_at = ? WHERE vault_id = ?`, timestamp(time.Now().Add(-time.Minute)), testVaultID); err != nil {
		t.Fatal(err)
	}
	before := readDatabaseState(t, store)
	for i := 0; i < 2; i++ {
		list, err := store.ListFiles(ctx, testUserID, testVaultID)
		if err != nil || !reflect.DeepEqual(list, beforeList) {
			t.Fatalf("staging leaked into listing: %#v, %v", list, err)
		}
		selected := beforeList.Files[0]
		got, err := store.DownloadFileConditional(ctx, testUserID, testVaultID, selected.Path, DownloadConditions{&selected.Hash, &selected.Revision})
		if err != nil || got != selected {
			t.Fatalf("staging changed selected metadata: %#v, %v", got, err)
		}
		for _, path := range []string{"removed.md", "pending.png"} {
			if _, err := store.DownloadFile(ctx, testUserID, testVaultID, path); !errors.Is(err, ErrNotFound) {
				t.Fatalf("%s should not be downloadable: %v", path, err)
			}
		}
	}
	if after := readDatabaseState(t, store); after != before {
		t.Fatal("read modified database state")
	}
	if _, err := os.Stat(store.stagingSessionDir(begin.SessionID)); err != nil {
		t.Fatalf("read removed staging: %v", err)
	}
}

func TestConditionalDownloadSelectionChanges(t *testing.T) {
	for _, change := range []string{"unrelated", "changed", "removed", "renamed", "same bytes new revision"} {
		t.Run(change, func(t *testing.T) {
			store := newTestStore(t)
			ctx := context.Background()
			commitReadState(t, store, map[string][]byte{"note.md": []byte("original")})
			list, err := store.ListFiles(ctx, testUserID, testVaultID)
			if err != nil {
				t.Fatal(err)
			}
			selected := list.Files[0]
			next := map[string][]byte{"note.md": []byte("original")}
			var wantErr error
			switch change {
			case "unrelated":
				next["image.png"] = []byte{0, 0xff}
			case "changed":
				next["note.md"] = []byte("changed")
				wantErr = ErrFileChanged
			case "removed":
				delete(next, "note.md")
				wantErr = ErrNotFound
			case "renamed":
				delete(next, "note.md")
				next["renamed.md"] = []byte("original")
				wantErr = ErrNotFound
			case "same bytes new revision":
				commitReadState(t, store, map[string][]byte{"note.md": []byte("intermediate")})
				wantErr = ErrFileChanged
			}
			commitReadState(t, store, next)
			got, err := store.DownloadFileConditional(ctx, testUserID, testVaultID, selected.Path, DownloadConditions{&selected.Hash, &selected.Revision})
			if !errors.Is(err, wantErr) || wantErr == nil && got != selected {
				t.Fatalf("selected download: %#v, %v; want %v", got, err, wantErr)
			}
			current, err := store.ListFiles(ctx, testUserID, testVaultID)
			if err != nil || current.ServerRevision <= list.ServerRevision || len(current.Files) != len(next) {
				t.Fatalf("new list: %#v, %v", current, err)
			}
			if change == "same bytes new revision" {
				if _, err := store.DownloadFileConditional(ctx, testUserID, testVaultID, selected.Path, DownloadConditions{ExpectedHash: &selected.Hash}); err != nil {
					t.Fatalf("hash-only condition should still match: %v", err)
				}
			}
		})
	}
}

func TestDownloadConditionsValidation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	commitReadState(t, store, map[string][]byte{"note.md": []byte("content")})
	file, err := store.DownloadFile(ctx, testUserID, testVaultID, "note.md")
	if err != nil {
		t.Fatal(err)
	}
	wrongHash, badHash, emptyHash := strings.Repeat("0", 64), "invalid", ""
	wrongRevision, negative := file.Revision+1, int64(-1)
	for _, tc := range []struct {
		name       string
		conditions DownloadConditions
		want       error
	}{
		{"legacy", DownloadConditions{}, nil},
		{"both", DownloadConditions{&file.Hash, &file.Revision}, nil},
		{"hash only", DownloadConditions{ExpectedHash: &file.Hash}, nil},
		{"revision only", DownloadConditions{ExpectedRevision: &file.Revision}, nil},
		{"wrong hash", DownloadConditions{ExpectedHash: &wrongHash}, ErrFileChanged},
		{"wrong revision", DownloadConditions{ExpectedRevision: &wrongRevision}, ErrFileChanged},
		{"bad hash", DownloadConditions{ExpectedHash: &badHash}, ErrBadRequest},
		{"empty hash", DownloadConditions{ExpectedHash: &emptyHash}, ErrBadRequest},
		{"negative revision", DownloadConditions{ExpectedRevision: &negative}, ErrBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := store.DownloadFileConditional(ctx, testUserID, testVaultID, "note.md", tc.conditions)
			if !errors.Is(err, tc.want) || tc.want == nil && got != file || tc.want != nil && got != (DownloadResult{}) {
				t.Fatalf("got %#v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestReadVaultIsolation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	other, err := store.UpsertAllowedUser(ctx, "other@example.com", UserRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	otherVault, err := store.CreateVault(ctx, other.ID, "Private")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.CreateVault(ctx, testUserID, "Deleted")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SoftDeleteVault(ctx, testUserID, deleted.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateVault(ctx, testUserID, "Second")
	if err != nil {
		t.Fatal(err)
	}
	commitReadState(t, store, map[string][]byte{"note.md": []byte("only in first vault")})
	list, err := store.ListFiles(ctx, testUserID, second.ID)
	if err != nil || len(list.Files) != 0 || list.ServerRevision != 0 {
		t.Fatalf("other owned vault leaked files: %#v, %v", list, err)
	}
	for _, vaultID := range []string{otherVault.ID, deleted.ID, "vault_missing"} {
		if _, err := store.ListFiles(ctx, testUserID, vaultID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("list %s: %v", vaultID, err)
		}
		if _, err := store.DownloadFile(ctx, testUserID, vaultID, "note.md"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("download %s: %v", vaultID, err)
		}
	}
	if _, err := store.ListFiles(ctx, testUserID, ""); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("missing vault ID: %v", err)
	}
}

func TestReadAPIKeyIdentityRotationAndDisabledUser(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key, err := store.CurrentAPIKey(ctx, testUserID)
	if err != nil {
		t.Fatal(err)
	}
	user, valid, err := store.AuthenticateAPIKey(ctx, key)
	if err != nil || !valid || user.ID != testUserID || user.Role != UserRoleAdmin {
		t.Fatalf("key identity: %#v, %v, %v", user, valid, err)
	}
	if _, err := store.ListFiles(ctx, user.ID, testVaultID); err != nil {
		t.Fatalf("authenticated owner cannot list: %v", err)
	}
	newKey, err := store.RotateAPIKey(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "invalid", key} {
		if _, valid, err := store.AuthenticateAPIKey(ctx, invalid); err != nil || valid {
			t.Fatalf("invalid key accepted: valid=%v, error=%v", valid, err)
		}
	}
	if _, valid, err := store.AuthenticateAPIKey(ctx, newKey); err != nil || !valid {
		t.Fatalf("rotated key rejected: %v, %v", valid, err)
	}
	// SetUserStatus disallows disabling admins, so use an ordinary allowed user.
	other, err := store.UpsertAllowedUser(ctx, "disabled@example.com", UserRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := store.CurrentAPIKey(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserStatus(ctx, other.ID, UserStatusDisabled); err != nil {
		t.Fatal(err)
	}
	if _, valid, err := store.AuthenticateAPIKey(ctx, otherKey); err != nil || valid {
		t.Fatalf("disabled key accepted: %v, %v", valid, err)
	}
}

func TestListFilesConsistentDuringCommits(t *testing.T) {
	store := newTestStore(t)
	commitReadState(t, store, map[string][]byte{"note.md": []byte("revision 1")})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		for {
			list, err := store.ListFiles(ctx, testUserID, testVaultID)
			if ctx.Err() != nil {
				done <- nil
				return
			}
			if err != nil {
				done <- err
				return
			}
			if len(list.Files) != 1 || list.Files[0].Revision != list.ServerRevision || list.Files[0].Hash != sha256Hex([]byte(fmt.Sprintf("revision %d", list.ServerRevision))) {
				done <- fmt.Errorf("mixed commit states: %#v", list)
				return
			}
		}
	}()
	<-started
	for revision := 2; revision <= 20; revision++ {
		commitReadState(t, store, map[string][]byte{"note.md": []byte(fmt.Sprintf("revision %d", revision))})
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func stageReadState(t *testing.T, store *Store, contents map[string][]byte) BeginSyncResult {
	t.Helper()
	ctx := context.Background()
	list, err := store.ListFiles(ctx, testUserID, testVaultID)
	if err != nil {
		t.Fatal(err)
	}
	previous := map[string]int64{}
	for _, file := range list.Files {
		previous[file.Path] = file.Revision
	}
	begin := beginSyncForTest(t, store, "read_test_client")
	manifest := ManifestRequest{SessionID: begin.SessionID, ClientID: "read_test_client", VaultID: testVaultID, LastKnownServerRevision: list.ServerRevision}
	for path, content := range contents {
		manifest.Files = append(manifest.Files, ManifestFile{Path: path, Hash: sha256Hex(content), Size: int64(len(content)), LastKnownRevision: previous[path]})
	}
	plan, err := store.PlanSync(ctx, testUserID, manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range plan.Actions {
		if action.Type == PlanActionUpload {
			if err := store.StageUpload(ctx, testUserID, begin.SessionID, manifest.ClientID, action.Path, action.ExpectedHash, action.Size, bytes.NewReader(contents[action.Path])); err != nil {
				t.Fatal(err)
			}
		}
	}
	return begin
}

func commitReadState(t *testing.T, store *Store, contents map[string][]byte) {
	t.Helper()
	begin := stageReadState(t, store, contents)
	if _, err := store.CommitSync(context.Background(), testUserID, CommitRequest{SessionID: begin.SessionID, ClientID: "read_test_client"}); err != nil {
		t.Fatal(err)
	}
}

// Compare every sync-related row, including timestamps and lock expiries.
func readDatabaseState(t *testing.T, store *Store) string {
	t.Helper()
	var result strings.Builder
	for _, table := range []string{"vaults", "files", "file_revisions", "tombstones", "sync_locks", "sync_sessions", "staged_uploads", "sync_plan_actions", "conflicts"} {
		rows, err := store.db.QueryContext(context.Background(), "SELECT * FROM "+table+" ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values, dest := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			fmt.Fprintf(&result, "%s: %#v\n", table, values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return result.String()
}
