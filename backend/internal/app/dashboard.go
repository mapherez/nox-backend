package app

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
)

// Keep the UI in editable files while shipping one self-contained executable.
//
//go:embed dashboard/dashboard.html dashboard/dashboard.css dashboard/dashboard.js dashboard/favicon.svg
var dashboardFiles embed.FS

func formatDashboardBytes(bytes int64) string {
	if bytes <= 0 {
		return "0 MB"
	}
	mb := float64(bytes) / (1024 * 1024)
	if mb < 0.1 {
		return "< 0.1 MB"
	}
	if mb >= 10 {
		return fmt.Sprintf("%.0f MB", mb)
	}
	return fmt.Sprintf("%.1f MB", mb)
}

func dashboardIcon(name string) template.HTML {
	icons := map[string]string{
		"eye":        `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2 12s3-7 10-7 10 7 10 7-3 7-10 7S2 12 2 12Z"></path><circle cx="12" cy="12" r="3"></circle></svg>`,
		"vault":      `<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="4"></rect><circle cx="12" cy="12" r="4"></circle><path d="m12 8 0 8m-4-4h8M18 7v3"></path></svg>`,
		"copy":       `<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>`,
		"download":   `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><path d="M7 10l5 5 5-5"></path><path d="M12 15V3"></path></svg>`,
		"logout":     `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"></path><path d="M16 17l5-5-5-5"></path><path d="M21 12H9"></path></svg>`,
		"refresh":    `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M21 12a9 9 0 0 0-15-6.7L3 8"></path><path d="M3 3v5h5"></path><path d="M3 12a9 9 0 0 0 15 6.7L21 16"></path><path d="M21 21v-5h-5"></path></svg>`,
		"restore":    `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12a9 9 0 1 0 3-6.7"></path><path d="M3 3v6h6"></path><path d="M12 7v5l3 2"></path></svg>`,
		"shield":     `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10"></path><path d="M9 12l2 2 4-4"></path></svg>`,
		"toggle-off": `<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="2" y="7" width="20" height="10" rx="5"></rect><circle cx="7" cy="12" r="3"></circle></svg>`,
		"toggle-on":  `<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="2" y="7" width="20" height="10" rx="5"></rect><circle cx="17" cy="12" r="3"></circle></svg>`,
		"trash":      `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h18"></path><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"></path><path d="M10 11v6"></path><path d="M14 11v6"></path></svg>`,
		"user":       `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 21a8 8 0 0 0-16 0"></path><circle cx="12" cy="7" r="4"></circle></svg>`,
		"user-x":     `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"></path><circle cx="9" cy="7" r="4"></circle><path d="M17 8l5 5"></path><path d="M22 8l-5 5"></path></svg>`,
		"x":          `<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M18 6L6 18"></path><path d="M6 6l12 12"></path></svg>`,
	}
	return template.HTML(icons[name])
}

func isAdminDashboardUser(user storage.User) bool {
	return user.Role == storage.UserRoleAdmin
}

type dashboardPageData struct {
	Authenticated   bool
	OAuthConfigured bool
	LoginURL        string
	ServerURL       string
	Message         string
	User            storage.User
	APIKey          string
	Vaults          []storage.Vault
	DeletedVaults   []storage.Vault
	IsAdmin         bool
	Users           []storage.User
}

var dashboardTemplate = template.Must(template.New("dashboard.html").Funcs(template.FuncMap{
	"formatBytes": formatDashboardBytes,
	"icon":        dashboardIcon,
	"isAdminUser": isAdminDashboardUser,
	"formatTime":  formatDashboardTime,
	"totalBytes":  totalVaultBytes,
}).ParseFS(dashboardFiles, "dashboard/dashboard.html"))

func formatDashboardTime(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format("02 Jan 2006 - 15:04 UTC")
}

func totalVaultBytes(vaults []storage.Vault) int64 {
	var total int64
	for _, vault := range vaults {
		total += vault.SizeBytes
	}
	return total
}

func (s *Server) handleDashboardAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var name, contentType string
	switch r.URL.Path {
	case "/vault-dashboard/assets/dashboard.css":
		name, contentType = "dashboard.css", "text/css; charset=utf-8"
	case "/vault-dashboard/assets/dashboard.js":
		name, contentType = "dashboard.js", "text/javascript; charset=utf-8"
	case "/vault-dashboard/assets/favicon.svg":
		name, contentType = "favicon.svg", "image/svg+xml"
	default:
		http.NotFound(w, r)
		return
	}
	content, err := dashboardFiles.ReadFile("dashboard/" + name)
	if err != nil {
		http.Error(w, "Asset unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(content))
}
