package cli

import (
	"strings"
	"testing"
	"time"
)

func TestParseServeFlagsDefaults(t *testing.T) {
	got, err := parseServeFlags([]string{}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.watch != false {
		t.Fatalf("default watch should be false")
	}
	if got.watchInterval != 60*time.Second {
		t.Fatalf("default watch interval should be 60s, got %v", got.watchInterval)
	}
	if got.dashboard != DashboardDisabled {
		t.Fatalf("default dashboard should be disabled, got %v", got.dashboard)
	}
}

func TestParseServeFlagsEnvFallbacks(t *testing.T) {
	env := map[string]string{
		"ULTRABASE_CONFIG_WATCH":          "true",
		"ULTRABASE_CONFIG_WATCH_INTERVAL": "30s",
		"ULTRABASE_DASHBOARD":             "readwrite",
	}
	got, err := parseServeFlags([]string{}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.watch || got.watchInterval != 30*time.Second || got.dashboard != DashboardReadwrite {
		t.Fatalf("env fallbacks ignored: %+v", got)
	}
}

func TestParseServeFlagsConfigSourceEnv(t *testing.T) {
	// ULTRABASE_CONFIG_SOURCE takes precedence over the legacy ULTRABASE_CONFIG.
	env := map[string]string{
		"ULTRABASE_CONFIG_SOURCE": "s3://bucket/new",
		"ULTRABASE_CONFIG":        "s3://bucket/legacy",
	}
	got, err := parseServeFlags([]string{}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.configPath != "s3://bucket/new" {
		t.Fatalf("configPath = %q, want s3://bucket/new", got.configPath)
	}

	// With only the legacy name set, it is still honored.
	got, err = parseServeFlags([]string{}, func(k string) string {
		return map[string]string{"ULTRABASE_CONFIG": "s3://bucket/legacy"}[k]
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.configPath != "s3://bucket/legacy" {
		t.Fatalf("legacy ULTRABASE_CONFIG ignored: %q", got.configPath)
	}
}

func TestParseServeFlagsExplicitWinsOverEnv(t *testing.T) {
	env := map[string]string{"ULTRABASE_DASHBOARD": "readwrite"}
	got, err := parseServeFlags([]string{"--dashboard", "readonly"}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.dashboard != DashboardReadonly {
		t.Fatalf("explicit --dashboard should win over env, got %v", got.dashboard)
	}
}

func TestParseServeFlagsValidation(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "interval too low",
			args:    []string{"--watch-interval", "5s"},
			wantErr: "--watch-interval must be at least 10s",
		},
		{
			name:    "unknown dashboard mode",
			args:    []string{"--dashboard", "true"},
			wantErr: "must be one of",
		},
		{
			name:    "unknown URI scheme",
			args:    []string{"--config", "ftp://example/file"},
			wantErr: "unsupported config backend",
		},
		{
			name:    "env var below minimum attributed to env name",
			args:    []string{},
			env:     map[string]string{"ULTRABASE_CONFIG_WATCH_INTERVAL": "5s"},
			wantErr: "ULTRABASE_CONFIG_WATCH_INTERVAL must be at least 10s",
		},
		{
			name:    "bad dashboard env attributed to env name",
			args:    []string{},
			env:     map[string]string{"ULTRABASE_DASHBOARD": "bogus"},
			wantErr: "ULTRABASE_DASHBOARD: --dashboard must be one of",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := tc.env
			_, err := parseServeFlags(tc.args, func(k string) string { return env[k] })
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
