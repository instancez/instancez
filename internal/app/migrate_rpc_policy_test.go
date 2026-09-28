package app

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func rpcPolicyConfig() *domain.Config {
	on := true
	return &domain.Config{
		Version: 1,
		Tables: map[string]domain.Table{
			"allow_list": {Fields: []domain.Field{{Name: "member_id", Type: "bigint"}}},
			"members": {
				Fields:     []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}},
				RLSEnabled: &on,
				RLS:        []domain.RLSPolicy{{Operations: []string{"select"}, Using: "public.can_see(id)"}},
			},
		},
		Storage: map[string]domain.Bucket{"docs": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: "public.can_see(1)"}}}},
		RPC: map[string]domain.Function{"can_see": {
			Language: "sql", Volatility: "stable", Security: "invoker",
			Args:    []domain.FuncArg{{Name: "mid", Type: "bigint"}},
			Returns: domain.FuncReturn{Type: "boolean"},
			Body:    "SELECT EXISTS (SELECT 1 FROM public.allow_list a WHERE a.member_id = mid)",
		}},
	}
}

func stmtIndex(stmts []string, prefix string) int {
	for i, s := range stmts {
		if strings.HasPrefix(s, prefix) {
			return i
		}
	}
	return -1
}

func TestPlans_CreateRPCsBeforePolicies(t *testing.T) {
	cfg := rpcPolicyConfig()
	old := &domain.Config{Version: 1, Tables: map[string]domain.Table{"allow_list": cfg.Tables["allow_list"]}}
	for name, stmts := range map[string][]string{
		"scratch": planFromScratchStatements(cfg, domain.DefaultRoles()),
		"update":  planUpdateStatements(old, cfg, domain.DefaultRoles()),
	} {
		fn := stmtIndex(stmts, `CREATE OR REPLACE FUNCTION public."can_see"`)
		tbl := stmtIndex(stmts, "CREATE POLICY members_select_0")
		sto := stmtIndex(stmts, "CREATE POLICY storage_docs_select_0")
		if fn < 0 || tbl < 0 || sto < 0 || fn > tbl || fn > sto {
			t.Fatalf("%s: function at %d, table policy at %d, storage policy at %d:\n%s", name, fn, tbl, sto, strings.Join(stmts, "\n"))
		}
		if helpers := stmtIndex(stmts, "CREATE OR REPLACE FUNCTION auth."); helpers >= 0 && helpers > fn {
			t.Fatalf("%s: auth helpers must precede user RPCs", name)
		}
	}
}

func TestDiffRemovedRPC_DropsDependentManagedPoliciesFirst(t *testing.T) {
	old := rpcPolicyConfig()
	updated := rpcPolicyConfig()
	delete(updated.RPC, "can_see")
	updated.Storage = nil
	stmts := diffRemovedRPCFunctions(old, updated)
	if len(stmts) != 2 {
		t.Fatalf("want DO block + DROP FUNCTION, got %v", stmts)
	}
	do, drop := stmts[0], stmts[1]
	for _, want := range []string{
		`to_regprocedure('public."can_see"(bigint)')`,
		`'{members_select_0}'::name[]`,
		"SELECT DISTINCT p.polname",
		"RAISE EXCEPTION 'rpc can_see is still used by policy % on %'",
	} {
		if !strings.Contains(do, want) {
			t.Errorf("DO block missing %q:\n%s", want, do)
		}
	}
	if drop != `DROP FUNCTION IF EXISTS public."can_see"(bigint);` || strings.Contains(strings.Join(stmts, " "), "CASCADE") {
		t.Fatalf("drop = %q", drop)
	}
}

func TestDiffRemovedRPC_UnchangedFunctionEmitsNothing(t *testing.T) {
	if got := diffRemovedRPCFunctions(rpcPolicyConfig(), rpcPolicyConfig()); len(got) != 0 {
		t.Fatalf("unchanged rpc must emit nothing, got %v", got)
	}
}

func TestManagedPolicyNames(t *testing.T) {
	cfg := rpcPolicyConfig()
	cfg.Tables["open"] = domain.Table{Fields: []domain.Field{{Name: "id", Type: "int"}}}
	got := strings.Join(managedPolicyNames(cfg), ",")
	if got != "members_select_0,storage_docs_select_0" {
		t.Fatalf("got %q", got)
	}
	if n := managedPolicyNames(&domain.Config{}); len(n) != 0 {
		t.Fatalf("empty config: %v", n)
	}
}

func TestDropPoliciesUsing_TruncatesManagedNames(t *testing.T) {
	long := strings.Repeat("t", 60) + "_select_0"
	sql := dropPoliciesUsing("f", domain.Function{}, []string{long})
	if !strings.Contains(sql, "'{"+long[:63]+"}'::name[]") {
		t.Fatalf("managed name not truncated to 63 bytes:\n%s", sql)
	}
	if !strings.Contains(dropPoliciesUsing("f", domain.Function{}, nil), "'{}'::name[]") {
		t.Fatal("no managed policies must render an empty array")
	}
}
