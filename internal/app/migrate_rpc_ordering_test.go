package app

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// A policy's USING/WITH CHECK can call a user RPC (e.g. can_access_school), and
// CREATE POLICY resolves those function references at creation time. So every
// RPC must be emitted before any policy, and the RPC batch runs with
// check_function_bodies=false so an RPC that references a sibling RPC sorted
// later in the batch (or an as-yet-ungranted table) still creates. These tests
// pin both invariants for the two plan paths.

func firstIndex(t *testing.T, stmts []string, needle string) int {
	t.Helper()
	for i, s := range stmts {
		if strings.Contains(s, needle) {
			return i
		}
	}
	t.Fatalf("no statement contains %q", needle)
	return -1
}

func orderingConfig() *domain.Config {
	return &domain.Config{
		Tables: map[string]domain.Table{
			"docs": {
				Fields: []domain.Field{{Name: "id", Type: "uuid", PrimaryKey: true}, {Name: "school_id", Type: "uuid"}},
				RLS:    []domain.RLSPolicy{{Operations: []string{"select"}, Using: "can_access(school_id)"}},
			},
		},
		RPC: map[string]domain.Function{
			// Sorts AFTER can_access, so a naive alphabetical batch would create
			// can_access (which calls is_owner) before is_owner exists.
			"is_owner": {
				Language: "sql", Volatility: "stable", Security: "definer",
				Returns: domain.FuncReturn{Type: "boolean"},
				Args:    []domain.FuncArg{{Name: "p", Type: "uuid"}},
				Body:    "SELECT true;",
			},
			"can_access": {
				Language: "sql", Volatility: "stable", Security: "definer",
				Returns: domain.FuncReturn{Type: "boolean"},
				Args:    []domain.FuncArg{{Name: "p", Type: "uuid"}},
				Body:    "SELECT is_owner(p);",
			},
		},
	}
}

func testRoles() domain.Roles {
	return domain.Roles{
		Authenticator: "app_x_authenticator", Anon: "app_x_anon",
		Authenticated: "app_x_authenticated", Service: "app_x_service_role", Seed: "app_x_seed",
	}
}

func assertRPCsBeforePolicies(t *testing.T, stmts []string) {
	t.Helper()
	fnIdx := firstIndex(t, stmts, `CREATE OR REPLACE FUNCTION public."can_access"`)
	polIdx := firstIndex(t, stmts, "CREATE POLICY")
	if fnIdx >= polIdx {
		t.Fatalf("RPC must be created before policies: can_access at %d, first CREATE POLICY at %d", fnIdx, polIdx)
	}
	offIdx := firstIndex(t, stmts, "check_function_bodies = false")
	onIdx := firstIndex(t, stmts, "check_function_bodies = true")
	if !(offIdx < fnIdx && fnIdx < onIdx && onIdx < polIdx) {
		t.Fatalf("RPC batch must be wrapped in a check_function_bodies=false window before policies: off=%d fn=%d on=%d policy=%d", offIdx, fnIdx, onIdx, polIdx)
	}
}

func TestPlanFromScratch_RPCsBeforePolicies(t *testing.T) {
	assertRPCsBeforePolicies(t, planFromScratchStatements(orderingConfig(), testRoles()))
}

func TestPlanUpdate_RPCsBeforePolicies(t *testing.T) {
	// nil oldCfg → every table/RPC/policy is an addition, same ordering risk.
	assertRPCsBeforePolicies(t, planUpdateStatements(nil, orderingConfig(), testRoles()))
}
