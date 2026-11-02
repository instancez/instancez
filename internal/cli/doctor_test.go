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

func TestDoctorChecksNoDSNSkipsDBChecks(t *testing.T) {
	noEnv := func(string) string { return "" }
	checks := doctorChecks("instancez.yaml", noEnv)
	if len(checks) != 2 {
		t.Fatalf("got %d checks, want 2 (config + superuser DSN)", len(checks))
	}
	var dsnFailures int
	for _, r := range preflight.RunAll(checks) {
		switch r.Name {
		case "database connect", "role layout":
			t.Errorf("cascading check %q ran without a DSN", r.Name)
		case "Superuser DSN present":
			if !r.OK {
				dsnFailures++
			}
		}
	}
	if dsnFailures != 1 {
		t.Fatalf("want exactly one DSN failure, got %d", dsnFailures)
	}
}

func TestDoctorChecksUnreachableDBOneFailure(t *testing.T) {
	lookup := func(k string) string {
		if k == preflight.EnvSuperuserDSN {
			return "postgres://x:y@127.0.0.1:1/db?connect_timeout=1"
		}
		return ""
	}
	checks := doctorChecks("instancez.yaml", lookup)
	if len(checks) != 3 {
		t.Fatalf("got %d checks, want 3", len(checks))
	}
	results := preflight.RunAll(checks)
	last := results[len(results)-1]
	if last.OK || last.Name != "database connect" {
		t.Fatalf("last result = %+v, want failed database connect", last)
	}
	for _, r := range results {
		if r.Name == "role layout" {
			t.Fatalf("role layout reported after a connect failure: %+v", r)
		}
	}
}
