package cli

import (
	"strings"
	"testing"
	"time"
)

func TestParseDevFlagsDefaults(t *testing.T) {
	got, err := parseDevFlags([]string{}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.watch != true {
		t.Fatalf("default watch in dev should be true")
	}
	if got.watchInterval != 60*time.Second {
		t.Fatalf("default watch interval should be 60s")
	}
	if got.dashboard != DashboardReadwrite {
		t.Fatalf("default dashboard in dev should be readwrite")
	}
	if got.dbSrc != DevDBSourceDSN {
		t.Fatalf("dbSrc = %v, want DevDBSourceDSN", got.dbSrc)
	}
}

func TestParseDevFlagsOverrides(t *testing.T) {
	got, err := parseDevFlags(
		[]string{"--no-watch", "--dashboard", "disabled"},
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.watch != false {
		t.Fatalf("--no-watch should turn watch off")
	}
	if got.dashboard != DashboardDisabled {
		t.Fatalf("--dashboard disabled should be honored in dev")
	}
}

// TestParseDevFlagsUseDSNDeprecated verifies that --use-dsn is still accepted
// (hidden/deprecated no-op) and resolves to DevDBSourceDSN.
func TestParseDevFlagsUseDSNDeprecated(t *testing.T) {
	got, err := parseDevFlags([]string{"--use-dsn"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("--use-dsn should still be accepted (deprecated no-op): %v", err)
	}
	if got.dbSrc != DevDBSourceDSN {
		t.Fatalf("dbSrc = %v, want DevDBSourceDSN", got.dbSrc)
	}
}

// TestParseDevFlagsVerboseFromEnv verifies the standardized INSTANCEZ_VERBOSE
// env var binds to the --verbose flag (it had no env binding before).
func TestParseDevFlagsVerboseFromEnv(t *testing.T) {
	got, err := parseDevFlags([]string{}, func(k string) string {
		return map[string]string{"INSTANCEZ_VERBOSE": "true"}[k]
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.verbose {
		t.Fatal("INSTANCEZ_VERBOSE=true should set verbose")
	}
}

// TestParseDevFlagsRemovedFlagsUnknown verifies that the removed --use-docker
// and --use-cloud-ephemeral flags are now unknown and cause a parse error.
func TestParseDevFlagsRemovedFlagsUnknown(t *testing.T) {
	for _, flag := range []string{"--use-docker", "--use-cloud-ephemeral", "--use-cloud"} {
		_, err := parseDevFlags([]string{flag}, func(string) string { return "" })
		if err == nil {
			t.Errorf("%s: expected parse error for removed flag, got nil", flag)
			continue
		}
		if !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s: error %q should mention 'unknown flag'", flag, err.Error())
		}
	}
}

// TestParseDevFlagsEmbeddedPG verifies --embedded-pg sets DevDBSourceEmbedded.
func TestParseDevFlagsEmbeddedPG(t *testing.T) {
	got, err := parseDevFlags([]string{"--embedded-pg"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.dbSrc != DevDBSourceEmbedded {
		t.Fatalf("dbSrc = %v, want DevDBSourceEmbedded", got.dbSrc)
	}
	if got.resetPG {
		t.Fatal("resetPG should be false when --reset-pg not passed")
	}
}

// TestParseDevFlagsResetPGRequiresEmbedded verifies --reset-pg without --embedded-pg is an error.
func TestParseDevFlagsResetPGRequiresEmbedded(t *testing.T) {
	_, err := parseDevFlags([]string{"--reset-pg"}, func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error for --reset-pg without --embedded-pg")
	}
	if !strings.Contains(err.Error(), "--reset-pg requires --embedded-pg") {
		t.Fatalf("error %q should mention --reset-pg requires --embedded-pg", err.Error())
	}
}

// TestParseDevFlagsEmbeddedPGWithReset verifies --embedded-pg --reset-pg is accepted.
func TestParseDevFlagsEmbeddedPGWithReset(t *testing.T) {
	got, err := parseDevFlags([]string{"--embedded-pg", "--reset-pg"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.dbSrc != DevDBSourceEmbedded {
		t.Fatalf("dbSrc = %v, want DevDBSourceEmbedded", got.dbSrc)
	}
	if !got.resetPG {
		t.Fatal("resetPG should be true when --reset-pg is passed")
	}
}

// TestParseDevFlagsPGDataDir verifies pgDataDir is derived from configPath.
func TestParseDevFlagsPGDataDir(t *testing.T) {
	got, err := parseDevFlags(
		[]string{"--embedded-pg", "--config", "/tmp/myproject/instancez.yaml"},
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := "/tmp/myproject/pgdata"
	if got.pgDataDir != want {
		t.Fatalf("pgDataDir = %q, want %q", got.pgDataDir, want)
	}
}
