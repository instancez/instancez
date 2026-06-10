package funcs

import (
	"strings"
	"testing"

	"github.com/saedx1/instancez/internal/domain"
)

// TestAsInstancezEnvRef exercises the ref-detection helper at the unit level.
func TestAsInstancezEnvRef(t *testing.T) {
	cases := []struct {
		in      string
		wantRef string
		wantOK  bool
	}{
		{"${INSTANCEZ_ENV_FOO}", "INSTANCEZ_ENV_FOO", true},
		{"${INSTANCEZ_ENV_STRIPE_KEY}", "INSTANCEZ_ENV_STRIPE_KEY", true},
		{"${INSTANCEZ_ENV_A_1_B}", "INSTANCEZ_ENV_A_1_B", true},
		// literals — must not match
		{"https://api.stripe.com", "", false},
		{"${FOO}", "", false},              // missing INSTANCEZ_ENV_ prefix
		{"${INSTANCEZ_DSN}", "", false},    // INSTANCEZ_ not INSTANCEZ_ENV_
		{"INSTANCEZ_ENV_FOO", "", false},       // no ${} wrapper
		{"${INSTANCEZ_ENV_}", "", false},       // empty suffix
		{" ${INSTANCEZ_ENV_FOO}", "", false},   // leading space
		{"${INSTANCEZ_ENV_FOO} ", "", false},   // trailing space
	}
	for _, tc := range cases {
		ref, ok := asInstancezEnvRef(tc.in)
		if ok != tc.wantOK || ref != tc.wantRef {
			t.Errorf("asInstancezEnvRef(%q) = (%q, %v), want (%q, %v)", tc.in, ref, ok, tc.wantRef, tc.wantOK)
		}
	}
}

// TestNewFailsEarlyOnMissingEnvRef verifies that New returns an error when a
// function references a ${INSTANCEZ_ENV_*} key that is absent from EnvMap — and
// does so BEFORE spawning node (no node required to run this test).
func TestNewFailsEarlyOnMissingEnvRef(t *testing.T) {
	opts := Options{
		Dir: t.TempDir(),
		Functions: map[string]domain.CodeFunction{
			"pay": {
				Runtime: "node",
				File:    "pay.js",
				Env:     map[string]string{"STRIPE_KEY": "${INSTANCEZ_ENV_MISSING}"},
			},
		},
		EnvMap: map[string]string{
			// INSTANCEZ_ENV_MISSING is intentionally absent
		},
	}
	_, err := New(opts)
	if err == nil {
		t.Fatal("expected error for missing INSTANCEZ_ENV_ ref, got nil")
	}
	// Error message should name the function and the missing ref.
	errStr := err.Error()
	if !containsAll(errStr, "pay", "INSTANCEZ_ENV_MISSING") {
		t.Fatalf("error message %q did not mention function name and ref", errStr)
	}
}

// TestNewAcceptsLiteralEnvValues verifies that a literal env value (no ${…})
// does not trigger the fail-early check even when EnvMap is empty.
func TestNewAcceptsLiteralEnvValues(t *testing.T) {
	// No node available in unit tests, so we can only verify New does NOT
	// error out on the validation step. We expect it to proceed to spawning
	// node (which may fail) — that's fine; the important thing is the
	// validation itself passes.
	opts := Options{
		Dir: "/nonexistent-dir-that-will-fail-at-node-spawn",
		Functions: map[string]domain.CodeFunction{
			"svc": {
				Runtime: "node",
				File:    "svc.js",
				Env:     map[string]string{"BASE_URL": "https://api.example.com"},
			},
		},
		EnvMap: map[string]string{}, // empty — literals don't need EnvMap
	}
	_, err := New(opts)
	// The error (if any) must NOT be the fail-early validation error —
	// it may be a file-write or node-spawn error.
	if err != nil {
		if containsAll(err.Error(), "not in INSTANCEZ_ENV_ namespace") {
			t.Fatalf("literal env value triggered fail-early: %v", err)
		}
		// Any other error (shim write / node start) is expected in a unit
		// environment without a real functions dir or node binary.
	}
}

// containsAll reports whether s contains all of the given substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
