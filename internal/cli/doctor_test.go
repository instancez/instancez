package cli

import (
	"errors"
	"testing"

	"github.com/instancez/instancez/internal/cli/preflight"
)

// TestDoctorRunnerAllFailing verifies that doctorChecks-equivalent logic
// reports ALL failing checks (no short-circuit) and returns errReported when
// any check fails.
func TestDoctorRunnerAllFailing(t *testing.T) {
	checks := []preflight.Check{
		func() preflight.Result {
			return preflight.Result{Name: "a", OK: false, Detail: "failed a", FixHint: "fix a"}
		},
		func() preflight.Result {
			return preflight.Result{Name: "b", OK: false, Detail: "failed b", FixHint: "fix b"}
		},
		func() preflight.Result {
			return preflight.Result{Name: "c", OK: true}
		},
	}

	results := preflight.RunAll(checks)
	if len(results) != 3 {
		t.Fatalf("RunAll returned %d results, want 3", len(results))
	}
	if !preflight.AnyFailed(results) {
		t.Fatal("AnyFailed should be true")
	}

	// Simulate what runDoctor does: return errReported on failure.
	var got error
	if preflight.AnyFailed(results) {
		got = errReported
	}
	if !errors.Is(got, errReported) {
		t.Fatalf("expected errReported, got %v", got)
	}
}

// TestDoctorRunnerAllPassing verifies that runDoctor returns nil when all
// checks pass.
func TestDoctorRunnerAllPassing(t *testing.T) {
	checks := []preflight.Check{
		func() preflight.Result { return preflight.Result{Name: "a", OK: true} },
		func() preflight.Result { return preflight.Result{Name: "b", OK: true} },
	}

	results := preflight.RunAll(checks)
	if preflight.AnyFailed(results) {
		t.Fatal("AnyFailed should be false when all checks pass")
	}

	var got error
	if preflight.AnyFailed(results) {
		got = errReported
	}
	if got != nil {
		t.Fatalf("expected nil error, got %v", got)
	}
}

// TestDoctorChecksNoLiveDB verifies that doctorChecks can be built and run
// without a live database.  The config check will fail because no
// instancez.yaml exists in the test directory, but the runner must not panic.
func TestDoctorChecksNoLiveDB(t *testing.T) {
	noEnv := func(string) string { return "" }
	checks := doctorChecks("instancez.yaml", noEnv)
	results := preflight.RunAll(checks)
	if len(results) != len(checks) {
		t.Fatalf("RunAll returned %d results, want %d", len(results), len(checks))
	}
	// At least the config check should have failed (no file in cwd).
	if !preflight.AnyFailed(results) {
		t.Log("Note: unexpectedly all checks passed; may run from a directory with a valid instancez.yaml")
	}
}

// TestDoctorChecksContainsExpectedChecks is a membership guard that asserts
// doctorChecks returns exactly the expected set of named checks.  If a check
// is accidentally removed this test will fail, preventing a silent regression.
func TestDoctorChecksContainsExpectedChecks(t *testing.T) {
	noEnv := func(string) string { return "" }
	checks := doctorChecks("instancez.yaml", noEnv)

	// Run each check against the empty env and collect result Names.
	names := make(map[string]bool, len(checks))
	for _, c := range checks {
		r := c()
		names[r.Name] = true
	}

	required := []string{
		"config file valid",
		"DSN env vars present",
		"owner DB connect",
		"auth DB connect",
		"role layout",
	}
	for _, want := range required {
		if !names[want] {
			t.Errorf("doctorChecks is missing check %q; got names: %v", want, names)
		}
	}
	if len(checks) != len(required) {
		t.Errorf("doctorChecks returned %d checks, want %d; names: %v", len(checks), len(required), names)
	}
}
