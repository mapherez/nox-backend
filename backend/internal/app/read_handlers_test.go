package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mapherez/nox-sync/backend/internal/storage"
)

func TestReadAPIContentsLegacyDownloadsAndZIP(t *testing.T) {
	server, store, user, vault := newTestServer(t)
	key := readTestKey(t, store, user.ID)
	empty := readRequest(server, key, http.MethodGet, "/v1/files?vaultId="+vault.ID, nil)
	assertReadStatus(t, empty, http.StatusOK, "")
	var emptyJSON map[string]any
	decodeReadJSON(t, empty, &emptyJSON)
	if len(emptyJSON) != 3 || emptyJSON["vaultId"] != vault.ID || emptyJSON["serverRevision"] != float64(0) || !reflect.DeepEqual(emptyJSON["files"], []any{}) {
		t.Fatalf("empty list contract: %#v", emptyJSON)
	}
	contents := map[string][]byte{
		"World/Humans.md":         []byte("# Humans\r\n"),
		"World/Deep/Humans.md":    []byte("deep"),
		"Other/Humans.md":         []byte("other"),
		"Imagens/Árvore azul.png": {0x89, 'P', 'N', 'G', 0, 0xff},
		"empty.bin":               {},
	}
	commitReadHTTP(t, server, key, vault.ID, contents)
	list := readHTTPList(t, server, key, vault.ID)
	if list.VaultID != vault.ID || list.ServerRevision != 1 || len(list.Files) != len(contents) {
		t.Fatalf("list contract: %#v", list)
	}
	for i, file := range list.Files {
		if i > 0 && list.Files[i-1].Path >= file.Path {
			t.Fatalf("not sorted: %#v", list.Files)
		}
		content, exists := contents[file.Path]
		if !exists || file.Hash != readTestHash(content) || file.Size != int64(len(content)) || file.Revision != 1 {
			t.Fatalf("metadata differs from bytes: %#v", file)
		}
		for _, conditions := range []url.Values{
			{},
			{"expectedHash": {file.Hash}},
			{"expectedRevision": {strconv.FormatInt(file.Revision, 10)}},
			{"expectedHash": {file.Hash}, "expectedRevision": {strconv.FormatInt(file.Revision, 10)}},
		} {
			conditions.Set("vaultId", vault.ID)
			conditions.Set("path", file.Path)
			res := readRequest(server, key, http.MethodGet, "/v1/files/download?"+conditions.Encode(), nil)
			assertReadDownload(t, res, file, content)
		}
	}
	// Assert the exact wire shape: no host paths, content or historical metadata.
	res := readRequest(server, key, http.MethodGet, "/v1/files?vaultId="+vault.ID, nil)
	var wire struct {
		Files []map[string]any `json:"files"`
	}
	decodeReadJSON(t, res, &wire)
	for _, file := range wire.Files {
		if len(file) != 4 || file["path"] == nil || file["hash"] == nil || file["size"] == nil || file["revision"] == nil {
			t.Fatalf("unexpected file fields: %#v", file)
		}
	}
	token, err := store.CreateWebSession(context.Background(), user.ID, webSessionDuration)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/vault-dashboard/vaults/download?vaultId="+vault.ID, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	zipRes := httptest.NewRecorder()
	server.Routes().ServeHTTP(zipRes, req)
	if zipRes.Code != http.StatusOK || zipRes.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("ZIP response: %d %s", zipRes.Code, zipRes.Body)
	}
	archive, err := zip.NewReader(bytes.NewReader(zipRes.Body.Bytes()), int64(zipRes.Body.Len()))
	if err != nil || len(archive.File) != len(contents) {
		t.Fatalf("ZIP contents: %v", err)
	}
	for i, entry := range archive.File {
		if entry.Name != list.Files[i].Path {
			t.Fatalf("ZIP path differs: %q", entry.Name)
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || !bytes.Equal(data, contents[entry.Name]) {
			t.Fatalf("ZIP bytes differ: %q, %v", entry.Name, err)
		}
	}
}

func TestReadAPIValidation(t *testing.T) {
	server, store, user, vault := newTestServer(t)
	key := readTestKey(t, store, user.ID)
	commitReadHTTP(t, server, key, vault.ID, map[string][]byte{"note.md": []byte("content")})
	base := "/v1/files/download?vaultId=" + vault.ID + "&path=note.md"
	for _, query := range []string{
		"expectedHash=", "expectedHash=xyz", "expectedHash=" + strings.Repeat("A", 64),
		"expectedHash=" + strings.Repeat("0", 64) + "&expectedHash=" + strings.Repeat("0", 64),
		"expectedRevision=", "expectedRevision=-1", "expectedRevision=abc", "expectedRevision=1.0",
		"expectedRevision=9223372036854775808", "expectedRevision=%2B1", "expectedRevision=%201",
		"expectedRevision=1&expectedRevision=1", "expectedRevision=%zz",
	} {
		t.Run(query, func(t *testing.T) {
			assertReadStatus(t, readRequest(server, key, http.MethodGet, base+"&"+query, nil), http.StatusBadRequest, "BAD_REQUEST")
		})
	}
	for _, query := range []string{"expectedHash=" + strings.Repeat("0", 64), "expectedRevision=2", "expectedRevision=0"} {
		assertReadStatus(t, readRequest(server, key, http.MethodGet, base+"&"+query, nil), http.StatusConflict, "FILE_CHANGED")
	}
	for _, target := range []string{"/v1/files", "/v1/files?vaultId=", "/v1/files/download?vaultId=" + vault.ID, "/v1/files/download?vaultId=" + vault.ID + "&path=../secret", "/v1/files/download?path=note.md"} {
		assertReadStatus(t, readRequest(server, key, http.MethodGet, target, nil), http.StatusBadRequest, "BAD_REQUEST")
	}
	for _, target := range []string{"/v1/files?vaultId=" + vault.ID, base} {
		res := readRequest(server, key, http.MethodPost, target, nil)
		assertReadStatus(t, res, http.StatusMethodNotAllowed, "BAD_REQUEST")
		if res.Header().Get("Allow") != http.MethodGet {
			t.Fatalf("missing GET Allow header")
		}
	}
}

func TestReadAPIAuthenticationRotationAndIsolation(t *testing.T) {
	server, store, admin, adminVault := newTestServer(t)
	ctx := context.Background()
	adminKey := readTestKey(t, store, admin.ID)
	other, err := store.UpsertAllowedUser(ctx, "other@example.com", storage.UserRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	otherVault, err := store.CreateVault(ctx, other.ID, "Other")
	if err != nil {
		t.Fatal(err)
	}
	otherKey := readTestKey(t, store, other.ID)
	for _, owner := range []struct {
		key   string
		vault storage.Vault
	}{{adminKey, adminVault}, {otherKey, otherVault}} {
		commitReadHTTP(t, server, owner.key, owner.vault.ID, map[string][]byte{"note.md": []byte(owner.vault.ID)})
	}
	for _, tc := range []struct {
		key          string
		own, foreign storage.Vault
	}{{adminKey, adminVault, otherVault}, {otherKey, otherVault, adminVault}} {
		listRes := readRequest(server, tc.key, http.MethodGet, "/v1/vaults", nil)
		var vaults storage.VaultListResponse
		decodeReadJSON(t, listRes, &vaults)
		if listRes.Code != http.StatusOK || len(vaults.Vaults) != 1 || vaults.Vaults[0].ID != tc.own.ID {
			t.Fatalf("vault isolation: %#v", vaults)
		}
		for _, target := range []string{"/v1/files?vaultId=" + tc.foreign.ID, "/v1/files/download?vaultId=" + tc.foreign.ID + "&path=note.md"} {
			assertReadStatus(t, readRequest(server, tc.key, http.MethodGet, target, nil), http.StatusNotFound, "NOT_FOUND")
		}
	}
	targets := []string{"/v1/auth/check", "/v1/vaults", "/v1/files?vaultId=" + otherVault.ID, "/v1/files/download?vaultId=" + otherVault.ID + "&path=note.md"}
	for _, target := range targets {
		if res := readRequest(server, "", http.MethodGet, target, nil); res.Code != http.StatusUnauthorized {
			t.Fatalf("missing auth accepted: %s", target)
		}
		if res := readRequest(server, "invalid", http.MethodGet, target, nil); res.Code != http.StatusUnauthorized {
			t.Fatalf("invalid auth accepted: %s", target)
		}
	}
	newKey, err := store.RotateAPIKey(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if res := readRequest(server, otherKey, http.MethodGet, target, nil); res.Code != http.StatusUnauthorized {
			t.Fatalf("old key accepted: %s", target)
		}
		if res := readRequest(server, newKey, http.MethodGet, target, nil); res.Code != http.StatusOK {
			t.Fatalf("new key rejected: %s: %d %s", target, res.Code, res.Body)
		}
	}
	if err := store.SetUserStatus(ctx, other.ID, storage.UserStatusDisabled); err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if res := readRequest(server, newKey, http.MethodGet, target, nil); res.Code != http.StatusUnauthorized {
			t.Fatalf("disabled user accepted: %s", target)
		}
	}
	if err := store.SoftDeleteVault(ctx, admin.ID, adminVault.ID); err != nil {
		t.Fatal(err)
	}
	for _, vaultID := range []string{adminVault.ID, "vault_missing"} {
		for _, target := range []string{"/v1/files?vaultId=" + vaultID, "/v1/files/download?vaultId=" + vaultID + "&path=note.md"} {
			assertReadStatus(t, readRequest(server, adminKey, http.MethodGet, target, nil), http.StatusNotFound, "NOT_FOUND")
		}
	}
}

func TestReadAPISelectionAcrossSyncChanges(t *testing.T) {
	for _, change := range []string{"unrelated", "changed", "removed", "renamed", "same bytes new revision"} {
		t.Run(change, func(t *testing.T) {
			server, store, user, vault := newTestServer(t)
			key := readTestKey(t, store, user.ID)
			commitReadHTTP(t, server, key, vault.ID, map[string][]byte{"note.md": []byte("original")})
			selected := readHTTPList(t, server, key, vault.ID).Files[0]
			next := map[string][]byte{"note.md": []byte("original")}
			want, code := http.StatusConflict, "FILE_CHANGED"
			switch change {
			case "unrelated":
				next["other.bin"] = []byte{0, 0xff}
				want, code = http.StatusOK, ""
			case "changed":
				next["note.md"] = []byte("new")
			case "removed":
				delete(next, "note.md")
				want, code = http.StatusNotFound, "NOT_FOUND"
			case "renamed":
				delete(next, "note.md")
				next["new name.md"] = []byte("original")
				want, code = http.StatusNotFound, "NOT_FOUND"
			case "same bytes new revision":
				commitReadHTTP(t, server, key, vault.ID, map[string][]byte{"note.md": []byte("intermediate")})
			}
			begin := stageReadHTTP(t, server, key, vault.ID, next)
			events, unsubscribe := server.events.subscribe(vault.ID)
			defer unsubscribe()
			before, err := store.SyncStatus(context.Background(), user.ID, vault.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Staging remains invisible, and reads do not broadcast sync events.
			beforeList := readHTTPList(t, server, key, vault.ID)
			if len(beforeList.Files) != 1 || beforeList.Files[0] != selected && change != "same bytes new revision" {
				t.Fatalf("staging visible: %#v", beforeList)
			}
			path := selectedDownloadURL(vault.ID, selected)
			staged := readRequest(server, key, http.MethodGet, path, nil)
			if change != "same bytes new revision" {
				assertReadDownload(t, staged, selected, []byte("original"))
			} else {
				assertReadStatus(t, staged, http.StatusConflict, "FILE_CHANGED")
			}
			after, err := store.SyncStatus(context.Background(), user.ID, vault.ID)
			if err != nil || before != after {
				t.Fatalf("reads modified sync status: %#v -> %#v, %v", before, after, err)
			}
			select {
			case event := <-events:
				t.Fatalf("read emitted sync event: %s", event)
			default:
			}
			commitReadSessionHTTP(t, server, key, begin)
			res := readRequest(server, key, http.MethodGet, path, nil)
			if want == http.StatusOK {
				assertReadDownload(t, res, selected, []byte("original"))
			} else {
				assertReadStatus(t, res, want, code)
			}
			list := readHTTPList(t, server, key, vault.ID)
			if len(list.Files) != len(next) || list.ServerRevision <= selected.Revision {
				t.Fatalf("committed state: %#v", list)
			}
			for _, file := range list.Files {
				// Plugin requests without new conditions still download current bytes.
				legacy := "/v1/files/download?" + url.Values{"vaultId": {vault.ID}, "path": {file.Path}}.Encode()
				assertReadDownload(t, readRequest(server, key, http.MethodGet, legacy, nil), file, next[file.Path])
			}
		})
	}
}

func TestReadDownloadKeepsValidatedBlobDuringLaterCommit(t *testing.T) {
	server, store, user, vault := newTestServer(t)
	key := readTestKey(t, store, user.ID)
	commitReadHTTP(t, server, key, vault.ID, map[string][]byte{"note.md": []byte("original")})
	selected := readHTTPList(t, server, key, vault.ID).Files[0]
	begin := stageReadHTTP(t, server, key, vault.ID, map[string][]byte{"note.md": []byte("replacement with a different size")})
	res := httptest.NewRecorder()
	writer := &readCommitWriter{ResponseRecorder: res, afterHeader: func() {
		// A commit after validation must not replace the chosen response blob.
		commitReadSessionHTTP(t, server, key, begin)
	}}
	req := httptest.NewRequest(http.MethodGet, selectedDownloadURL(vault.ID, selected), nil)
	req.Header.Set("Authorization", "Bearer "+key)
	server.Routes().ServeHTTP(writer, req)
	assertReadDownload(t, res, selected, []byte("original"))
	assertReadStatus(t, readRequest(server, key, http.MethodGet, selectedDownloadURL(vault.ID, selected), nil), http.StatusConflict, "FILE_CHANGED")
	current := readHTTPList(t, server, key, vault.ID).Files[0]
	if current.Hash == selected.Hash || current.Revision <= selected.Revision {
		t.Fatalf("concurrent commit did not change the file: %#v", current)
	}
}

type readCommitWriter struct {
	*httptest.ResponseRecorder
	afterHeader func()
}

func (w *readCommitWriter) WriteHeader(code int) {
	w.ResponseRecorder.WriteHeader(code)
	if callback := w.afterHeader; callback != nil {
		w.afterHeader = nil
		callback()
	}
}

func readRequest(server *Server, key, method, target string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res := httptest.NewRecorder()
	server.Routes().ServeHTTP(res, req)
	return res
}

func readTestKey(t *testing.T, store *storage.Store, userID string) string {
	t.Helper()
	key, err := store.CurrentAPIKey(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func readTestHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func decodeReadJSON(t *testing.T, res *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(res.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response %s: %v", res.Body, err)
	}
}

func assertReadStatus(t *testing.T, res *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if res.Code != status || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response: %d, headers=%v, body=%s; want %d no-store", res.Code, res.Header(), res.Body, status)
	}
	if code != "" {
		var payload map[string]string
		decodeReadJSON(t, res, &payload)
		if payload["code"] != code || payload["message"] == "" || res.Header().Get("X-NoX-Sync-Hash") != "" {
			t.Fatalf("error contract: %#v, %v", payload, res.Header())
		}
	}
}

func assertReadDownload(t *testing.T, res *httptest.ResponseRecorder, file storage.DownloadResult, content []byte) {
	t.Helper()
	assertReadStatus(t, res, http.StatusOK, "")
	if !bytes.Equal(res.Body.Bytes(), content) || readTestHash(res.Body.Bytes()) != file.Hash || int64(res.Body.Len()) != file.Size {
		t.Fatalf("download bytes differ for %s", file.Path)
	}
	for name, want := range map[string]string{
		"X-NoX-Sync-Path": file.Path, "X-NoX-Sync-Hash": file.Hash,
		"X-NoX-Sync-Revision": strconv.FormatInt(file.Revision, 10),
		"Content-Length":      strconv.FormatInt(file.Size, 10), "Content-Type": "application/octet-stream",
	} {
		if got := res.Header().Get(name); got != want {
			t.Fatalf("%s = %q; want %q", name, got, want)
		}
	}
}

func readHTTPList(t *testing.T, server *Server, key, vaultID string) storage.FileList {
	t.Helper()
	res := readRequest(server, key, http.MethodGet, "/v1/files?vaultId="+vaultID, nil)
	assertReadStatus(t, res, http.StatusOK, "")
	var list storage.FileList
	decodeReadJSON(t, res, &list)
	return list
}

func selectedDownloadURL(vaultID string, file storage.DownloadResult) string {
	return "/v1/files/download?" + url.Values{
		"vaultId": {vaultID}, "path": {file.Path}, "expectedHash": {file.Hash},
		"expectedRevision": {strconv.FormatInt(file.Revision, 10)},
	}.Encode()
}

// These fixtures exercise the existing plugin HTTP flow, including upload/commit.
func stageReadHTTP(t *testing.T, server *Server, key, vaultID string, contents map[string][]byte) storage.BeginSyncResult {
	t.Helper()
	list := readHTTPList(t, server, key, vaultID)
	previous := map[string]int64{}
	for _, file := range list.Files {
		previous[file.Path] = file.Revision
	}
	var begin storage.BeginSyncResult
	readSyncJSON(t, server, key, "/v1/sync/begin", storage.BeginSyncRequest{VaultID: vaultID, ClientID: "read_test", ClientName: "Read test"}, &begin)
	manifest := storage.ManifestRequest{SessionID: begin.SessionID, ClientID: "read_test", VaultID: vaultID, LastKnownServerRevision: list.ServerRevision}
	for path, content := range contents {
		manifest.Files = append(manifest.Files, storage.ManifestFile{Path: path, Hash: readTestHash(content), Size: int64(len(content)), LastKnownRevision: previous[path]})
	}
	var plan storage.SyncPlan
	readSyncJSON(t, server, key, "/v1/sync/manifest", manifest, &plan)
	for _, action := range plan.Actions {
		if action.Type != storage.PlanActionUpload {
			continue
		}
		query := url.Values{"clientId": {"read_test"}, "path": {action.Path}, "hash": {action.ExpectedHash}, "size": {strconv.FormatInt(action.Size, 10)}}
		res := readRequest(server, key, http.MethodPut, "/v1/sync/upload/"+begin.SessionID+"?"+query.Encode(), bytes.NewReader(contents[action.Path]))
		if res.Code != http.StatusOK {
			t.Fatalf("plugin upload: %d %s", res.Code, res.Body)
		}
	}
	return begin
}

func commitReadSessionHTTP(t *testing.T, server *Server, key string, begin storage.BeginSyncResult) {
	t.Helper()
	var result storage.CommitResult
	readSyncJSON(t, server, key, "/v1/sync/commit", storage.CommitRequest{SessionID: begin.SessionID, ClientID: "read_test"}, &result)
}

func commitReadHTTP(t *testing.T, server *Server, key, vaultID string, contents map[string][]byte) {
	t.Helper()
	commitReadSessionHTTP(t, server, key, stageReadHTTP(t, server, key, vaultID, contents))
}

func readSyncJSON(t *testing.T, server *Server, key, target string, payload, dst any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	res := readRequest(server, key, http.MethodPost, target, bytes.NewReader(data))
	if res.Code != http.StatusOK {
		t.Fatalf("plugin flow %s: %d %s", target, res.Code, res.Body)
	}
	decodeReadJSON(t, res, dst)
}
