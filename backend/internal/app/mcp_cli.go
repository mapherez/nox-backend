package app

import (
	"context"

	noxmcp "github.com/mapherez/nox-mcp/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Human presentation is published only through NoX discovery. Tool schemas,
// execution and the standard tools/list projection remain independent of it.
func backendCLIDiscovery() noxmcp.DiscoveryOptions {
	return noxmcp.DiscoveryOptions{
		ServerID:    serviceName,
		Descriptors: backendCLIDescriptors(),
		// Preserve the default Suite view. NewSuite applies its existing
		// identity/scopes filter around this resolver.
		ResolveView: func(context.Context, *mcp.RequestExtra, []*mcp.Tool) (string, []string, []string, noxmcp.JSON, error) {
			return "suite", nil, nil, nil, nil
		},
	}
}

func backendCLIDescriptors() map[string]noxmcp.JSON {
	item := func(label, group string, order int) noxmcp.JSON {
		navigation := noxmcp.JSON{"label": label, "order": order}
		if group != "" {
			navigation["group"] = group
		}
		return noxmcp.JSON{"version": "1.0", "navigation": navigation}
	}
	descriptors := map[string]noxmcp.JSON{
		"backend_health":        item("Service status", "", 10),
		"backend_info":          item("Service information", "", 20),
		"backend_whoami":        item("Current account", "", 30),
		"backend_vault_list":    item("List", "Vaults", 40),
		"backend_vault_create":  item("Create", "Vaults", 50),
		"backend_vault_status":  item("Check status", "Vaults", 60),
		"backend_vault_delete":  item("Delete", "Vaults", 70),
		"backend_vault_restore": item("Restore", "Vaults", 80),
		"backend_vault_purge":   item("Permanently delete", "Vaults", 90),
		"backend_file_list":     item("List", "Files", 100),
	}
	descriptors["backend_vault_create"]["parameters"] = noxmcp.JSON{
		"/name": noxmcp.JSON{"control": "text", "order": 10},
	}
	for _, name := range []string{"backend_vault_status", "backend_vault_delete", "backend_vault_restore", "backend_vault_purge", "backend_file_list"} {
		items := "/vaults"
		if name == "backend_vault_restore" || name == "backend_vault_purge" {
			items = "/deletedVaults"
		}
		descriptors[name]["requires"] = []string{"dynamic-options"}
		descriptors[name]["parameters"] = noxmcp.JSON{
			"/vaultId": noxmcp.JSON{
				"order": 10,
				"options": noxmcp.JSON{
					"tool": "backend_vault_list", "itemsPointer": items,
					"valuePointer": "/vaultId", "labelPointer": "/name",
				},
			},
		}
	}
	descriptors["backend_vault_delete"]["requires"] = []string{"dynamic-options", "confirmation"}
	descriptors["backend_vault_delete"]["confirmation"] = noxmcp.JSON{
		"mode": "boolean", "message": "Delete the selected vault? Its data will remain available for restoration.",
	}
	descriptors["backend_vault_purge"]["requires"] = []string{"dynamic-options", "confirmation"}
	descriptors["backend_vault_purge"]["confirmation"] = noxmcp.JSON{
		"mode": "typed", "matchPointer": "/vaultId",
		"message": "Permanently delete the selected vault and its unshared data? This action cannot be undone. Type the vault ID to confirm.",
	}
	// Confirmation is local to the human interaction. Deliberately no
	// bindPointer: the existing tools accept no extra confirmation argument.
	return descriptors
}
