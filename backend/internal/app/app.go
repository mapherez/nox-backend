package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
)

// Config contains the minimal backend runtime configuration.
type Config struct {
	DirectMCPDisabled, SuiteMCPEnabled                           bool
	Addr                                                         string
	DataDir                                                      string
	Version                                                      string
	PublicURL                                                    string
	GoogleClientID                                               string
	GoogleClientSecret                                           string
	AdminEmails                                                  []string
	SuiteReportInterval, SuiteObservationFreshness               time.Duration
	TLSCertFile, TLSKeyFile                                      string
	SuiteAllowedCIDRs, SuiteAllowedPorts, SuiteTrustedProxyCIDRs []string
}

// Run starts the backend HTTP server.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "/data"
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}

	if err := validateSuiteConfig(cfg); err != nil {
		return err
	}
	if err := ensureDataDirs(cfg.DataDir); err != nil {
		return err
	}

	store, err := storage.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("close sqlite database: %v", err)
		}
	}()
	if err := store.BootstrapAdmins(ctx, cfg.AdminEmails); err != nil {
		return err
	}

	server := NewServer(cfg, store)
	serverCtx, stopServer := context.WithCancel(ctx)
	defer stopServer()
	go server.monitorStaleLocks(serverCtx)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-serverCtx.Done():
				return
			case <-ticker.C:
				if err := store.MaintainSuiteTrust(serverCtx); err != nil && serverCtx.Err() == nil {
					log.Printf("Suite trust housekeeping failed")
				}
			}
		}
	}()

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("nox-backend listening on %s data_dir=%s", cfg.Addr, cfg.DataDir)
		if cfg.TLSCertFile != "" {
			errCh <- httpServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			errCh <- httpServer.ListenAndServe()
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func ensureDataDirs(dataDir string) error {
	dirs := []string{
		dataDir,
		filepath.Join(dataDir, "blobs"),
		filepath.Join(dataDir, "staging"),
		filepath.Join(dataDir, "logs"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create data directory %q: %w", dir, err)
		}
	}

	return nil
}
