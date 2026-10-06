package command

import (
	"os"
	"testing"
)

func TestRuntimeVersion(t *testing.T) {
	for _, tc := range []struct {
		name, built, env, want string
		unset                  bool
	}{
		{name: "local", want: "dev", unset: true},
		{name: "empty local override", want: "dev"},
		{name: "build", built: "1.2.3", want: "1.2.3", unset: true},
		{name: "empty override", built: "1.2.3", want: "1.2.3"},
		{name: "override", built: "1.2.3", env: "runtime-test", want: "runtime-test"},
		{name: "local override", env: "runtime-test", want: "runtime-test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NOX_BACKEND_VERSION", "")
			t.Setenv("NOX_SYNC_VERSION", tc.env)
			if tc.unset {
				if err := os.Unsetenv("NOX_SYNC_VERSION"); err != nil {
					t.Fatal(err)
				}
			}
			if got := runtimeVersion(tc.built); got != tc.want {
				t.Fatalf("version = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestEnvironmentAliases(t *testing.T) {
	for _, tc := range []struct{ name, canonical, legacy, want string }{
		{"defaults", "", "", "fallback"},
		{"legacy", "", "legacy-value", "legacy-value"},
		{"canonical", "new-value", "", "new-value"},
		{"precedence", "new-value", "legacy-value", "new-value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"ADDR", "DATA_DIR", "PUBLIC_URL", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "ADMIN_EMAILS", "VERSION"} {
				t.Setenv("NOX_BACKEND_"+key, tc.canonical)
				t.Setenv("NOX_SYNC_"+key, tc.legacy)
				if got := getenv("NOX_SYNC_"+key, "fallback"); got != tc.want {
					t.Fatalf("%s: got %q; want %q", key, got, tc.want)
				}
			}
		})
	}
}
