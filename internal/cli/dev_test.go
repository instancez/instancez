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

// TestParseDevFlagsUseCloud verifies that --use-cloud resolves to DevDBSourceCloud.
func TestParseDevFlagsUseCloud(t *testing.T) {
	got, err := parseDevFlags([]string{"--use-cloud"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.dbSrc != DevDBSourceCloud {
		t.Fatalf("dbSrc = %v, want DevDBSourceCloud", got.dbSrc)
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
	for _, flag := range []string{"--use-docker", "--use-cloud-ephemeral"} {
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
