package main

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
			original := buildVersion
			t.Cleanup(func() { buildVersion = original })
			buildVersion = tc.built
			t.Setenv("NOX_SYNC_VERSION", tc.env)
			if tc.unset {
				if err := os.Unsetenv("NOX_SYNC_VERSION"); err != nil {
					t.Fatal(err)
				}
			}
			if got := runtimeVersion(); got != tc.want {
				t.Fatalf("version = %q; want %q", got, tc.want)
			}
		})
	}
}
