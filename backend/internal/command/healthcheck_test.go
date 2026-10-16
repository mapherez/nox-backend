package command

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHealthcheckURL(t *testing.T) {
	for _, tc := range []struct{ addr, want string }{
		{":8080", "http://127.0.0.1:8080/v1/health"},
		{"0.0.0.0:9090", "http://127.0.0.1:9090/v1/health"},
		{"[::]:9090", "http://[::1]:9090/v1/health"},
		{"127.0.0.1:9090", "http://127.0.0.1:9090/v1/health"},
		{"[::1]:9090", "http://[::1]:9090/v1/health"},
		{"localhost:9090", "http://localhost:9090/v1/health"},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			got, err := healthcheckURL(tc.addr)
			if err != nil || got != tc.want {
				t.Fatalf("healthcheckURL(%q) = %q, %v; want %q", tc.addr, got, err, tc.want)
			}
		})
	}
	if err := checkHealth(context.Background(), "invalid"); err == nil {
		t.Fatal("invalid listen address should fail")
	}
}

func TestHealthcheckHTTPStatus(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent, http.StatusFound, http.StatusUnauthorized, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/health" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				// A redirect to a successful endpoint must not make the probe pass.
				if r.URL.Path == "/redirected" {
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
			}))
			defer server.Close()
			err := checkHealth(context.Background(), server.Listener.Addr().String())
			if (err == nil) != (status == http.StatusOK) {
				t.Fatalf("status %d: error = %v", status, err)
			}
		})
	}
}

func TestHealthcheckConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	addr := server.Listener.Addr().String()
	server.Close()
	if err := checkHealth(context.Background(), addr); err == nil {
		t.Fatal("an unreachable server should fail")
	}
}

func TestHealthcheckTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	started := time.Now()
	err := checkHealth(context.Background(), server.Listener.Addr().String())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected request timeout, got %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("probe exceeded Docker's 5-second timeout: %s", elapsed)
	}
}

func TestRunHealthcheck(t *testing.T) {
	args := os.Args
	os.Args = []string{"nox-backend", "healthcheck"}
	t.Cleanup(func() { os.Args = args })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	addr := server.Listener.Addr().String()
	_, port, _ := strings.Cut(addr, ":")
	for _, tc := range []struct{ name, canonical, legacy string }{
		{"canonical wildcard", ":" + port, "invalid"},
		{"legacy", "", addr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NOX_BACKEND_ADDR", tc.canonical)
			t.Setenv("NOX_SYNC_ADDR", tc.legacy)
			t.Setenv("NOX_BACKEND_DATA_DIR", "")
			if err := Run("test"); err != nil {
				t.Fatalf("healthcheck command failed: %v", err)
			}
		})
	}
	t.Setenv("NOX_BACKEND_ADDR", "invalid")
	if err := Run("test"); err == nil {
		t.Fatal("healthcheck command should return probe failures")
	}
}
