package app

import "github.com/mapherez/nox-backend/backend/internal/storage"

type dashboardUser struct {
	ID             string                    `json:"id"`
	Email          string                    `json:"email"`
	FirstName      string                    `json:"firstName"`
	DisplayName    string                    `json:"displayName"`
	Role           string                    `json:"role"`
	Status         string                    `json:"status"`
	LastLoginAt    string                    `json:"lastLoginAt"`
	IsAdminManager bool                      `json:"isAdminManager"`
	Permissions    userManagementPermissions `json:"permissions"`
}
type userManagementPermissions struct {
	CanDelete       bool `json:"canDelete"`
	CanChangeRole   bool `json:"canChangeRole"`
	CanChangeStatus bool `json:"canChangeStatus"`
}

func (s *Server) presentDashboardUser(actor, user storage.User) dashboardUser {
	canManage := s.canManageUser(actor, user)
	return dashboardUser{
		ID: user.ID, Email: user.Email, FirstName: user.FirstName, DisplayName: user.DisplayName,
		Role: user.Role, Status: user.Status, LastLoginAt: user.LastLoginAt,
		IsAdminManager: s.configuredAdmin(user.Email),
		Permissions:    userManagementPermissions{canManage, canManage, canManage},
	}
}

type dashboardVault struct {
	ID        string `json:"vaultId"`
	Name      string `json:"name"`
	Revision  int64  `json:"revision,string"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updatedAt"`
	DeletedAt string `json:"deletedAt,omitempty"`
	SizeBytes int64  `json:"sizeBytes,string"`
}

func presentDashboardVaults(vaults []storage.Vault) []dashboardVault {
	result := make([]dashboardVault, 0, len(vaults))
	for _, v := range vaults {
		result = append(result, dashboardVault{v.ID, v.Name, v.Revision, v.Status, v.UpdatedAt, v.DeletedAt, v.SizeBytes})
	}
	return result
}

type dashboardPageData struct {
	Authenticated    bool             `json:"authenticated"`
	OAuthConfigured  bool             `json:"oauthConfigured"`
	LoginURL         string           `json:"loginUrl"`
	ServerURL        string           `json:"serverUrl"`
	User             dashboardUser    `json:"user"`
	APIKey           string           `json:"apiKey"`
	Vaults           []dashboardVault `json:"vaults"`
	DeletedVaults    []dashboardVault `json:"deletedVaults"`
	IsAdmin          bool             `json:"isAdmin"`
	Users            []dashboardUser  `json:"users"`
	IntegrationError string           `json:"integrationError,omitempty"`
	Connections      []connectionView `json:"connections,omitempty"`
}
