package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mapherez/nox-backend/backend/internal/storage"
)

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.requireMethod(w, r, http.MethodGet) {
		return
	}
	user, ok := s.requireAPIUser(w, r)
	if !ok {
		return
	}
	vaultID, ok := vaultIDFromQuery(w, r)
	if !ok {
		return
	}
	result, err := s.store.ListFiles(r.Context(), user.ID, vaultID)
	if err != nil {
		writeStorageError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func downloadConditions(r *http.Request) (storage.DownloadConditions, error) {
	var conditions storage.DownloadConditions
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return conditions, fmt.Errorf("%w: invalid query parameters", storage.ErrBadRequest)
	}
	if values, supplied := query["expectedHash"]; supplied {
		if len(values) != 1 {
			return conditions, fmt.Errorf("%w: expectedHash must be supplied once", storage.ErrBadRequest)
		}
		conditions.ExpectedHash = &values[0]
	}
	if values, supplied := query["expectedRevision"]; supplied {
		if len(values) != 1 || values[0] == "" || strings.ContainsAny(values[0], "+- \t\r\n") {
			return conditions, fmt.Errorf("%w: expectedRevision must be a non-negative integer", storage.ErrBadRequest)
		}
		revision, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			return conditions, fmt.Errorf("%w: expectedRevision must be a non-negative integer", storage.ErrBadRequest)
		}
		conditions.ExpectedRevision = &revision
	}
	return conditions, nil
}
