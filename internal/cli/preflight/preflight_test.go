package preflight_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/cli/preflight"
)

// ---------------------------------------------------------------------------
// DSNPresentCheck
// ---------------------------------------------------------------------------

func TestDSNPresentCheck(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantOK  bool
		wantErr string // substring expected in Detail when !wantOK
	}{
		{
			name:   "both present",
			env:    map[string]string{preflight.EnvOwnerDSN: "postgres://owner", preflight.EnvAuthDSN: "postgres://auth"},
			wantOK: true,
		},
		{
			name:    "owner missing",
			env:     map[string]string{preflight.EnvAuthDSN: "postgres://auth"},
			wantOK:  false,
			wantErr: preflight.EnvOwnerDSN,
		},
		{
			name:    "auth missing",
			env:     map[string]string{preflight.EnvOwnerDSN: "postgres://owner"},
			wantOK:  false,
			wantErr: preflight.EnvAuthDSN,
		},
		{
			name:    "both missing",
			env:     map[string]string{},
			wantOK:  false,
			wantErr: "not set",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(k string) string { return tc.env[k] }
			r := preflight.DSNPresentCheck(lookup)()
			if r.OK != tc.wantOK {
				t.Fatalf("OK = %v, want %v (detail: %s)", r.OK, tc.wantOK, r.Detail)
			}
			if !tc.wantOK {
				if !strings.Contains(r.Detail, tc.wantErr) {
					t.Errorf("Detail %q does not contain %q", r.Detail, tc.wantErr)
				}
				if r.FixHint == "" {
					t.Errorf("FixHint must be non-empty on failure")
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RoleLayoutCheck (via fake RoleReporter)
// ---------------------------------------------------------------------------

type fakeRoleReporter struct {
	roles  map[string]bool
	grants map[string]bool
	err    error
	// grantsErr, if non-nil, is returned by AuthenticatorGrants instead of err.
	grantsErr error
}

func (f *fakeRoleReporter) ExistingRoles() (map[string]bool, error) {
	return f.roles, f.err
}

func (f *fakeRoleReporter) AuthenticatorGrants() (map[string]bool, error) {
	if f.grantsErr != nil {
		return nil, f.grantsErr
	}
	return f.grants, f.err
}

func allExpectedRoles() map[string]bool {
	return map[string]bool{
		"ultrabase_owner": true,
		"authenticator":   true,
		"anon":            true,
		"authenticated":   true,
		"service_role":    true,
	}
}

func allExpectedGrants() map[string]bool {
	return map[string]bool{
		"anon":          true,
		"authenticated": true,
		"service_role":  true,
	}
}

func TestRoleLayoutCheck_AllPresent(t *testing.T) {
	reporter := &fakeRoleReporter{roles: allExpectedRoles(), grants: allExpectedGrants()}
	r := preflight.RoleLayoutCheck(reporter)()
	if !r.OK {
		t.Fatalf("expected OK, got failure: %s", r.Detail)
	}
}

func TestRoleLayoutCheck_MissingRole(t *testing.T) {
	roles := allExpectedRoles()
	delete(roles, "authenticator")
	reporter := &fakeRoleReporter{roles: roles, grants: allExpectedGrants()}
	r := preflight.RoleLayoutCheck(reporter)()
	if r.OK {
		t.Fatal("expected failure for missing role, got OK")
	}
	if !strings.Contains(r.Detail, "authenticator") {
		t.Errorf("Detail %q should name the missing role", r.Detail)
	}
	if r.FixHint == "" {
		t.Errorf("FixHint must be non-empty on failure")
	}
}

func TestRoleLayoutCheck_AuthenticatorNotGranted(t *testing.T) {
	grants := allExpectedGrants()
	delete(grants, "anon")
	reporter := &fakeRoleReporter{roles: allExpectedRoles(), grants: grants}
	r := preflight.RoleLayoutCheck(reporter)()
	if r.OK {
		t.Fatal("expected failure when authenticator is missing a grant, got OK")
	}
	if !strings.Contains(r.Detail, "anon") {
		t.Errorf("Detail %q should name the ungranted role", r.Detail)
	}
	if r.FixHint == "" {
		t.Errorf("FixHint must be non-empty on failure")
	}
}

func TestRoleLayoutCheck_ReporterError(t *testing.T) {
	reporter := &fakeRoleReporter{err: errors.New("connection refused")}
	r := preflight.RoleLayoutCheck(reporter)()
	if r.OK {
		t.Fatal("expected failure when reporter errors, got OK")
	}
	if r.FixHint == "" {
		t.Errorf("FixHint must be non-empty on failure")
	}
}

func TestRoleLayoutCheck_GrantsQueryError(t *testing.T) {
	reporter := &fakeRoleReporter{
		roles:     allExpectedRoles(),
		grantsErr: errors.New("permission denied"),
	}
	r := preflight.RoleLayoutCheck(reporter)()
	if r.OK {
		t.Fatal("expected failure when grants query errors, got OK")
	}
	if r.FixHint == "" {
		t.Errorf("FixHint must be non-empty on failure")
	}
}

// ---------------------------------------------------------------------------
// RunAll — must NOT short-circuit
// ---------------------------------------------------------------------------

func TestRunAll_NoShortCircuit(t *testing.T) {
	called := make([]bool, 3)
	checks := []preflight.Check{
		func() preflight.Result { called[0] = true; return preflight.Result{Name: "a", OK: false, FixHint: "fix a"} },
		func() preflight.Result { called[1] = true; return preflight.Result{Name: "b", OK: false, FixHint: "fix b"} },
		func() preflight.Result { called[2] = true; return preflight.Result{Name: "c", OK: true} },
	}
	results := preflight.RunAll(checks)
	if len(results) != 3 {
		t.Fatalf("RunAll returned %d results, want 3", len(results))
	}
	for i, c := range called {
		if !c {
			t.Errorf("check[%d] was never called — RunAll short-circuited", i)
		}
	}
	if preflight.AnyFailed(results) == false {
		t.Error("AnyFailed should return true when failures present")
	}
}

// ---------------------------------------------------------------------------
// RunUntilFail
// ---------------------------------------------------------------------------

func TestRunUntilFail_StopsAtFirstFailure(t *testing.T) {
	secondCalled := false
	checks := []preflight.Check{
		func() preflight.Result { return preflight.Result{Name: "a", OK: false, FixHint: "fix a"} },
		func() preflight.Result { secondCalled = true; return preflight.Result{Name: "b", OK: true} },
	}
	r, failed := preflight.RunUntilFail(checks)
	if !failed {
		t.Fatal("expected failed=true")
	}
	if r.Name != "a" {
		t.Errorf("expected first failure name 'a', got %q", r.Name)
	}
	if secondCalled {
		t.Error("second check should not have been called")
	}
}

func TestRunUntilFail_AllPass(t *testing.T) {
	checks := []preflight.Check{
		func() preflight.Result { return preflight.Result{Name: "a", OK: true} },
		func() preflight.Result { return preflight.Result{Name: "b", OK: true} },
	}
	_, failed := preflight.RunUntilFail(checks)
	if failed {
		t.Fatal("expected failed=false when all pass")
	}
}

// ---------------------------------------------------------------------------
// AnyFailed
// ---------------------------------------------------------------------------

func TestAnyFailed(t *testing.T) {
	allOK := []preflight.Result{{OK: true}, {OK: true}}
	if preflight.AnyFailed(allOK) {
		t.Error("AnyFailed should be false for all-OK results")
	}
	withFail := []preflight.Result{{OK: true}, {OK: false, FixHint: "fix"}}
	if !preflight.AnyFailed(withFail) {
		t.Error("AnyFailed should be true when any result fails")
	}
}

// ---------------------------------------------------------------------------
// Render output smoke test
// ---------------------------------------------------------------------------

func TestRender_Output(t *testing.T) {
	results := []preflight.Result{
		{Name: "check a", OK: true},
		{Name: "check b", OK: false, Detail: "something wrong", FixHint: "do this to fix"},
	}
	var sb strings.Builder
	preflight.Render(&sb, results)
	out := sb.String()
	if !strings.Contains(out, "✓") {
		t.Error("expected tick for passing check")
	}
	if !strings.Contains(out, "✗") {
		t.Error("expected cross for failing check")
	}
	if !strings.Contains(out, "do this to fix") {
		t.Error("expected FixHint in output")
	}
}
