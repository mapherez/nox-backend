package command

import (
	"context"
	"fmt"
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
		if len(os.Args) != 2 || os.Args[1] != "migrate-database" {
			return fmt.Errorf("usage: nox-backend [migrate-database]")
		}
		return storage.MigrateDatabaseFile(context.Background(), getenv("NOX_SYNC_DATA_DIR", "/data"))
	}
	cfg := app.Config{
		Addr:               getenv("NOX_SYNC_ADDR", ":8080"),
		DataDir:            getenv("NOX_SYNC_DATA_DIR", "/data"),
		Version:            runtimeVersion(buildVersion),
		PublicURL:          getenv("NOX_SYNC_PUBLIC_URL", ""),
		GoogleClientID:     getenv("NOX_SYNC_GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getenv("NOX_SYNC_GOOGLE_CLIENT_SECRET", ""),
		AdminEmails:        splitCSV(getenv("NOX_SYNC_ADMIN_EMAILS", "")),
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
