package app

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/mapherez/nox-backend/backend/internal/storage"
)

func (s *Server) handleDashboardData(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/dashboard" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	user, authenticated, err := s.webUser(r)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "SERVER_ERROR", "Failed to load session.")
		return
	}
	data := dashboardPageData{
		Authenticated:   authenticated,
		OAuthConfigured: s.googleOAuthConfigured(),
		LoginURL:        "/auth/google/start?redirect=/vault-dashboard",
		ServerURL:       s.publicURL(r),
	}
	if authenticated {
		apiKey, err := s.store.CurrentAPIKey(r.Context(), user.ID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "SERVER_ERROR", "Failed to load API key.")
			return
		}
		vaults, err := s.store.ListVaults(r.Context(), user.ID)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		deletedVaults, err := s.store.ListDeletedVaults(r.Context(), user.ID)
		if err != nil {
			writeStorageError(w, err)
			return
		}
		data.User = presentDashboardUser(user)
		data.APIKey = apiKey
		data.Vaults = presentDashboardVaults(vaults)
		data.DeletedVaults = presentDashboardVaults(deletedVaults)
		data.IsAdmin = user.Role == storage.UserRoleAdmin
		if data.IsAdmin {
			users, err := s.store.ListUsers(r.Context())
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "SERVER_ERROR", "Failed to load users.")
				return
			}
			data.Connections, err = s.foundationConnectionViews(r.Context())
			if err != nil {
				writeManagementError(w, err)
				return
			}
			for _, item := range users {
				data.Users = append(data.Users, presentDashboardUser(item))
			}
		}
	}

	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleRotateAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}
	user, ok := s.requireWebUser(w, r)
	if !ok {
		http.Redirect(w, r, "/vault-dashboard", http.StatusSeeOther)
		return
	}
	if _, err := s.store.RotateAPIKey(r.Context(), user.ID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "SERVER_ERROR", "Failed to generate a new API key.")
		return
	}

	http.Redirect(w, r, "/vault-dashboard?message=api-key-rotated", http.StatusSeeOther)
}

func (s *Server) handleAddUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}
	admin, ok := s.requireAdminWebUser(w, r)
	if !ok {
		return
	}
	if _, err := s.addManagedUser(r.Context(), admin, r.FormValue("email"), r.FormValue("role")); err != nil {
		writeStorageError(w, err)
		return
	}
	http.Redirect(w, r, "/vault-dashboard?message=user-saved", http.StatusSeeOther)
}

func (s *Server) handleSetUserStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}
	admin, ok := s.requireAdminWebUser(w, r)
	if !ok {
		return
	}
	userID := strings.TrimSpace(r.FormValue("userId"))
	status := strings.TrimSpace(r.FormValue("status"))
	if err := s.setManagedUserStatus(r.Context(), admin, userID, status); err != nil {
		writeStorageError(w, err)
		return
	}

	http.Redirect(w, r, "/vault-dashboard?message=user-saved", http.StatusSeeOther)
}

func (s *Server) handleSetUserRole(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}
	admin, ok := s.requireAdminWebUser(w, r)
	if !ok {
		return
	}
	userID := strings.TrimSpace(r.FormValue("userId"))
	role := strings.ToUpper(strings.TrimSpace(r.FormValue("role")))
	if err := s.setManagedUserRole(r.Context(), admin, userID, role); err != nil {
		writeStorageError(w, err)
		return
	}

	http.Redirect(w, r, "/vault-dashboard?message=user-saved", http.StatusSeeOther)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}
	admin, ok := s.requireAdminWebUser(w, r)
	if !ok {
		return
	}
	userID := strings.TrimSpace(r.FormValue("userId"))
	if err := s.deleteManagedUser(r.Context(), admin, userID); err != nil {
		writeStorageError(w, err)
		return
	}

	http.Redirect(w, r, "/vault-dashboard?message=user-deleted", http.StatusSeeOther)
}

func (s *Server) handleDeleteVault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}
	user, ok := s.requireWebUser(w, r)
	if !ok {
		http.Redirect(w, r, "/vault-dashboard", http.StatusSeeOther)
		return
	}
	if err := s.store.SoftDeleteVault(r.Context(), user.ID, r.FormValue("vaultId")); err != nil {
		writeStorageError(w, err)
		return
	}
	http.Redirect(w, r, "/vault-dashboard?message=vault-deleted", http.StatusSeeOther)
}

func (s *Server) handleRestoreVault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}
	user, ok := s.requireWebUser(w, r)
	if !ok {
		http.Redirect(w, r, "/vault-dashboard", http.StatusSeeOther)
		return
	}
	if err := s.store.RestoreVault(r.Context(), user.ID, r.FormValue("vaultId")); err != nil {
		writeStorageError(w, err)
		return
	}
	http.Redirect(w, r, "/vault-dashboard?message=vault-restored", http.StatusSeeOther)
}

func (s *Server) handlePurgeVault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}
	user, ok := s.requireWebUser(w, r)
	if !ok {
		http.Redirect(w, r, "/vault-dashboard", http.StatusSeeOther)
		return
	}
	if err := s.store.PurgeDeletedVault(r.Context(), user.ID, r.FormValue("vaultId")); err != nil {
		writeStorageError(w, err)
		return
	}
	http.Redirect(w, r, "/vault-dashboard?message=vault-purged", http.StatusSeeOther)
}

func (s *Server) handleDownloadVaultZip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}
	user, ok := s.requireWebUser(w, r)
	if !ok {
		http.Redirect(w, r, "/vault-dashboard", http.StatusSeeOther)
		return
	}
	s.downloadVaultZip(w, r, user, strings.TrimSpace(r.URL.Query().Get("vaultId")), writeStorageError)
}

func (s *Server) downloadVaultZip(w http.ResponseWriter, r *http.Request, user storage.User, vaultID string, writeError func(http.ResponseWriter, error)) {
	vault, err := s.store.VaultByID(r.Context(), user.ID, vaultID)
	if err != nil {
		writeError(w, err)
		return
	}
	files, err := s.store.CurrentVaultFiles(r.Context(), user.ID, vaultID)
	if err != nil {
		writeError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, sanitizeFilename(vault.Name)))
	writer := zip.NewWriter(w)
	defer writer.Close()

	for _, file := range files {
		if err := writeZipFile(writer, s.store.BlobPath(file.Hash), file.Path); err != nil {
			return
		}
	}
}

func writeZipFile(writer *zip.Writer, blobPath string, vaultPath string) error {
	source, err := os.Open(blobPath)
	if err != nil {
		return err
	}
	defer source.Close()

	entry, err := writer.CreateHeader(&zip.FileHeader{
		Name:   vaultPath,
		Method: zip.Deflate,
	})
	if err != nil {
		return err
	}
	_, err = io.Copy(entry, source)
	return err
}

func sanitizeFilename(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "nox-sync-vault"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		case r == ' ':
			builder.WriteRune('-')
		}
	}
	if builder.Len() == 0 {
		return "nox-sync-vault"
	}
	return builder.String()
}
