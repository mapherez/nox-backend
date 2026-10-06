package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

var cliPaths = map[string]string{
	"backend_health":        "health",
	"backend_info":          "info",
	"backend_whoami":        "whoami",
	"backend_vault_list":    "vault list",
	"backend_vault_create":  "vault create",
	"backend_vault_delete":  "vault delete",
	"backend_vault_restore": "vault restore",
	"backend_vault_purge":   "vault purge",
	"backend_vault_status":  "vault status",
	"backend_file_list":     "file list",
}

type mcpIdentityKey struct{}
type mcpIdentity struct {
	user storage.User
	err  *apiAuthError
}
type emptyInput struct{}
type vaultInput struct {
	VaultID string `json:"vaultId" jsonschema:"ID of a vault owned by the authenticated user."`
}
type identityResponse struct {
	User string `json:"user"`
	Role string `json:"role"`
}
type okResponse struct {
	OK bool `json:"ok"`
}

func (s *Server) mcpHandler() (http.Handler, error) {
	tools, err := s.mcpTools()
	if err != nil {
		return nil, err
	}
	runtime, err := newBackendMCP(s.cfg.Version, tools)
	if err != nil {
		return nil, err
	}
	handler := runtime.HTTPHandler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requireMethod(w, r, http.MethodPost) {
			return
		}
		// The SDK's RequireBearerToken middleware rejects anonymous requests and
		// writes HTTP/OAuth errors. This endpoint has public tools and needs
		// structured per-tool AUTH_REQUIRED/AUTH_FAILED errors instead. Keep only
		// the resolved identity/error in context, never the credential itself.
		user, authErr := s.apiUser(r.Context(), authToken(r))
		ctx := context.WithValue(r.Context(), mcpIdentityKey{}, mcpIdentity{user, authErr})
		handler.ServeHTTP(w, r.WithContext(ctx))
	}), nil
}

func newBackendMCP(version string, tools []noxmcp.Tool) (*noxmcp.Runtime, error) {
	paths := map[string]bool{}
	for i := range tools {
		path := strings.TrimSpace(cliPaths[tools[i].Name])
		if path == "" || strings.HasPrefix(path, "backend ") || paths[path] {
			return nil, fmt.Errorf("tool %q requires a unique app-local _meta.cli mapping", tools[i].Name)
		}
		paths[path] = true
		tools[i].Meta = map[string]any{"cli": path}
	}
	return noxmcp.New(noxmcp.Options{AppID: serviceName, Name: serviceName, Version: version, Tools: tools})
}

// backendTool adapts typed backend inputs/results to the official runtime's
// map interface. Schemas come from the same JSON types used for execution.
func backendTool[I, O any](definition noxmcp.Tool, authenticated bool, execute func(noxmcp.ExecutionContext, storage.User, I) (O, error)) (noxmcp.Tool, error) {
	var err error
	definition.InputSchema, err = jsonschema.For[I](nil)
	if err != nil {
		return definition, err
	}
	definition.OutputSchema, err = jsonschema.For[O](nil)
	if err != nil {
		return definition, err
	}
	definition.Execute = func(ctx noxmcp.ExecutionContext, args map[string]any) (map[string]any, error) {
		var user storage.User
		if authenticated {
			identity, ok := ctx.Value(mcpIdentityKey{}).(mcpIdentity)
			if !ok {
				return nil, &noxmcp.Error{Code: "AUTH_REQUIRED", Message: "NoX Sync API key is required."}
			}
			if identity.err != nil {
				return nil, &noxmcp.Error{Code: identity.err.code, Message: identity.err.message}
			}
			user = identity.user
		}
		var input I
		data, err := json.Marshal(args)
		if err == nil {
			err = json.Unmarshal(data, &input)
		}
		if err != nil {
			return nil, &noxmcp.Error{Code: "BAD_REQUEST", Message: "Invalid tool arguments."}
		}
		if !definition.ReadOnly {
			if err := ctx.CheckActive(); err != nil {
				return nil, err
			}
		}
		output, err := execute(ctx, user, input)
		if err != nil {
			return nil, mcpBackendError(err)
		}
		data, err = json.Marshal(output)
		var result map[string]any
		if err == nil {
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber() // Preserve int64 revisions/sizes in the wire result.
			err = decoder.Decode(&result)
			mcpResultIntegers(result)
		}
		if err != nil {
			return nil, mcpBackendError(err)
		}
		return result, nil
	}
	return definition, nil
}

// The schema validator expects native numeric types. Every numeric field in
// this catalog is an int64; converting via float64 would round large revisions.
func mcpResultIntegers(value any) any {
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			value[key] = mcpResultIntegers(child)
		}
	case []any:
		for i, child := range value {
			value[i] = mcpResultIntegers(child)
		}
	case json.Number:
		if integer, err := value.Int64(); err == nil {
			return integer
		}
	}
	return value
}

func mcpBackendError(err error) *noxmcp.Error {
	switch {
	case errors.Is(err, storage.ErrBadRequest):
		return &noxmcp.Error{Code: "BAD_REQUEST", Message: err.Error()}
	case errors.Is(err, storage.ErrNotFound):
		return &noxmcp.Error{Code: "NOT_FOUND", Message: "Requested resource was not found."}
	case errors.Is(err, storage.ErrFileChanged):
		return &noxmcp.Error{Code: "FILE_CHANGED", Message: "File changed since selection. Refresh the file list and select again."}
	default:
		return &noxmcp.Error{Code: "SERVER_ERROR", Message: "Backend operation failed."}
	}
}

func (s *Server) mcpTools() ([]noxmcp.Tool, error) {
	var tools []noxmcp.Tool
	var buildErr error
	add := func(tool noxmcp.Tool, err error) {
		if err != nil {
			buildErr = errors.Join(buildErr, fmt.Errorf("tool %s: %w", tool.Name, err))
		}
		tools = append(tools, tool)
	}
	add(backendTool(noxmcp.Tool{Name: "backend_health", Description: "Read backend readiness and its runtime version.", ReadOnly: true, Idempotent: true}, false,
		func(_ noxmcp.ExecutionContext, _ storage.User, _ emptyInput) (healthResponse, error) {
			return s.health(), nil
		}))
	add(backendTool(noxmcp.Tool{Name: "backend_info", Description: "Read service identity and Stable Client API capabilities.", ReadOnly: true, Idempotent: true}, false,
		func(_ noxmcp.ExecutionContext, _ storage.User, _ emptyInput) (ServiceInfo, error) {
			return s.serviceInfo(), nil
		}))
	add(backendTool(noxmcp.Tool{Name: "backend_whoami", Description: "Read the API-key user's email and role.", ReadOnly: true, Idempotent: true}, true,
		func(_ noxmcp.ExecutionContext, user storage.User, _ emptyInput) (identityResponse, error) {
			return identityResponse{user.Email, user.Role}, nil
		}))
	add(backendTool(noxmcp.Tool{Name: "backend_vault_list", Description: "List the user's active and soft-deleted vaults.", ReadOnly: true, Idempotent: true}, true,
		func(ctx noxmcp.ExecutionContext, user storage.User, _ emptyInput) (storage.VaultListResponse, error) {
			return s.listVaults(ctx, user.ID)
		}))
	add(backendTool(noxmcp.Tool{Name: "backend_vault_create", Description: "Create a vault owned by the API-key user."}, true,
		func(ctx noxmcp.ExecutionContext, user storage.User, input storage.CreateVaultRequest) (storage.Vault, error) {
			return s.store.CreateVault(ctx, user.ID, input.Name)
		}))
	add(backendTool(noxmcp.Tool{Name: "backend_vault_delete", Description: "Soft-delete an active owned vault, retaining its data for restoration.", Destructive: true}, true,
		func(ctx noxmcp.ExecutionContext, user storage.User, input vaultInput) (okResponse, error) {
			return okResponse{true}, s.store.SoftDeleteVault(ctx, user.ID, input.VaultID)
		}))
	add(backendTool(noxmcp.Tool{Name: "backend_vault_restore", Description: "Restore an owned soft-deleted vault."}, true,
		func(ctx noxmcp.ExecutionContext, user storage.User, input vaultInput) (okResponse, error) {
			return okResponse{true}, s.store.RestoreVault(ctx, user.ID, input.VaultID)
		}))
	add(backendTool(noxmcp.Tool{Name: "backend_vault_purge", Description: "Irreversibly purge an owned soft-deleted vault and its unreferenced blobs.", Destructive: true}, true,
		func(ctx noxmcp.ExecutionContext, user storage.User, input vaultInput) (okResponse, error) {
			return okResponse{true}, s.store.PurgeDeletedVault(ctx, user.ID, input.VaultID)
		}))
	add(backendTool(noxmcp.Tool{Name: "backend_vault_status", Description: "Read an active owned vault's committed revision and sync status, refreshing stale locks.", ReadOnly: true, Idempotent: true}, true,
		func(ctx noxmcp.ExecutionContext, user storage.User, input vaultInput) (vaultStatusResponse, error) {
			return s.refreshedVaultStatus(ctx, user.ID, input.VaultID)
		}))
	add(backendTool(noxmcp.Tool{Name: "backend_file_list", Description: "List an active owned vault's committed file metadata, ordered by vault-relative path.", ReadOnly: true, Idempotent: true}, true,
		func(ctx noxmcp.ExecutionContext, user storage.User, input vaultInput) (storage.FileList, error) {
			return s.store.ListFiles(ctx, user.ID, input.VaultID)
		}))
	return tools, buildErr
}
