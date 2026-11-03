package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
)

func noXHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// Preserve the established browser entry point used by existing integrations.
// It now starts NoX Login; Google credentials and callbacks belong only to Auth.
func (s *Server) handleGoogleStart(w http.ResponseWriter, r *http.Request) { s.noXStart(w, r) }
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !managementMethod(w, r, "POST") {
		return
	}
	if s.noXLogout(w, r, false) {
		http.Redirect(w, r, "/vault-dashboard", 303)
	}
}
func (s *Server) noxLoginConfigured() bool {
	_, _, err := s.noXClient(context.Background())
	return err == nil
}
