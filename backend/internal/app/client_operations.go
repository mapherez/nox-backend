package app

import (
	"context"
	"net/http"

	"github.com/mapherez/nox-backend/backend/internal/storage"
)

type healthResponse struct {
	Status             string `json:"status"`
	Version            string `json:"version"`
	DataDirInitialized bool   `json:"dataDirInitialized"`
	DatabasePath       string `json:"databasePath"`
}

func (s *Server) health() healthResponse {
	return healthResponse{"ready", s.cfg.Version, true, s.store.DBPath()}
}

func (s *Server) serviceInfo() ServiceInfo {
	return ServiceInfo{
		Service: serviceName, Version: s.cfg.Version, APIVersion: apiVersion,
		Capabilities: []string{capabilityAuth, capabilityVaults, capabilityFiles, capabilityVaultStatus},
	}
}

type apiAuthError struct {
	status        int
	code, message string
}

func (e *apiAuthError) Error() string { return e.message }

func (s *Server) apiUser(ctx context.Context, token string) (storage.User, *apiAuthError) {
	if token == "" {
		return storage.User{}, &apiAuthError{http.StatusUnauthorized, "AUTH_REQUIRED", "NoX Sync API key is required."}
	}
	user, valid, err := s.store.AuthenticateAPIKey(ctx, token)
	if err != nil {
		return storage.User{}, &apiAuthError{http.StatusInternalServerError, "SERVER_ERROR", "Failed to validate API key."}
	}
	if !valid {
		return storage.User{}, &apiAuthError{http.StatusUnauthorized, "AUTH_FAILED", "Invalid NoX Sync API key."}
	}
	return user, nil
}

func (s *Server) listVaults(ctx context.Context, userID string) (storage.VaultListResponse, error) {
	vaults, err := s.store.ListVaults(ctx, userID)
	if err != nil {
		return storage.VaultListResponse{}, err
	}
	deleted, err := s.store.ListDeletedVaults(ctx, userID)
	return storage.VaultListResponse{Vaults: vaults, DeletedVaults: deleted}, err
}

type vaultStatusResponse struct {
	VaultID        string             `json:"vaultId"`
	ServerRevision int64              `json:"serverRevision"`
	Sync           syncStatusResponse `json:"sync"`
}

type syncStatusResponse struct {
	State      string `json:"state"`
	SessionID  string `json:"sessionId"`
	ClientID   string `json:"clientId"`
	ClientName string `json:"clientName"`
	StartedAt  string `json:"startedAt"`
}

// refreshedVaultStatus preserves the status endpoint's stale-lock event behavior.
func (s *Server) refreshedVaultStatus(ctx context.Context, userID, vaultID string) (vaultStatusResponse, error) {
	payload, reaped, err := s.statusPayloadWithRefresh(ctx, userID, vaultID)
	if err == nil && reaped {
		s.events.broadcast(vaultID, payload)
	}
	return payload, err
}
