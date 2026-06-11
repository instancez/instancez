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
		"INSTANCEZ_WATCH":          "true",
		"INSTANCEZ_WATCH_INTERVAL": "30s",
		"INSTANCEZ_DASHBOARD":      "readwrite",
	}
	got, err := parseServeFlags([]string{}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.watch || got.watchInterval != 30*time.Second || got.dashboard != DashboardReadwrite {
		t.Fatalf("env fallbacks ignored: %+v", got)
	}
}

func TestParseServeFlagsConfigEnv(t *testing.T) {
	// The single standardized name sets the config path.
	got, err := parseServeFlags([]string{}, func(k string) string {
		return map[string]string{"INSTANCEZ_CONFIG": "s3://bucket/new"}[k]
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.configPath != "s3://bucket/new" {
		t.Fatalf("configPath = %q, want s3://bucket/new", got.configPath)
	}

	// The old INSTANCEZ_CONFIG_SOURCE name is no longer bound.
	got, err = parseServeFlags([]string{}, func(k string) string {
		return map[string]string{"INSTANCEZ_CONFIG_SOURCE": "s3://bucket/old"}[k]
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.configPath != "instancez.yaml" {
		t.Fatalf("INSTANCEZ_CONFIG_SOURCE should no longer bind; configPath = %q, want default", got.configPath)
	}
}

func TestParseServeFlagsExplicitWinsOverEnv(t *testing.T) {
	env := map[string]string{"INSTANCEZ_DASHBOARD": "readwrite"}
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
			env:     map[string]string{"INSTANCEZ_WATCH_INTERVAL": "5s"},
			wantErr: "INSTANCEZ_WATCH_INTERVAL must be at least 10s",
		},
		{
			name:    "bad dashboard env attributed to env name",
			args:    []string{},
			env:     map[string]string{"INSTANCEZ_DASHBOARD": "bogus"},
			wantErr: "INSTANCEZ_DASHBOARD: --dashboard must be one of",
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

func TestParseDotenvFlag_ServeDefault(t *testing.T) {
	opts, err := parseServeFlags([]string{}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.dotenvWritable {
		t.Error("expected dotenvWritable=false by default for serve")
	}
}

func TestParseDotenvFlag_ServeExplicit(t *testing.T) {
	opts, err := parseServeFlags(
		[]string{"--dashboard-write-dotenv", "--dotenv-path", "/etc/app.env"},
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.dotenvWritable {
		t.Error("expected dotenvWritable=true")
	}
	if opts.dotenvPath != "/etc/app.env" {
		t.Errorf("dotenvPath = %q, want /etc/app.env", opts.dotenvPath)
	}
}

func TestParseDotenvFlag_ServeRequiresDotenvPath(t *testing.T) {
	_, err := parseServeFlags(
		[]string{"--dashboard-write-dotenv"},
		func(string) string { return "" },
	)
	if err == nil {
		t.Fatal("expected error when --dashboard-write-dotenv set without --dotenv-path")
	}
	if !strings.Contains(err.Error(), "dotenv-path") {
		t.Errorf("error %q should mention dotenv-path", err.Error())
	}
}

func TestParseDotenvFlag_DevDefault(t *testing.T) {
	opts, err := parseDevFlags([]string{}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.dotenvWritable {
		t.Error("expected dotenvWritable=true by default for dev")
	}
	if opts.dotenvPath != ".development.env" {
		t.Errorf("dotenvPath = %q, want .development.env", opts.dotenvPath)
	}
}
