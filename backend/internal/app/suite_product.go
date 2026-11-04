package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
	noxmcp "github.com/mapherez/nox-mcp/go"
)

const backendSuiteMCPPath = "/mcp/suite"

// Explicit classification: new tools are absent from Suite until classified.
// Health includes database-path diagnostics, so it is sensitive in Suite.
var backendSuitePolicies = map[string]noxmcp.SuitePolicy{
	"backend_health":        {Version: "1.0", RequiredScopes: []string{"nox:inspect", "nox:sensitive-read"}},
	"backend_info":          {Version: "1.0", RequiredScopes: []string{"nox:inspect"}},
	"backend_whoami":        {Version: "1.0", RequiredScopes: []string{"nox:inspect"}},
	"backend_vault_list":    {Version: "1.0", RequiredScopes: []string{"nox:inspect"}},
	"backend_vault_status":  {Version: "1.0", RequiredScopes: []string{"nox:inspect"}},
	"backend_file_list":     {Version: "1.0", RequiredScopes: []string{"nox:inspect"}},
	"backend_vault_create":  {Version: "1.0", RequiredScopes: []string{"nox:inspect", "nox:write"}},
	"backend_vault_delete":  {Version: "1.0", RequiredScopes: []string{"nox:inspect", "nox:write"}},
	"backend_vault_restore": {Version: "1.0", RequiredScopes: []string{"nox:inspect", "nox:write"}},
	"backend_vault_purge":   {Version: "1.0", RequiredScopes: []string{"nox:inspect", "nox:write"}},
}

func (s *Server) handleIntegrationDeclaration(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !s.requireMethod(w, r, http.MethodGet) {
		return
	}
	version := s.cfg.Version
	if version == "" {
		version = "dev"
	}
	doc := noxmcp.SuiteProductDeclaration{ContractVersion: "1.0", ProductID: serviceName, ProductVersion: version, DisplayName: "NoX Backend"}
	if s.cfg.SuiteMCPEnabled {
		doc.Capabilities.SuiteMcp = &noxmcp.SuiteProductDeclarationCapabilitiesSuiteMcp{Path: backendSuiteMCPPath}
	}
	raw, err := json.Marshal(doc)
	if err == nil {
		_, err = noxmcp.LoadSuiteProductDeclaration(raw)
	}
	if err != nil {
		writeJSONError(w, 503, "INTEGRATION_UNAVAILABLE", "The product declaration is unavailable.")
		return
	}
	writeJSON(w, 200, doc)
}
func (s *Server) suiteMCPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !s.suiteSecureTransport(r) {
			writeJSONError(w, 403, "FORBIDDEN", "Suite MCP requires the same-host local channel.")
			return
		}
		if !s.requireMethod(w, r, http.MethodPost) {
			return
		}
		if r.URL.RawQuery != "" {
			writeJSONError(w, 400, "BAD_REQUEST", "Suite MCP does not accept query parameters.")
			return
		}
		if len(r.Header.Values("Authorization")) != 1 {
			suiteMCPUnauthorized(w)
			return
		}
		fields := strings.Fields(r.Header.Get("Authorization"))
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			suiteMCPUnauthorized(w)
			return
		}
		select {
		case s.suiteMCPRequests <- struct{}{}:
			defer func() { <-s.suiteMCPRequests }()
		default:
			writeJSONError(w, 503, "OVERLOADED", "Suite MCP request capacity is occupied.")
			return
		}
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(20 * time.Second))
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		origin, err := s.suiteOrigin()
		if err != nil {
			writeJSONError(w, 503, "INTEGRATION_UNAVAILABLE", "The local Suite channel is not configured.")
			return
		}
		admission, err := s.store.SuiteAcceptBackendToken(ctx, fields[1], origin, backendSuiteMCPPath)
		if err != nil {
			var rejected *storage.SuiteError
			if errors.As(err, &rejected) && rejected.Status == 401 {
				suiteMCPUnauthorized(w)
			} else {
				writeJSONError(w, 503, "INTEGRATION_UNAVAILABLE", "Local Suite authority is unavailable.")
			}
			return
		}
		runtime, err := s.newSuiteMCP(ctx, admission, origin)
		if err != nil {
			writeJSONError(w, 503, "INTEGRATION_UNAVAILABLE", "Suite MCP is unavailable.")
			return
		}
		runtime.HTTPHandler().ServeHTTP(w, r.WithContext(ctx))
	})
}
func suiteMCPUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	writeJSONError(w, 401, "UNAUTHENTICATED", "A current Suite delegation or probe is required.")
}
func (s *Server) newSuiteMCP(ctx context.Context, admission storage.SuiteBackendAdmission, origin string) (*noxmcp.Runtime, error) {
	tools, err := s.mcpTools()
	if err != nil {
		return nil, err
	}
	if err = setBackendCLIMetadata(tools); err != nil {
		return nil, err
	}
	for i := range tools {
		tools[i] = s.bindSuiteMCPTool(tools[i], admission, origin)
	}
	return noxmcp.NewSuite(noxmcp.Options{AppID: serviceName, Name: serviceName, Version: s.cfg.Version, Tools: tools}, noxmcp.SuiteRuntimeOptions{
		Verification: func() noxmcp.SuiteVerificationOptions { return admission.Verification },
		Policies:     backendSuitePolicies,
		IsActive: func(identity *noxmcp.VerifiedSuiteIdentity) bool {
			_, err := s.store.SuiteAcceptBackendToken(ctx, identity.AccessToken(), origin, backendSuiteMCPPath)
			return err == nil
		},
	}, backendCLIDiscovery())
}

func (s *Server) bindSuiteMCPTool(tool noxmcp.Tool, admission storage.SuiteBackendAdmission, origin string) noxmcp.Tool {

	execute := tool.Execute
	tool.Execute = func(exec noxmcp.ExecutionContext, args map[string]any) (map[string]any, error) {
		if err := exec.CheckActive(); err != nil {
			return nil, err
		}
		// Shared across request-local runtimes and batches. Keep the slot until
		// execution actually exits, including after a transport/runtime timeout.
		select {
		case s.suiteMCPExecutions <- struct{}{}:
			defer func() { <-s.suiteMCPExecutions }()
		default:
			return nil, &noxmcp.Error{Code: "OVERLOADED", Message: "Suite MCP execution capacity is occupied.", Retryable: true}
		}
		if admission.Identity.Kind() != "user" || exec.UserID != admission.User.ID {
			return nil, &noxmcp.Error{Code: "FORBIDDEN", Message: "A delegated user is required."}
		}
		exec.Context = storage.WithSuiteBackendIdentity(exec.Context, admission, origin, backendSuiteMCPPath)
		exec.Context = context.WithValue(exec.Context, mcpIdentityKey{}, mcpIdentity{user: admission.User})
		return execute(exec, args)
	}
	return tool
}
