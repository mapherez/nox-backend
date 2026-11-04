package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	noxmcp "github.com/mapherez/nox-mcp/go"
)

func backendCLITestDefinitions(t *testing.T, f *backendSuiteFixture, token string) map[string]noxmcp.JSON {
	t.Helper()
	cache, err := noxmcp.NewDiscoveryCache(f.backend.URL+backendSuiteMCPPath, "cli-test")
	if err != nil {
		t.Fatal(err)
	}
	definitionRequests := 0
	request := func(_ context.Context, method string, params noxmcp.JSON) (noxmcp.JSON, error) {
		if method == "nox/tools/definitions" {
			definitionRequests++
		}
		status, raw := f.rpc(t, token, suiteRPC(method, params), map[string]string{"Mcp-Protocol-Version": noxmcp.DiscoveryProtocol, "Mcp-Method": method})
		suiteHTTPStatus(t, status, 200, raw)
		var envelope struct {
			Result noxmcp.JSON `json:"result"`
			Error  any         `json:"error"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Error != nil || envelope.Result == nil {
			t.Fatalf("%s returned no successful result: %s (%v)", method, raw, err)
		}
		return envelope.Result, nil
	}
	for range 2 {
		if err := cache.Sync(context.Background(), request); err != nil {
			t.Fatal("sync CLI definitions", err)
		}
	}
	if definitionRequests != 1 {
		t.Fatalf("unchanged CLI definitions fetched %d times", definitionRequests)
	}
	return cache.Definitions()
}

func TestBackendCLIDiscoveryPublication(t *testing.T) {
	f := backendSuiteServer(t)
	for _, tc := range []struct {
		name, key string
		count     int
	}{{"admin", f.adminKey, 10}, {"reader", f.memberKey, 5}} {
		t.Run(tc.name, func(t *testing.T) {
			token := f.token(t, tc.key, false)
			definitions := backendCLITestDefinitions(t, f, token)
			if len(definitions) != tc.count {
				t.Fatalf("discovery has %d tools, want %d", len(definitions), tc.count)
			}
			listed, err := f.connect(t, token).ListTools(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(listed.Tools) != tc.count {
				t.Fatalf("standard catalog has %d tools, want %d", len(listed.Tools), tc.count)
			}
			for _, tool := range listed.Tools {
				definition := definitions[tool.Name]
				cli, ok := definition["cli"].(map[string]any)
				if !ok {
					t.Fatal("missing CLI descriptor", tool.Name)
				}
				interpreted, err := noxmcp.InterpretCLI(cli)
				if err != nil {
					t.Fatal("invalid CLI descriptor", tool.Name, err)
				}
				navigation := interpreted["navigation"].(map[string]any)
				if navigation["label"] == "" || navigation["order"] == nil {
					t.Fatal("missing menu label/order", tool.Name)
				}
				canonical := definition["tool"].(map[string]any)
				meta := canonical["_meta"].(map[string]any)
				if meta["cli"] != cliPaths[tool.Name] || meta["nox/authorization"] == nil {
					t.Fatal("canonical metadata changed", tool.Name)
				}
				for _, key := range []string{"cli", "navigation", "parameters", "confirmation"} {
					if _, exposed := tool.Meta[key]; exposed {
						t.Fatal("human menu data leaked into tools/list", tool.Name, key)
					}
				}
				// The only difference from the standard tool is reserved CLI metadata.
				delete(meta, "cli")
				data, err := json.Marshal(tool)
				if err != nil {
					t.Fatal(err)
				}
				var standard map[string]any
				if err := json.Unmarshal(data, &standard); err != nil || !reflect.DeepEqual(canonical, standard) {
					t.Fatal("standard tool schemas/annotations/policy differ from canonical definition", tool.Name, err)
				}
				if params, ok := interpreted["parameters"].(map[string]any); ok {
					properties := standard["inputSchema"].(map[string]any)["properties"].(map[string]any)
					for pointer, value := range params {
						if properties[strings.TrimPrefix(pointer, "/")] == nil {
							t.Fatal("menu parameter absent from input schema", tool.Name, pointer)
						}
						parameter := value.(map[string]any)
						if options, ok := parameter["options"].(map[string]any); ok {
							if definitions[options["tool"].(string)] == nil {
								t.Fatal("selector depends on a tool hidden from this user", tool.Name)
							}
						}
					}
				}
			}
		})
	}
}

func TestBackendCLIVaultSelectionLifecycle(t *testing.T) {
	f := backendSuiteServer(t)
	token := f.token(t, f.adminKey, false)
	definitions := backendCLITestDefinitions(t, f, token)
	session := f.connect(t, token)
	selector := func(name string) []map[string]any {
		t.Helper()
		cli := definitions[name]["cli"].(map[string]any)
		params := cli["parameters"].(map[string]any)
		options := params["/vaultId"].(map[string]any)["options"].(map[string]any)
		if options["tool"] != "backend_vault_list" || options["valuePointer"] != "/vaultId" || options["labelPointer"] != "/name" {
			t.Fatal("selector does not use the existing vault list result", name, options)
		}
		result := callBackendMCP(t, session, options["tool"].(string), nil, "")
		items := options["itemsPointer"].(string)
		want := "/vaults"
		if name == "backend_vault_restore" || name == "backend_vault_purge" {
			want = "/deletedVaults"
		}
		if items != want {
			t.Fatal("wrong vault lifecycle source", name, items)
		}
		var rows []map[string]any
		if values, ok := result[strings.TrimPrefix(items, "/")].([]any); ok {
			for _, value := range values {
				rows = append(rows, value.(map[string]any))
			}
		}
		return rows
	}
	contains := func(rows []map[string]any, id string) bool {
		for _, row := range rows {
			if row["vaultId"] == id {
				return true
			}
		}
		return false
	}
	// Missing optional deletedVaults represents no choices, not a new API/error.
	if len(selector("backend_vault_restore")) != 0 || len(selector("backend_vault_purge")) != 0 {
		t.Fatal("unexpected initial deleted vaults")
	}
	first := callBackendMCP(t, session, "backend_vault_create", map[string]any{"name": "Same name"}, "")
	second := callBackendMCP(t, session, "backend_vault_create", map[string]any{"name": "Same name"}, "")
	id := first["vaultId"].(string)
	otherID := second["vaultId"].(string)
	foreign, err := f.store.CreateVault(context.Background(), f.member.ID, "Same name")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backend_vault_status", "backend_vault_delete", "backend_file_list"} {
		rows := selector(name)
		if id == otherID || !contains(rows, id) || !contains(rows, otherID) || contains(rows, foreign.ID) {
			t.Fatal("selection loses IDs for duplicate names or exposes foreign vaults", name)
		}
	}
	for _, name := range []string{"backend_vault_delete", "backend_vault_purge"} {
		cli := definitions[name]["cli"].(map[string]any)
		confirmation := cli["confirmation"].(map[string]any)
		if _, binds := confirmation["bindPointer"]; binds {
			t.Fatal("confirmation must not add a tool argument", name)
		}
		if name == "backend_vault_delete" && confirmation["mode"] != "boolean" || name == "backend_vault_purge" && (confirmation["mode"] != "typed" || confirmation["matchPointer"] != "/vaultId") {
			t.Fatal("missing recoverable/irreversible confirmation", name)
		}
	}
	args := map[string]any{"vaultId": id}
	callBackendMCP(t, session, "backend_vault_delete", args, "")
	if contains(selector("backend_vault_delete"), id) || !contains(selector("backend_vault_restore"), id) {
		t.Fatal("deleted vault not moved between selector sources")
	}
	callBackendMCP(t, session, "backend_vault_status", args, "NOT_FOUND")
	callBackendMCP(t, session, "backend_vault_restore", args, "")
	if !contains(selector("backend_vault_status"), id) || contains(selector("backend_vault_purge"), id) {
		t.Fatal("restored vault not moved between selector sources")
	}
	callBackendMCP(t, session, "backend_file_list", args, "")
	callBackendMCP(t, session, "backend_vault_delete", args, "")
	callBackendMCP(t, session, "backend_vault_purge", args, "")
	if contains(selector("backend_vault_purge"), id) || !contains(selector("backend_vault_status"), otherID) {
		t.Fatal("purge removed the wrong vault or left a selectable stale entry")
	}
}
