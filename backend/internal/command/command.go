package command

import (
	"context"
	"fmt"
	"github.com/mapherez/nox-backend/backend/internal/noxlogin"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/app"
	"github.com/mapherez/nox-backend/backend/internal/storage"
)

// Run starts NoX Backend or executes its maintenance commands.
func Run(buildVersion string) error {
	if len(os.Args) > 1 {
		if len(os.Args) == 2 {
			switch os.Args[1] {
			case "auth-setup":
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				data, err := storage.Open(ctx, getenv("NOX_SYNC_DATA_DIR", "/data"))
				if err != nil {
					return err
				}
				defer data.Close()
				err = noxlogin.Setup(ctx, data, os.Getenv("NOX_AUTH_PUBLIC_URL"), getenv("NOX_SYNC_PUBLIC_URL", ""), envDefault("NOX_AUTH_ADMIN_SOCKET", "/run/nox-auth-admin/control.sock"), filepath.Join(getenv("NOX_SYNC_SUITE_SOCKET_DIR", "/run/nox-suite"), "nox-auth.sock"))
				if err == nil {
					log.Print("NoX Login client configured.")
				}
				return err
			case "healthcheck":
				origin, _ := url.Parse(getenv("NOX_SYNC_PUBLIC_URL", ""))
				name := ""
				if origin != nil {
					name = origin.Hostname()
				}
				return checkHealthTLS(context.Background(), getenv("NOX_SYNC_ADDR", ":8080"), getenv("NOX_SYNC_TLS_CERT_FILE", "") != "", name, getenv("NOX_SYNC_TLS_CA_FILE", ""))
			case "reset-suite-event-cursors":
				store, err := storage.Open(context.Background(), getenv("NOX_SYNC_DATA_DIR", "/data"))
				if err != nil {
					return err
				}
				defer store.Close()
				return store.ResetSuiteEventCursors(context.Background())
			case "migrate-database":
				return storage.MigrateDatabaseFile(context.Background(), getenv("NOX_SYNC_DATA_DIR", "/data"))
			}
		}
		return fmt.Errorf("usage: nox-backend [healthcheck|auth-setup|migrate-database|reset-suite-event-cursors]")
	}
	report, err := suiteInterval("NOX_SYNC_SUITE_REPORT_INTERVAL_MS", 15000)
	if err != nil {
		return err
	}
	freshness, err := suiteInterval("NOX_SYNC_SUITE_OBSERVATION_FRESHNESS_MS", 45000)
	if err != nil {
		return err
	}
	direct, err := mcpEnabled("NOX_SYNC_DIRECT_MCP_ENABLED", true)
	if err != nil {
		return err
	}
	suite, err := mcpEnabled("NOX_SYNC_SUITE_MCP_ENABLED", false)
	if err != nil {
		return err
	}
	cfg := app.Config{
		DirectMCPDisabled: !direct, SuiteMCPEnabled: suite,
		SuiteSocketDir:      getenv("NOX_SYNC_SUITE_SOCKET_DIR", ""),
		SuiteReportInterval: report, SuiteObservationFreshness: freshness,
		Addr:          getenv("NOX_SYNC_ADDR", ":8080"),
		DataDir:       getenv("NOX_SYNC_DATA_DIR", "/data"),
		Version:       runtimeVersion(buildVersion),
		PublicURL:     getenv("NOX_SYNC_PUBLIC_URL", ""),
		AuthPublicURL: os.Getenv("NOX_AUTH_PUBLIC_URL"),
		TLSCertFile:   getenv("NOX_SYNC_TLS_CERT_FILE", ""), TLSKeyFile: getenv("NOX_SYNC_TLS_KEY_FILE", ""),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, cfg)
}

func runtimeVersion(buildVersion string) string {
	if version := getenv("NOX_SYNC_VERSION", buildVersion); version != "" {
		return version
	}
	return "dev"
}

func getenv(key, fallback string) string {
	value := os.Getenv(strings.Replace(key, "NOX_SYNC_", "NOX_BACKEND_", 1))
	if value == "" {
		value = os.Getenv(key)
	}
	if value == "" {
		return fallback
	}
	return value
}

func suiteInterval(name string, fallback int64) (time.Duration, error) {
	value, err := strconv.ParseInt(getenv(name, strconv.FormatInt(fallback, 10)), 10, 64)
	if err != nil || value <= 0 || value > int64((1<<63-1)/int64(time.Millisecond)) {
		return 0, fmt.Errorf("%s must be a positive millisecond interval", strings.Replace(name, "NOX_SYNC_", "NOX_BACKEND_", 1))
	}
	return time.Duration(value) * time.Millisecond, nil
}

func mcpEnabled(name string, fallback bool) (bool, error) {
	raw := getenv(name, strconv.FormatBool(fallback))
	if raw != "true" && raw != "false" {
		return false, fmt.Errorf("%s must be true or false", strings.Replace(name, "NOX_SYNC_", "NOX_BACKEND_", 1))
	}
	return raw == "true", nil
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
