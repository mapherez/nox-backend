package command

import (
	"context"
	"fmt"
	"net"
	"net/http"
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
	url, err := healthcheckURL(addr)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("healthcheck request: %w", err)
	}

	// Connect directly to the backend, regardless of HTTP proxy environment variables.
	transport := &http.Transport{}
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
