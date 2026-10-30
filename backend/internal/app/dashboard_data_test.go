package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mapherez/nox-backend/backend/internal/storage"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboardDataSessionIsolation(t *testing.T) {
	server, store, admin, _ := newTestServer(t)
	member, err := store.UpsertAllowedUser(context.Background(), "member@example.com", storage.UserRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		user        storage.User
		auth, admin bool
	}{{"anonymous", storage.User{}, false, false}, {"admin", admin, true, true}, {"member", member, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/dashboard?userId="+admin.ID+"&isAdmin=true", nil)
			if tc.auth {
				token, err := store.CreateWebSession(context.Background(), tc.user.ID, webSessionDuration)
				if err != nil {
					t.Fatal(err)
				}
				req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
			} else {
				key, _ := store.CurrentAPIKey(context.Background(), admin.ID)
				req.Header.Set("Authorization", "Bearer "+key)
			}
			res := httptest.NewRecorder()
			server.Routes().ServeHTTP(res, req)
			var data dashboardPageData
			if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &data) != nil || data.Authenticated != tc.auth || data.IsAdmin != tc.admin {
				t.Fatalf("dashboard result: %d %s", res.Code, res.Body.String())
			}
			if res.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private view must not be cached")
			}
			if strings.Contains(res.Body.String(), "GoogleSub") || strings.Contains(res.Body.String(), "googleSub") {
				t.Fatal("unnecessary Google identity disclosed")
			}
			if tc.auth {
				key, _ := store.CurrentAPIKey(context.Background(), tc.user.ID)
				if data.User.ID != tc.user.ID || data.APIKey != key {
					t.Fatal("wrong user/key projected")
				}
			} else if data.APIKey != "" || data.User.ID != "" {
				t.Fatal("anonymous view disclosed credentials")
			}
			if !tc.admin && (len(data.Users) != 0 || len(data.Connections) != 0) {
				t.Fatal("user registry leaked")
			}
		})
	}
}
func TestDashboardProjectionPreservesInt64(t *testing.T) {
	data, err := json.Marshal(presentDashboardVaults([]storage.Vault{{ID: "v", Revision: math.MaxInt64, SizeBytes: math.MaxInt64}}))
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if json.Unmarshal(data, &decoded) != nil {
		t.Fatal("invalid JSON")
	}
	if decoded[0]["revision"] != "9223372036854775807" || decoded[0]["sizeBytes"] != "9223372036854775807" {
		t.Fatalf("presentation loses int64 precision: %s", data)
	}
}
func TestAdministrationOperationsEnforcePolicyWithoutUI(t *testing.T) {
	server, store, admin, _ := newTestServer(t)
	ctx := context.Background()
	member, err := store.UpsertAllowedUser(ctx, "member@example.com", storage.UserRoleUser)
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.addManagedUser(ctx, member, "other@example.com", "USER")
	if !errors.Is(err, storage.ErrForbidden) {
		t.Fatal("non-admin added account")
	}
	for _, err := range []error{server.setManagedUserRole(ctx, member, admin.ID, "USER"), server.setManagedUserStatus(ctx, member, admin.ID, "DISABLED"), server.deleteManagedUser(ctx, member, admin.ID)} {
		if !errors.Is(err, storage.ErrForbidden) {
			t.Fatal("operation bypassed admin authorization")
		}
	}
	for _, err := range []error{server.setManagedUserRole(ctx, admin, admin.ID, "USER"), server.setManagedUserStatus(ctx, admin, admin.ID, "DISABLED"), server.deleteManagedUser(ctx, admin, admin.ID)} {
		if !errors.Is(err, storage.ErrBadRequest) {
			t.Fatal("operation bypassed existing admin protections")
		}
	}
	if err := server.setManagedUserStatus(ctx, admin, member.ID, "DISABLED"); err != nil {
		t.Fatal(err)
	}
}
