package cli

import (
	"strings"
	"testing"
	"time"
)

func TestParseDevFlagsDefaults(t *testing.T) {
	got, err := parseDevFlags([]string{"--use-dsn"}, func(string) string { return "" })
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
		[]string{"--use-dsn", "--no-watch", "--dashboard", "disabled"},
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

// TestParseDevFlagsRequiresDBSource locks the contract that `ultra dev`
// refuses to start without an explicit data-source choice. No defaulting,
// no auto-cloud — the user must say where the DB lives.
func TestParseDevFlagsRequiresDBSource(t *testing.T) {
	_, err := parseDevFlags([]string{}, func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error when no --use-* flag is supplied")
	}
	if !strings.Contains(err.Error(), "--use-dsn") {
		t.Errorf("error %q should mention the --use-* flags", err.Error())
	}
}

// TestParseDevFlagsMutuallyExclusive prevents the contradictory invocation
// where two data sources are requested simultaneously.
func TestParseDevFlagsMutuallyExclusive(t *testing.T) {
	_, err := parseDevFlags([]string{"--use-dsn", "--use-docker"}, func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error when two --use-* flags are supplied")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error %q should mention 'mutually exclusive'", err.Error())
	}
}

// TestSelectDevDBSourceMatrix exercises the picker directly without going
// through the flag set, so the precedence/error logic is exhaustively covered.
func TestSelectDevDBSourceMatrix(t *testing.T) {
	cases := []struct {
		name                                       string
		useDSN, useDocker, useCloudEphemeral       bool
		wantSrc                                    DevDBSource
		wantErrSubstr                              string
	}{
		{"none", false, false, false, DevDBSourceUnset, "exactly one"},
		{"dsn", true, false, false, DevDBSourceDSN, ""},
		{"docker", false, true, false, DevDBSourceDocker, ""},
		{"cloud", false, false, true, DevDBSourceCloudEphemeral, ""},
		{"dsn+docker", true, true, false, DevDBSourceUnset, "mutually exclusive"},
		{"dsn+cloud", true, false, true, DevDBSourceUnset, "mutually exclusive"},
		{"all three", true, true, true, DevDBSourceUnset, "mutually exclusive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectDevDBSource(tc.useDSN, tc.useDocker, tc.useCloudEphemeral)
			if tc.wantErrSubstr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.wantSrc {
					t.Fatalf("got src %v, want %v", got, tc.wantSrc)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSubstr)
			}
		})
	}
}
