package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/mapherez/nox-backend/backend/internal/storage"
)

// Administration has its own JSON adapters. Legacy browser/client routes retain
// their authentication and redirect contracts; credentials never cross between them.
func (s *Server) registerAdministration(mux *http.ServeMux) {
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeJSONError(w, 404, "NOT_FOUND", "API route not found.")
	})
	routes := []struct {
		path    string
		admin   bool
		handler func(http.ResponseWriter, *http.Request, storage.User)
	}{
		{"/api/account", false, s.handleAccount},
		{"/api/account/api-key", false, s.handleAccountKey},
		{"/api/account/api-key/rotate", false, s.handleAccountKeyRotation},
		{"/api/session/logout", false, s.handleSessionLogout},
		{"/api/admin/users", true, s.handleAdminUsers},
		{"/api/admin/users/{userId}", true, s.handleAdminUser},
		{"/api/admin/users/{userId}/status", true, s.handleAdminUserStatus},
		{"/api/admin/users/{userId}/role", true, s.handleAdminUserRole},
		{"/api/vaults", false, s.handleManagementVaults},
		{"/api/vaults/{vaultId}", false, s.handleManagementVaultDelete},
		{"/api/vaults/{vaultId}/restore", false, s.handleManagementVaultRestore},
		{"/api/vaults/{vaultId}/purge", false, s.handleManagementVaultPurge},
		{"/api/vaults/{vaultId}/download", false, s.handleManagementVaultDownload},
		{"/api/admin/connections", true, s.handleConnections},
		{"/api/admin/connections/{service}", true, s.handleConnection},
	}
	for _, route := range routes {
		mux.HandleFunc(route.path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			actor, ok := s.managementActor(w, r)
			if !ok {
				return
			}
			if route.admin && actor.Role != storage.UserRoleAdmin {
				writeJSONError(w, 403, "FORBIDDEN", "Admin access is required.")
				return
			}
			route.handler(w, r, actor)
		})
	}
}

func (s *Server) managementActor(w http.ResponseWriter, r *http.Request) (storage.User, bool) {
	// An explicit Authorization header always wins, including an invalid one.
	if values, present := r.Header["Authorization"]; present {
		if len(values) != 1 {
			writeJSONError(w, 401, "AUTH_FAILED", "Invalid authorization header.")
			return storage.User{}, false
		}
		parts := strings.Fields(values[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeJSONError(w, 401, "AUTH_FAILED", "A Bearer API key is required.")
			return storage.User{}, false
		}
		actor, err := s.apiUser(r.Context(), parts[1])
		if err != nil {
			writeJSONError(w, err.status, err.code, err.message)
			return storage.User{}, false
		}
		return actor, true
	}
	actor, ok, err := s.webUser(r)
	if err != nil {
		writeJSONError(w, 500, "SERVER_ERROR", "Failed to load session.")
		return storage.User{}, false
	}
	if !ok {
		writeJSONError(w, 401, "AUTH_REQUIRED", "An active session or Bearer API key is required.")
		return storage.User{}, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		// This custom header cannot be sent by cross-origin forms. No CORS is enabled.
		// Also reject cross-origin browser requests even if a proxy enabled CORS.
		if r.Header.Get("X-Nox-Request") != "1" || !s.sameManagementOrigin(r) {
			writeJSONError(w, 403, "FORBIDDEN", "A same-origin administration request is required.")
			return storage.User{}, false
		}
	}
	return actor, true
}

func (s *Server) sameManagementOrigin(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	supplied, err := url.Parse(origin)
	if err != nil || supplied.User != nil || supplied.RawQuery != "" || supplied.Fragment != "" || supplied.Path != "" {
		return false
	}
	expected, err := url.Parse(s.publicURL(r))
	return err == nil && (supplied.Scheme == "https" || supplied.Scheme == "http") && strings.EqualFold(supplied.Scheme, expected.Scheme) && strings.EqualFold(originHost(supplied), originHost(expected))
}

func managementMethod(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeJSONError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed.")
	return false
}

func decodeManagementJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeJSONError(w, 415, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json.")
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	defer r.Body.Close()
	if err != nil {
		writeJSONError(w, 413, "BODY_TOO_LARGE", "Request body exceeds the administration limit.")
		return false
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		writeJSONError(w, 400, "BAD_REQUEST", "Request body must be a JSON object.")
		return false
	}
	decoder := jsonDecoder(bytes.NewReader(body))
	if decoder.Decode(dst) != nil {
		writeJSONError(w, 400, "BAD_REQUEST", "Invalid administration fields.")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeJSONError(w, 400, "BAD_REQUEST", "Request body must contain one JSON object.")
		return false
	}
	return true
}

func writeManagementError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrBadRequest):
		writeJSONError(w, 400, "BAD_REQUEST", err.Error())
	case errors.Is(err, storage.ErrForbidden):
		writeJSONError(w, 403, "FORBIDDEN", "Access denied.")
	case errors.Is(err, storage.ErrNotFound):
		writeJSONError(w, 404, "NOT_FOUND", "Resource not found.")
	case errors.Is(err, storage.ErrPreconditionFailed):
		writeJSONError(w, 412, "PRECONDITION_FAILED", "Configuration changed. Reload before saving.")
	default:
		writeJSONError(w, 500, "SERVER_ERROR", "Administration operation failed.")
	}
}
func managementOK(w http.ResponseWriter, err error) {
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "GET") {
		return
	}
	writeJSON(w, 200, s.presentDashboardUser(actor, actor))
}
func (s *Server) handleAccountKey(w http.ResponseWriter, r *http.Request, user storage.User) {
	if !managementMethod(w, r, "GET") {
		return
	}
	key, err := s.store.CurrentAPIKey(r.Context(), user.ID)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"apiKey": key})
}
func (s *Server) handleAccountKeyRotation(w http.ResponseWriter, r *http.Request, user storage.User) {
	if !managementMethod(w, r, "POST") {
		return
	}
	key, err := s.store.RotateAPIKey(r.Context(), user.ID)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"apiKey": key})
}
func (s *Server) handleSessionLogout(w http.ResponseWriter, r *http.Request, _ storage.User) {
	if !managementMethod(w, r, "POST") {
		return
	}
	if _, present := r.Header["Authorization"]; present {
		writeJSONError(w, 400, "BAD_REQUEST", "Use the browser session to sign out.")
		return
	}
	// This endpoint ends a browser session, never revokes an unrelated API key.
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		err = s.store.DeleteWebSession(r.Context(), cookie.Value)
	}
	if err != nil && err != http.ErrNoCookie {
		writeManagementError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(s.publicURL(r), "https://")})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "GET", "POST") {
		return
	}
	if r.Method == "GET" {
		users, err := s.store.ListUsers(r.Context())
		if err != nil {
			writeManagementError(w, err)
			return
		}
		result := make([]dashboardUser, 0, len(users))
		for _, user := range users {
			result = append(result, s.presentDashboardUser(actor, user))
		}
		writeJSON(w, 200, result)
		return
	}
	var input struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decodeManagementJSON(w, r, &input) {
		return
	}
	user, err := s.addManagedUser(r.Context(), actor, input.Email, input.Role)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, 200, s.presentDashboardUser(actor, user))
}
func (s *Server) handleAdminUser(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "DELETE") {
		return
	}
	managementOK(w, s.deleteManagedUser(r.Context(), actor, r.PathValue("userId")))
}
func (s *Server) handleAdminUserStatus(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "PUT") {
		return
	}
	var input struct {
		Status string `json:"status"`
	}
	if !decodeManagementJSON(w, r, &input) {
		return
	}
	managementOK(w, s.setManagedUserStatus(r.Context(), actor, r.PathValue("userId"), input.Status))
}
func (s *Server) handleAdminUserRole(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "PUT") {
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if !decodeManagementJSON(w, r, &input) {
		return
	}
	managementOK(w, s.setManagedUserRole(r.Context(), actor, r.PathValue("userId"), input.Role))
}
func (s *Server) handleManagementVaults(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "GET") {
		return
	}
	data, err := s.listVaults(r.Context(), actor.ID)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"vaults": presentDashboardVaults(data.Vaults), "deletedVaults": presentDashboardVaults(data.DeletedVaults)})
}
func (s *Server) handleManagementVaultDelete(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "DELETE") {
		return
	}
	managementOK(w, s.store.SoftDeleteVault(r.Context(), actor.ID, r.PathValue("vaultId")))
}
func (s *Server) handleManagementVaultRestore(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "POST") {
		return
	}
	managementOK(w, s.store.RestoreVault(r.Context(), actor.ID, r.PathValue("vaultId")))
}
func (s *Server) handleManagementVaultPurge(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "DELETE") {
		return
	}
	managementOK(w, s.store.PurgeDeletedVault(r.Context(), actor.ID, r.PathValue("vaultId")))
}
func (s *Server) handleManagementVaultDownload(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "GET") {
		return
	}
	s.downloadVaultZip(w, r, actor, r.PathValue("vaultId"), writeManagementError)
}

type connectionView struct {
	storage.FoundationConnection
	Status string `json:"status"`
}

func (s *Server) handleConnections(w http.ResponseWriter, r *http.Request, _ storage.User) {
	if !managementMethod(w, r, "GET") {
		return
	}
	result, err := s.foundationConnectionViews(r.Context())
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) foundationConnectionViews(ctx context.Context) ([]connectionView, error) {
	saved, err := s.store.FoundationConnections(ctx)
	if err != nil {
		return nil, err
	}
	// Foundation roles are configuration targets, not a catalog of Suite products.
	result := []connectionView{{FoundationConnection: storage.FoundationConnection{Service: "yard"}, Status: "not_configured"}, {FoundationConnection: storage.FoundationConnection{Service: "gateway"}, Status: "not_configured"}}
	for i := range result {
		for _, item := range saved {
			if result[i].Service == item.Service {
				purpose := "gateway"
				if item.Service == "yard" {
					purpose = "installer"
				}
				state, err := s.store.SuiteFoundationStatus(ctx, purpose, item.Origin)
				if err != nil {
					return nil, err
				}
				result[i] = connectionView{item, state}
			}
		}
	}
	return result, nil
}
func (s *Server) handleConnection(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "PUT", "DELETE") {
		return
	}
	service := r.PathValue("service")
	if service != "yard" && service != "gateway" {
		writeJSONError(w, 404, "NOT_FOUND", "Unknown foundation-service role.")
		return
	}
	revision := r.Header.Get("If-Match")
	none := r.Header.Get("If-None-Match")
	if len(r.Header.Values("If-Match")) > 1 || len(r.Header.Values("If-None-Match")) > 1 || (none != "" && none != "*") {
		writeJSONError(w, 428, "PRECONDITION_REQUIRED", "Use a single configuration precondition.")
		return
	}
	create := none == "*"
	if create && r.Method == "PUT" && revision == "" {
		// Empty storage revision means create only.
	} else if len(revision) > 2 && strings.HasPrefix(revision, `"`) && strings.HasSuffix(revision, `"`) && !strings.ContainsAny(revision[1:len(revision)-1], "\" ,\t\r\n") && !create {
		revision = revision[1 : len(revision)-1]
	} else {
		writeJSONError(w, 428, "PRECONDITION_REQUIRED", "Use If-None-Match: * to create or If-Match with the quoted configuration revision to change/remove.")
		return
	}
	if r.Method == "DELETE" {
		managementOK(w, s.store.DeleteFoundationConnection(r.Context(), service, revision))
		return
	}
	var input struct {
		Origin string `json:"origin"`
	}
	if !decodeManagementJSON(w, r, &input) {
		return
	}
	item, err := s.saveFoundationConnection(r.Context(), actor, service, input.Origin, revision)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	w.Header().Set("ETag", `"`+item.Revision+`"`)
	writeJSON(w, 200, connectionView{item, "not_paired"})
}

func originHost(origin *url.URL) string {
	host := strings.ToLower(origin.Hostname())
	port := origin.Port()
	if port == "" || (origin.Scheme == "https" && port == "443") || (origin.Scheme == "http" && port == "80") {
		return host
	}
	return host + ":" + port
}
