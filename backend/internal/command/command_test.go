package command

import (
	"os"
	"testing"
	"time"
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
			for _, key := range []string{"ADDR", "DATA_DIR", "PUBLIC_URL", "VERSION"} {
				t.Setenv("NOX_BACKEND_"+key, tc.canonical)
				t.Setenv("NOX_SYNC_"+key, tc.legacy)
				if got := getenv("NOX_SYNC_"+key, "fallback"); got != tc.want {
					t.Fatalf("%s: got %q; want %q", key, got, tc.want)
				}
			}
		})
	}
}

func TestSuiteIntervalsValidateAndSupportAliases(t *testing.T) {
	for _, suffix := range []string{"REPORT_INTERVAL_MS", "OBSERVATION_FRESHNESS_MS"} {
		name := "NOX_SYNC_SUITE_" + suffix
		canonical := "NOX_BACKEND_SUITE_" + suffix
		t.Setenv(name, "12000")
		t.Setenv(canonical, "")
		duration, err := suiteInterval(name, 15000)
		if err != nil || duration != 12*time.Second {
			t.Fatal("legacy interval ignored", err)
		}
		t.Setenv(canonical, "23000")
		duration, err = suiteInterval(name, 15000)
		if err != nil || duration != 23*time.Second {
			t.Fatal("canonical interval ignored", err)
		}
		for _, invalid := range []string{"0", "-1", "1.5", "NaN", "9223372036854775807"} {
			t.Setenv(canonical, invalid)
			if _, err = suiteInterval(name, 15000); err == nil {
				t.Fatalf("invalid interval accepted: %s", invalid)
			}
		}
	}
}

func TestMCPConfigurationFlags(t *testing.T) {
	for _, name := range []string{"DIRECT_MCP_ENABLED", "SUITE_MCP_ENABLED"} {
		legacy := "NOX_SYNC_" + name
		canonical := "NOX_BACKEND_" + name
		t.Setenv(legacy, "")
		t.Setenv(canonical, "")
		got, err := mcpEnabled(legacy, true)
		if err != nil || !got {
			t.Fatal("default", err)
		}
		t.Setenv(legacy, "false")
		got, err = mcpEnabled(legacy, true)
		if err != nil || got {
			t.Fatal("legacy", err)
		}
		t.Setenv(canonical, "true")
		got, err = mcpEnabled(legacy, false)
		if err != nil || !got {
			t.Fatal("canonical", err)
		}
		t.Setenv(canonical, "typo")
		if _, err = mcpEnabled(legacy, false); err == nil {
			t.Fatal("invalid flag ignored")
		}
	}
}
