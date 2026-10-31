package command

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/app"
	"github.com/mapherez/nox-backend/backend/internal/storage"
)

// Run is shared by the canonical command and the legacy entrypoint.
func Run(buildVersion string) error {
	if len(os.Args) > 1 {
		if len(os.Args) == 2 {
			switch os.Args[1] {
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
		return fmt.Errorf("usage: nox-backend [healthcheck|migrate-database|reset-suite-event-cursors]")
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
		SuiteReportInterval: report, SuiteObservationFreshness: freshness,
		Addr:               getenv("NOX_SYNC_ADDR", ":8080"),
		DataDir:            getenv("NOX_SYNC_DATA_DIR", "/data"),
		Version:            runtimeVersion(buildVersion),
		PublicURL:          getenv("NOX_SYNC_PUBLIC_URL", ""),
		GoogleClientID:     getenv("NOX_SYNC_GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getenv("NOX_SYNC_GOOGLE_CLIENT_SECRET", ""),
		AdminEmails:        splitCSV(getenv("NOX_SYNC_ADMIN_EMAILS", "")),
		TLSCertFile:        getenv("NOX_SYNC_TLS_CERT_FILE", ""), TLSKeyFile: getenv("NOX_SYNC_TLS_KEY_FILE", ""),
		SuiteAllowedCIDRs:      splitCSV(getenv("NOX_SYNC_SUITE_ALLOWED_CIDRS", "")),
		SuiteAllowedPorts:      splitCSV(getenv("NOX_SYNC_SUITE_ALLOWED_PORTS", "443")),
		SuiteTrustedProxyCIDRs: splitCSV(getenv("NOX_SYNC_SUITE_TRUSTED_PROXY_CIDRS", "")),
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

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	items := []string{}
	start := 0
	for i, r := range value {
		if r == ',' {
			if item := value[start:i]; item != "" {
				items = append(items, item)
			}
			start = i + 1
		}
	}
	if start <= len(value) {
		if item := value[start:]; item != "" {
			items = append(items, item)
		}
	}
	return items
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
