package command

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

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
			case "migrate-database":
				return storage.MigrateDatabaseFile(context.Background(), getenv("NOX_SYNC_DATA_DIR", "/data"))
			}
		}
		return fmt.Errorf("usage: nox-backend [healthcheck|migrate-database]")
	}
	cfg := app.Config{
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
