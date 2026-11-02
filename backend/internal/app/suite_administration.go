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
	ready := originErr == nil && validateSuiteSocketDirectory(s.cfg.SuiteSocketDir) == nil
	reason := ""
	if !ready {
		reason = "The local Suite channel is unavailable. Use the deployment with its shared local volume."
	}
	writeJSON(w, 200, map[string]any{"state": state, "configuration": map[string]any{"backendOrigin": origin, "canManage": ready, "canEnroll": ready, "enrollmentReason": reason, "reason": reason, "transport": "unix", "directMcpEnabled": !s.cfg.DirectMCPDisabled, "suiteMcpEnabled": s.cfg.SuiteMCPEnabled}})
}
