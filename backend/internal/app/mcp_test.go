package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpKeyTransport struct {
	base http.RoundTripper
	key  string
}

func (rt mcpKeyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if rt.key != "" {
		r.Header.Set("Authorization", "Bearer "+rt.key)
	}
	return rt.base.RoundTrip(r)
}

func connectBackendMCP(t *testing.T, httpServer *httptest.Server, key string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "backend-integration-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp", DisableStandaloneSSE: true, MaxRetries: -1,
		HTTPClient: &http.Client{Transport: mcpKeyTransport{httpServer.Client().Transport, key}},
	}, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		t.Fatalf("MCP initialize: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close MCP: %v", err)
		}
	})
	return session
}

func callBackendMCP(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, code string) map[string]any {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("MCP %s protocol error: %v", name, err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if result.IsError != (code != "") || code != "" && (value["code"] != code || value["message"] == "" || value["retryable"] != false) {
		t.Fatalf("MCP %s: isError=%v value=%s; want code %q", name, result.IsError, data, code)
	}
	if len(result.Content) != 1 {
		t.Fatalf("MCP %s missing text content: %+v", name, result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	var textValue map[string]any
	if !ok || json.Unmarshal([]byte(text.Text), &textValue) != nil || !reflect.DeepEqual(value, textValue) {
		t.Fatalf("MCP text and structured content differ: %+v", result)
	}
	return value
}

func TestMCPCatalogAndPublicTools(t *testing.T) {
	server, _, _, _ := newTestServer(t)
	server.cfg.Version = "resolved-runtime-version"
	httpServer := httptest.NewServer(server.Routes())
	defer httpServer.Close()
	session := connectBackendMCP(t, httpServer, "")
	initialized := session.InitializeResult()
	if initialized.ServerInfo.Name != serviceName || initialized.ServerInfo.Version != server.cfg.Version || initialized.ProtocolVersion != "2025-11-25" {
		t.Fatalf("initialize identity: %+v", initialized)
	}
	catalog, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		cli                               string
		readOnly, destructive, idempotent bool
	}{
		"backend_health":        {"health", true, false, true},
		"backend_info":          {"info", true, false, true},
		"backend_whoami":        {"whoami", true, false, true},
		"backend_vault_list":    {"vault list", true, false, true},
		"backend_vault_create":  {"vault create", false, false, false},
		"backend_vault_delete":  {"vault delete", false, true, false},
		"backend_vault_restore": {"vault restore", false, false, false},
		"backend_vault_purge":   {"vault purge", false, true, false},
		"backend_vault_status":  {"vault status", true, false, true},
		"backend_file_list":     {"file list", true, false, true},
	}
	if len(catalog.Tools) != 10 {
		t.Fatalf("tools/list: got %d tools", len(catalog.Tools))
	}
	seenNames, seenPaths := map[string]bool{}, map[string]bool{}
	for _, tool := range catalog.Tools {
		expected, ok := want[tool.Name]
		if !ok || seenNames[tool.Name] {
			t.Fatalf("unexpected/duplicate tool: %q", tool.Name)
		}
		seenNames[tool.Name] = true
		cli, ok := tool.Meta["cli"].(string)
		if !ok || cli == "" || cli != expected.cli || seenPaths[cli] || strings.HasPrefix(cli, "backend ") || len(tool.Meta) != 1 {
			t.Fatalf("wire _meta.cli for %s: %#v", tool.Name, tool.Meta)
		}
		seenPaths[cli] = true
		a := tool.Annotations
		if a == nil || a.DestructiveHint == nil || a.ReadOnlyHint != expected.readOnly || *a.DestructiveHint != expected.destructive || a.IdempotentHint != expected.idempotent {
			t.Fatalf("annotations for %s: %+v", tool.Name, a)
		}
		if tool.Description == "" {
			t.Fatalf("missing description: %s", tool.Name)
		}
		for _, wireSchema := range []any{tool.InputSchema, tool.OutputSchema} {
			data, err := json.Marshal(wireSchema)
			var schema jsonschema.Schema
			if err != nil || json.Unmarshal(data, &schema) != nil || schema.Type != "object" {
				t.Fatalf("missing/invalid object schema: %s: %s", tool.Name, data)
			}
			if _, err := schema.Resolve(nil); err != nil {
				t.Fatalf("invalid wire schema: %s: %v", tool.Name, err)
			}
		}
	}
	for _, tc := range []struct{ tool, path string }{{"backend_health", "/v1/health"}, {"backend_info", "/v1/info"}} {
		got := callBackendMCP(t, session, tc.tool, nil, "")
		var want map[string]any
		decodeReadJSON(t, readRequest(server, "", "GET", tc.path, nil), &want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("HTTP/MCP %s differ: %#v, %#v", tc.tool, got, want)
		}
	}
	for _, method := range []string{"GET", "DELETE"} {
		res, err := httpServer.Client().Do(mustMCPRequest(t, method, httpServer.URL+"/mcp"))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Allow") != "POST" {
			t.Fatalf("unexpected %s /mcp: %d", method, res.StatusCode)
		}
	}
}

func mustMCPRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestMCPRequiresCLIMapping(t *testing.T) {
	_, err := newBackendMCP("test", []noxmcp.Tool{{Name: "new_unmapped_tool"}})
	if err == nil || !strings.Contains(err.Error(), "new_unmapped_tool") || !strings.Contains(err.Error(), "_meta.cli") {
		t.Fatalf("missing mapping error: %v", err)
	}
}

func TestMCPAuthenticationAndOwnership(t *testing.T) {
	server, store, admin, adminVault := newTestServer(t)
	ctx := context.Background()
	other, err := store.UpsertAllowedUser(ctx, "mcp-user@example.com", storage.UserRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	otherVault, err := store.CreateVault(ctx, other.ID, "Other user's vault")
	if err != nil {
		t.Fatal(err)
	}
	adminKey, otherKey := readTestKey(t, store, admin.ID), readTestKey(t, store, other.ID)
	httpServer := httptest.NewServer(server.Routes())
	defer httpServer.Close()
	anonymous := connectBackendMCP(t, httpServer, "")
	invalid := connectBackendMCP(t, httpServer, "invalid")
	protected := []struct {
		name string
		args map[string]any
	}{
		{"backend_whoami", nil}, {"backend_vault_list", nil},
		{"backend_vault_create", map[string]any{"name": "blocked"}},
		{"backend_vault_delete", map[string]any{"vaultId": adminVault.ID}},
		{"backend_vault_restore", map[string]any{"vaultId": adminVault.ID}},
		{"backend_vault_purge", map[string]any{"vaultId": adminVault.ID}},
		{"backend_vault_status", map[string]any{"vaultId": adminVault.ID}},
		{"backend_file_list", map[string]any{"vaultId": adminVault.ID}},
	}
	for _, tool := range protected {
		callBackendMCP(t, anonymous, tool.name, tool.args, "AUTH_REQUIRED")
		callBackendMCP(t, invalid, tool.name, tool.args, "AUTH_FAILED")
	}
	for _, tool := range []string{"backend_health", "backend_info"} {
		callBackendMCP(t, invalid, tool, nil, "")
	}
	adminSession, otherSession := connectBackendMCP(t, httpServer, adminKey), connectBackendMCP(t, httpServer, otherKey)
	for _, owner := range []struct {
		session      *mcp.ClientSession
		user         storage.User
		own, foreign storage.Vault
	}{
		{adminSession, admin, adminVault, otherVault}, {otherSession, other, otherVault, adminVault},
	} {
		identity := callBackendMCP(t, owner.session, "backend_whoami", nil, "")
		if !reflect.DeepEqual(identity, map[string]any{"user": owner.user.Email, "role": owner.user.Role}) {
			t.Fatalf("identity: %#v", identity)
		}
		listed := callBackendMCP(t, owner.session, "backend_vault_list", nil, "")
		vaults := listed["vaults"].([]any)
		if len(vaults) != 1 || vaults[0].(map[string]any)["vaultId"] != owner.own.ID {
			t.Fatalf("ownership list: %#v", listed)
		}
		for _, tool := range []string{"backend_vault_status", "backend_file_list", "backend_vault_delete", "backend_vault_restore", "backend_vault_purge"} {
			callBackendMCP(t, owner.session, tool, map[string]any{"vaultId": owner.foreign.ID}, "NOT_FOUND")
		}
	}
	// Restore/purge must also enforce ownership when the foreign vault is in
	// the expected deleted state, rather than passing only because it is active.
	foreignArgs := map[string]any{"vaultId": otherVault.ID}
	callBackendMCP(t, otherSession, "backend_vault_delete", foreignArgs, "")
	for _, tool := range []string{"backend_vault_restore", "backend_vault_purge"} {
		callBackendMCP(t, adminSession, tool, foreignArgs, "NOT_FOUND")
	}
	adminList := callBackendMCP(t, adminSession, "backend_vault_list", nil, "")
	if _, exposed := adminList["deletedVaults"]; exposed {
		t.Fatalf("foreign deleted vault exposed: %#v", adminList)
	}
	callBackendMCP(t, otherSession, "backend_vault_restore", foreignArgs, "")
	// An initialized client must not retain authentication after rotation/disable.
	newKey, err := store.RotateAPIKey(ctx, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	callBackendMCP(t, otherSession, "backend_whoami", nil, "AUTH_FAILED")
	rotated := connectBackendMCP(t, httpServer, newKey)
	callBackendMCP(t, rotated, "backend_whoami", nil, "")
	if err := store.SetUserStatus(ctx, other.ID, storage.UserStatusDisabled); err != nil {
		t.Fatal(err)
	}
	for _, tool := range protected {
		callBackendMCP(t, rotated, tool.name, tool.args, "AUTH_FAILED")
	}
	callBackendMCP(t, adminSession, "backend_whoami", nil, "")
}

func TestMCPVaultLifecycleSharesHTTPState(t *testing.T) {
	server, store, user, _ := newTestServer(t)
	key := readTestKey(t, store, user.ID)
	httpServer := httptest.NewServer(server.Routes())
	defer httpServer.Close()
	session := connectBackendMCP(t, httpServer, key)
	for _, name := range []string{" ", strings.Repeat("x", 161), strings.Repeat("é", 81)} {
		callBackendMCP(t, session, "backend_vault_create", map[string]any{"name": name}, "BAD_REQUEST")
	}
	created := callBackendMCP(t, session, "backend_vault_create", map[string]any{"name": "  MCP Vault  "}, "")
	id := created["vaultId"].(string)
	if created["name"] != "MCP Vault" || created["revision"] != float64(0) || created["status"] != "ACTIVE" || created["sizeBytes"] != float64(0) {
		t.Fatalf("created: %#v", created)
	}
	var listed storage.VaultListResponse
	decodeReadJSON(t, readRequest(server, key, "GET", "/v1/vaults", nil), &listed)
	found := false
	for _, v := range listed.Vaults {
		if v.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("MCP-created vault missing in HTTP: %+v", listed)
	}
	args := map[string]any{"vaultId": id}
	files := callBackendMCP(t, session, "backend_file_list", args, "")
	if !reflect.DeepEqual(files["files"], []any{}) {
		t.Fatalf("empty files: %#v", files)
	}
	assertClientResponse(t, readRequest(server, key, "DELETE", "/v1/vaults?vaultId="+id, nil), 200, "")
	callBackendMCP(t, session, "backend_vault_delete", args, "NOT_FOUND")
	deleted := callBackendMCP(t, session, "backend_vault_list", nil, "")
	deletedVaults := deleted["deletedVaults"].([]any)
	if len(deletedVaults) != 1 || deletedVaults[0].(map[string]any)["vaultId"] != id || deletedVaults[0].(map[string]any)["deletedAt"] == nil {
		t.Fatalf("deleted list: %#v", deleted)
	}
	for _, tool := range []string{"backend_file_list", "backend_vault_status"} {
		callBackendMCP(t, session, tool, args, "NOT_FOUND")
	}
	callBackendMCP(t, session, "backend_vault_restore", args, "")
	callBackendMCP(t, session, "backend_vault_restore", args, "NOT_FOUND")
	status := callBackendMCP(t, session, "backend_vault_status", args, "")
	var httpStatus map[string]any
	decodeReadJSON(t, readRequest(server, key, "GET", "/v1/status?vaultId="+id, nil), &httpStatus)
	if !reflect.DeepEqual(status, httpStatus) {
		t.Fatalf("HTTP/MCP status differ: %#v %#v", status, httpStatus)
	}
	callBackendMCP(t, session, "backend_vault_purge", args, "NOT_FOUND")
	callBackendMCP(t, session, "backend_vault_delete", args, "")
	callBackendMCP(t, session, "backend_vault_purge", args, "")
	callBackendMCP(t, session, "backend_vault_purge", args, "NOT_FOUND")
	callBackendMCP(t, session, "backend_vault_restore", args, "NOT_FOUND")
	decodeReadJSON(t, readRequest(server, key, "GET", "/v1/vaults", nil), &listed)
	if len(listed.DeletedVaults) != 0 {
		t.Fatalf("purged vault remains in HTTP: %+v", listed)
	}
	for _, tool := range []string{"backend_file_list", "backend_vault_status", "backend_vault_delete", "backend_vault_restore", "backend_vault_purge"} {
		callBackendMCP(t, session, tool, map[string]any{"vaultId": " "}, "BAD_REQUEST")
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"backend_health", map[string]any{"apiKey": key}},
		{"backend_vault_create", map[string]any{}},
		{"backend_vault_create", map[string]any{"name": 123}},
		{"backend_file_list", map[string]any{}},
		{"backend_file_list", map[string]any{"vaultId": id, "extra": true}},
	} {
		callBackendMCP(t, session, tc.tool, tc.args, "INVALID_INPUT")
	}
}

func TestMCPFileCommittedStateAndStaleStatusBroadcast(t *testing.T) {
	server, store, user, vault := newTestServer(t)
	key := readTestKey(t, store, user.ID)
	commitReadHTTP(t, server, key, vault.ID, map[string][]byte{"z.bin": {}, "Árvore/nota.md": []byte("original"), "a.md": []byte("a")})
	httpServer := httptest.NewServer(server.Routes())
	defer httpServer.Close()
	session := connectBackendMCP(t, httpServer, key)
	args := map[string]any{"vaultId": vault.ID}
	assertSameFiles := func() {
		t.Helper()
		var want map[string]any
		decodeReadJSON(t, readRequest(server, key, "GET", "/v1/files?vaultId="+vault.ID, nil), &want)
		got := callBackendMCP(t, session, "backend_file_list", args, "")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("HTTP/MCP files differ: %#v %#v", got, want)
		}
		for _, f := range got["files"].([]any) {
			if len(f.(map[string]any)) != 4 {
				t.Fatalf("unexpected file fields: %#v", f)
			}
		}
	}
	assertSameFiles()
	begin := stageReadHTTP(t, server, key, vault.ID, map[string][]byte{"a.md": []byte("new staged bytes"), "new.md": []byte("new")})
	events, unsubscribe := server.events.subscribe(vault.ID)
	defer unsubscribe()
	assertSameFiles()
	staged := callBackendMCP(t, session, "backend_file_list", args, "")
	if staged["serverRevision"] != float64(1) || len(staged["files"].([]any)) != 3 {
		t.Fatalf("staging leaked: %#v", staged)
	}
	select {
	case event := <-events:
		t.Fatalf("file read broadcast: %s", event)
	default:
	}
	commitReadSessionHTTP(t, server, key, begin)
	select {
	case <-events: // The HTTP commit legitimately broadcasts the new status.
	case <-time.After(time.Second):
		t.Fatal("HTTP commit did not broadcast status")
	}
	assertSameFiles()
	// Expire a real sync lock in the temporary SQLite fixture, without waiting
	// two minutes or adding test-specific hooks to production code.
	if _, err := store.BeginSync(context.Background(), user.ID, storage.BeginSyncRequest{VaultID: vault.ID, ClientID: "stale-client", ClientName: "Stale client"}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", store.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE sync_locks SET expires_at = ? WHERE vault_id = ?", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), vault.ID); err != nil {
		t.Fatal(err)
	}
	assertSameFiles()
	var state string
	if err := db.QueryRow("SELECT status FROM sync_locks WHERE vault_id = ?", vault.ID).Scan(&state); err != nil || state != storage.SyncStateSyncing {
		t.Fatalf("file listing refreshed lock: %s %v", state, err)
	}
	status := callBackendMCP(t, session, "backend_vault_status", args, "")
	if status["sync"].(map[string]any)["state"] != storage.SyncStateStaleLock {
		t.Fatalf("lock was not reaped: %#v", status)
	}
	select {
	case event := <-events:
		var payload map[string]any
		if json.Unmarshal(event, &payload) != nil || !reflect.DeepEqual(payload, status) {
			t.Fatalf("stale broadcast differs: %s %#v", event, status)
		}
	case <-time.After(time.Second):
		t.Fatal("MCP status did not broadcast stale-lock refresh")
	}
	var httpStatus map[string]any
	decodeReadJSON(t, readRequest(server, key, "GET", "/v1/status?vaultId="+vault.ID, nil), &httpStatus)
	if !reflect.DeepEqual(httpStatus, status) {
		t.Fatalf("stale HTTP/MCP status differ: %#v %#v", httpStatus, status)
	}
	select {
	case event := <-events:
		t.Fatalf("duplicate stale broadcast: %s", event)
	default:
	}
}

func TestMCPInternalErrorsAreSanitized(t *testing.T) {
	server, store, user, _ := newTestServer(t)
	key := readTestKey(t, store, user.ID)
	httpServer := httptest.NewServer(server.Routes())
	defer httpServer.Close()
	session := connectBackendMCP(t, httpServer, key)
	// Corrupt only the fixture's vault table: API-key auth still succeeds,
	// while the real storage operation returns an internal SQL error.
	db, err := sql.Open("sqlite", store.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("DROP TABLE vaults"); err != nil {
		t.Fatal(err)
	}
	got := callBackendMCP(t, session, "backend_vault_list", nil, "SERVER_ERROR")
	if got["message"] != "Backend operation failed." || len(got) != 3 {
		t.Fatalf("internal error exposed: %#v", got)
	}
	if _, err := db.Exec("DROP TABLE api_keys"); err != nil {
		t.Fatal(err)
	}
	got = callBackendMCP(t, session, "backend_whoami", nil, "SERVER_ERROR")
	if got["message"] != "Failed to validate API key." || len(got) != 3 {
		t.Fatalf("internal auth error exposed: %#v", got)
	}
}

func TestMCPPreservesInt64RevisionsOnWire(t *testing.T) {
	server, store, user, vault := newTestServer(t)
	key := readTestKey(t, store, user.ID)
	const revision int64 = 9007199254740993 // Greater than float64's exact integer range.
	db, err := sql.Open("sqlite", store.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE vaults SET revision = ? WHERE id = ?", revision, vault.ID); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Routes())
	defer httpServer.Close()
	connectBackendMCP(t, httpServer, key) // Exercise initialize before the raw wire assertions.
	for _, tool := range []string{"backend_vault_status", "backend_file_list"} {
		body, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": map[string]any{"vaultId": vault.ID}},
		})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest("POST", httpServer.URL+"/mcp", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := httpServer.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Result struct {
				IsError           bool `json:"isError"`
				StructuredContent struct {
					ServerRevision int64 `json:"serverRevision"`
				} `json:"structuredContent"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		err = json.NewDecoder(res.Body).Decode(&wire)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || wire.Result.IsError || wire.Result.StructuredContent.ServerRevision != revision || len(wire.Result.Content) != 1 {
			t.Fatalf("%s int64 wire result: %+v, status=%d, error=%v", tool, wire, res.StatusCode, err)
		}
		var text struct {
			ServerRevision int64 `json:"serverRevision"`
		}
		if json.Unmarshal([]byte(wire.Result.Content[0].Text), &text) != nil || text.ServerRevision != revision {
			t.Fatalf("%s text revision lost precision: %+v", tool, wire)
		}
	}
}
