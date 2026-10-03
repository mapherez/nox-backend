package main

import (
	"context"
	"log"
	"os"

	"github.com/mapherez/nox-sync/backend/internal/app"
)

// buildVersion is supplied by release builds using -ldflags -X main.buildVersion.
var buildVersion string

func main() {
	cfg := app.Config{
		Addr:               getenv("NOX_SYNC_ADDR", ":8080"),
		DataDir:            getenv("NOX_SYNC_DATA_DIR", "/data"),
		Version:            runtimeVersion(),
		PublicURL:          getenv("NOX_SYNC_PUBLIC_URL", ""),
		GoogleClientID:     getenv("NOX_SYNC_GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getenv("NOX_SYNC_GOOGLE_CLIENT_SECRET", ""),
		AdminEmails:        splitCSV(getenv("NOX_SYNC_ADMIN_EMAILS", "")),
	}

	if err := app.Run(context.Background(), cfg); err != nil {
		log.Fatal(err)
	}
}

func runtimeVersion() string {
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
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
