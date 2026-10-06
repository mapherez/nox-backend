package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardAssetsAreEmbeddedAndRestricted(t *testing.T) {
	server := NewServer(Config{}, nil)
	for _, tc := range []struct{ name, contentType string }{
		{"dashboard.css", "text/css"}, {"dashboard.js", "text/javascript"}, {"favicon.svg", "image/svg+xml"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			res := httptest.NewRecorder()
			server.Routes().ServeHTTP(res, httptest.NewRequest(method, "/vault-dashboard/assets/"+tc.name, nil))
			if res.Code != 200 || !strings.HasPrefix(res.Header().Get("Content-Type"), tc.contentType) {
				t.Fatalf("asset %s: %d, %s", tc.name, res.Code, res.Header())
			}
			if method == http.MethodGet && res.Body.Len() == 0 {
				t.Fatal("empty embedded asset")
			}
		}
	}
	for _, path := range []string{"dashboard.html", "unknown", "database.db"} {
		res := httptest.NewRecorder()
		server.Routes().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/vault-dashboard/assets/"+path, nil))
		if res.Code != 404 {
			t.Fatalf("unexpectedly served %s", path)
		}
	}
	res := httptest.NewRecorder()
	server.Routes().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/vault-dashboard/assets/dashboard.js", nil))
	if res.Code != http.StatusMethodNotAllowed {
		t.Fatal("asset accepted POST")
	}
}
