package command

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func healthcheckURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("healthcheck address: %w", err)
	}
	// Wildcard listen addresses must be replaced with a connectable local address.
	if host == "" {
		host = "127.0.0.1"
	} else if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	}
	return "http://" + net.JoinHostPort(host, port) + "/v1/health", nil
}

func checkHealth(ctx context.Context, addr string) error {
	return checkHealthTLS(ctx, addr, false, "", "")
}

func checkHealthTLS(ctx context.Context, addr string, enabled bool, serverName, caFile string) error {
	url, err := healthcheckURL(addr)
	if err != nil {
		return err
	}
	if enabled {
		url = strings.Replace(url, "http://", "https://", 1)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}

	// Connect directly to the backend, regardless of HTTP proxy environment variables.
	transport := &http.Transport{}
	if enabled {
		config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
		if caFile != "" {
			roots, err := x509.SystemCertPool()
			if err != nil {
				roots = x509.NewCertPool()
			}
			body, err := os.ReadFile(caFile)
			if err != nil {
				return fmt.Errorf("read healthcheck TLS CA: %w", err)
			}
			if !roots.AppendCertsFromPEM(body) {
				return fmt.Errorf("healthcheck TLS CA contains no certificates")
			}
			config.RootCAs = roots
		}
		transport.TLSClientConfig = config
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   3 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck failed: HTTP %s", res.Status)
	}
	return nil
}
