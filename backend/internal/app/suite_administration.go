package app

import (
	"context"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	"net/http"
	"time"
)

func (s *Server) handleSuiteAdministration(w http.ResponseWriter, r *http.Request, actor storage.User) {
	if !managementMethod(w, r, "GET") {
		return
	}
	if r.URL.RawQuery != "" {
		writeJSONError(w, 400, "BAD_REQUEST", "Query parameters are not supported.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	state, err := s.store.SuiteAdministration(ctx, storage.SuiteCaller{UserID: actor.ID})
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	origin, originErr := s.suiteOrigin()
	ready := s.suiteSecureTransport(r) && originErr == nil
	reason := ""
	if !s.suiteSecureTransport(r) {
		reason = "Open this Backend through its trusted HTTPS address to manage Suite integration."
	} else if originErr != nil {
		reason = "Configure NOX_BACKEND_PUBLIC_URL with the trusted HTTPS Backend origin."

	}
	enrollmentReason := ""
	if len(s.cfg.SuiteAllowedCIDRs) == 0 {
		enrollmentReason = "Configure NOX_BACKEND_SUITE_ALLOWED_CIDRS and the approved target ports before pairing or verifying products."
	}
	writeJSON(w, 200, map[string]any{"state": state, "configuration": map[string]any{"backendOrigin": origin, "canManage": ready, "canEnroll": ready && len(s.cfg.SuiteAllowedCIDRs) > 0, "enrollmentReason": enrollmentReason, "reason": reason, "directMcpEnabled": !s.cfg.DirectMCPDisabled, "suiteMcpEnabled": s.cfg.SuiteMCPEnabled, "allowedCidrs": s.cfg.SuiteAllowedCIDRs, "allowedPorts": s.cfg.SuiteAllowedPorts}})
}
