package noxlogin

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mapherez/nox-backend/backend/internal/storage"
)

func TestSetupPersistsClientAndRejectsOldAuthAndIssuerReplacement(t *testing.T) {
	directory, err := os.MkdirTemp("", "nl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	data, err := storage.Open(context.Background(), filepath.Join(directory, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	socket := filepath.Join(directory, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var supported atomic.Bool
	supported.Store(true)
	var mu sync.Mutex
	var registration struct {
		ClientSecret string   `json:"client_secret"`
		RedirectURIs []string `json:"redirect_uris"`
		SuiteLogout  bool     `json:"suite_logout"`
	}
	var lastID string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/v1/clients/") {
			if r.Method != "PUT" {
				t.Error("wrong enrollment method")
			}
			if err := json.NewDecoder(r.Body).Decode(&registration); err != nil {
				t.Error(err)
			}
			lastID = strings.TrimPrefix(r.URL.Path, "/v1/clients/")
			w.WriteHeader(200)
			return
		}
		id, secret, ok := r.BasicAuth()
		if !ok || id != lastID || secret != registration.ClientSecret {
			http.Error(w, "bad fixture credentials", 401)
			return
		}
		if !supported.Load() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowed":false}`))
	})
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = Setup(ctx, data, "https://auth.example.test", "http://backend.home.arpa", socket, socket); err != nil {
		t.Fatal(err)
	}
	first, found, err := data.NoXLoginConfiguration(ctx)
	if err != nil || !found {
		t.Fatal(err)
	}
	mu.Lock()
	validRegistration := registration.SuiteLogout && len(registration.ClientSecret) >= 43 && len(registration.RedirectURIs) == 1 && registration.RedirectURIs[0] == "http://backend.home.arpa/auth/nox/callback"
	mu.Unlock()
	if !validRegistration {
		t.Fatal("incorrect registration")
	}

	if err = Setup(ctx, data, first.Issuer, first.Origin, socket, socket); err != nil {
		t.Fatal(err)
	}
	again, _, err := data.NoXLoginConfiguration(ctx)
	if err != nil || again != first {
		t.Fatal("setup changed client identity", err)
	}
	if err = Setup(ctx, data, "https://different.example.test", first.Origin, socket, socket); err == nil {
		t.Fatal("issuer replacement allowed")
	}
	supported.Store(false)
	if err = Setup(ctx, data, first.Issuer, first.Origin, socket, socket); err == nil || !strings.Contains(err.Error(), "update Auth") {
		t.Fatal("old Auth not diagnosed", err)
	}
	for _, origin := range []string{"http://backend.test/apps/backend", "http://backend.test?x=1", "http://user@backend.test", "http://backend.test#fragment"} {
		if err = Setup(ctx, data, first.Issuer, origin, socket, socket); err == nil {
			t.Fatal("invalid origin enrolled", origin)
		}
	}
}
