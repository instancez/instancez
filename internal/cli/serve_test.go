package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
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

func TestExtractCobraArgs(t *testing.T) {
	cases := []struct {
		name     string
		setFlags func(fs *pflag.FlagSet)
		want     []string
	}{
		{
			name:     "no flags set",
			setFlags: func(fs *pflag.FlagSet) {},
			want:     nil,
		},
		{
			name: "string flag set",
			setFlags: func(fs *pflag.FlagSet) {
				_ = fs.Set("config", "s3://bucket/key")
			},
			want: []string{"--config", "s3://bucket/key"},
		},
		{
			name: "bool flag set true",
			setFlags: func(fs *pflag.FlagSet) {
				_ = fs.Set("watch", "true")
			},
			want: []string{"--watch"},
		},
		{
			name: "bool flag set false explicitly",
			setFlags: func(fs *pflag.FlagSet) {
				_ = fs.Set("watch", "false")
			},
			want: []string{"--watch=false"},
		},
		{
			name: "duration flag set",
			setFlags: func(fs *pflag.FlagSet) {
				_ = fs.Set("watch-interval", "30s")
			},
			want: []string{"--watch-interval", "30s"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			fs.String("config", "ultrabase.yaml", "")
			fs.Bool("watch", false, "")
			fs.Duration("watch-interval", 60*time.Second, "")
			tc.setFlags(fs)
			got := extractCobraArgs(fs)
			if !slicesEqual(got, tc.want) {
				t.Fatalf("extractCobraArgs = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExtractCobraArgsRoundTrip(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("config", "ultrabase.yaml", "")
	fs.Bool("watch", false, "")
	fs.Duration("watch-interval", 60*time.Second, "")
	fs.String("dashboard", "disabled", "")
	fs.Int("port", 0, "")

	_ = fs.Set("config", "s3://bucket/key")
	_ = fs.Set("watch", "false")
	_ = fs.Set("watch-interval", "30s")
	_ = fs.Set("dashboard", "readonly")
	_ = fs.Set("port", "9090")

	args := extractCobraArgs(fs)

	opts, err := parseServeFlags(args, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.configPath != "s3://bucket/key" {
		t.Fatalf("configPath = %q", opts.configPath)
	}
	if opts.watch != false {
		t.Fatalf("watch should be false (explicit)")
	}
	if opts.watchInterval != 30*time.Second {
		t.Fatalf("watchInterval = %s", opts.watchInterval)
	}
	if opts.dashboard != DashboardReadonly {
		t.Fatalf("dashboard = %s", opts.dashboard)
	}
	if opts.port != 9090 {
		t.Fatalf("port = %d", opts.port)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
