package app

import "net/http"

const (
	serviceName           = "nox-backend"
	apiVersion            = "v1"
	capabilityAuth        = "auth"
	capabilityVaults      = "vaults"
	capabilityFiles       = "files"
	capabilityVaultStatus = "vault-status"
)

// ServiceInfo describes the stable HTTP API available to external clients.
type ServiceInfo struct {
	Service      string   `json:"service"`
	Version      string   `json:"version"`
	APIVersion   string   `json:"apiVersion"`
	Capabilities []string `json:"capabilities"`
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	if !s.requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, ServiceInfo{
		Service:    serviceName,
		Version:    s.cfg.Version,
		APIVersion: apiVersion,
		Capabilities: []string{
			capabilityAuth, capabilityVaults, capabilityFiles, capabilityVaultStatus,
		},
	})
}
