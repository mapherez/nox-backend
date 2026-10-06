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

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/vault-dashboard" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSONError(w, http.StatusMethodNotAllowed, "BAD_REQUEST", "Method not allowed.")
		return
	}

	user, authenticated := s.requireWebUser(w, r)
	data := dashboardPageData{
		Authenticated:   authenticated,
		OAuthConfigured: s.googleOAuthConfigured(),
		LoginURL:        "/auth/google/start?redirect=/vault-dashboard",
		ServerURL:       s.publicURL(r),
		Message:         dashboardMessage(r.URL.Query().Get("message")),
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
		data.User = user
		data.APIKey = apiKey
		data.Vaults = vaults
		data.DeletedVaults = deletedVaults
		data.IsAdmin = user.Role == storage.UserRoleAdmin
		if data.IsAdmin {
			users, err := s.store.ListUsers(r.Context())
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "SERVER_ERROR", "Failed to load users.")
				return
			}
			data.Users = users
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = dashboardTemplate.Execute(w, data)
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
	if _, ok := s.requireAdminWebUser(w, r); !ok {
		return
	}
	if _, err := s.store.UpsertAllowedUser(r.Context(), r.FormValue("email"), r.FormValue("role")); err != nil {
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
	target, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	if target.ID == admin.ID && status == storage.UserStatusDisabled {
		writeStorageError(w, fmt.Errorf("%w: admins cannot disable their own account", storage.ErrBadRequest))
		return
	}
	if target.Role == storage.UserRoleAdmin && status == storage.UserStatusDisabled {
		writeStorageError(w, fmt.Errorf("%w: admin users cannot be disabled", storage.ErrBadRequest))
		return
	}
	if err := s.store.SetUserStatus(r.Context(), userID, status); err != nil {
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
	target, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	if target.ID == admin.ID && role != storage.UserRoleAdmin {
		writeStorageError(w, fmt.Errorf("%w: admins cannot demote their own account", storage.ErrBadRequest))
		return
	}
	if target.Role == storage.UserRoleAdmin && role != storage.UserRoleAdmin {
		writeStorageError(w, fmt.Errorf("%w: admin users cannot be demoted", storage.ErrBadRequest))
		return
	}
	if err := s.store.SetUserRole(r.Context(), userID, role); err != nil {
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
	target, err := s.store.UserByID(r.Context(), userID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	if target.ID == admin.ID || target.Role == storage.UserRoleAdmin {
		writeStorageError(w, fmt.Errorf("%w: admin users cannot be deleted", storage.ErrBadRequest))
		return
	}
	if err := s.store.DeleteUser(r.Context(), userID); err != nil {
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
	vaultID := strings.TrimSpace(r.URL.Query().Get("vaultId"))
	vault, err := s.store.VaultByID(r.Context(), user.ID, vaultID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	files, err := s.store.CurrentVaultFiles(r.Context(), user.ID, vaultID)
	if err != nil {
		writeStorageError(w, err)
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

func dashboardMessage(code string) string {
	switch code {
	case "api-key-rotated":
		return "A new API key was generated. Update the key in every connected app."
	case "user-saved":
		return "User settings were saved."
	case "user-deleted":
		return "User deleted."
	case "vault-deleted":
		return "Vault deleted."
	case "vault-restored":
		return "Vault restored."
	case "vault-purged":
		return "Vault permanently deleted."
	default:
		return ""
	}
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
