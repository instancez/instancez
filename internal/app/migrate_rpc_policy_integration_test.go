//go:build integration

package app_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

func rpMembers(using string) domain.Table {
	on := true
	t := domain.Table{Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}, RLSEnabled: &on}
	if using != "" {
		t.RLS = []domain.RLSPolicy{{Operations: []string{"select"}, Using: using}}
	}
	return t
}

func rpCanSee(extra ...domain.FuncArg) domain.Function {
	return domain.Function{
		Language: "sql", Volatility: "stable", Security: "invoker",
		Args:    append([]domain.FuncArg{{Name: "mid", Type: "bigint"}}, extra...),
		Returns: domain.FuncReturn{Type: "boolean"},
		Body:    "SELECT EXISTS (SELECT 1 FROM public.allow_list a WHERE a.member_id = mid)",
	}
}

func rpCfg(using string, rpc map[string]domain.Function) *domain.Config {
	return &domain.Config{Version: 1, RPC: rpc, Tables: map[string]domain.Table{
		"allow_list": {Fields: []domain.Field{{Name: "member_id", Type: "bigint"}}},
		"members":    rpMembers(using),
	}}
}

func pronargs(t *testing.T, db interface {
	QueryRow(context.Context, string, ...any) (map[string]any, error)
}) string {
	t.Helper()
	row, err := db.QueryRow(context.Background(), `SELECT pronargs FROM pg_proc WHERE proname = 'can_see'`)
	if err != nil {
		t.Fatalf("pronargs: %v", err)
	}
	return fmt.Sprint(row["pronargs"])
}

func TestIntegration_PolicyCallingRPC_FreshDB(t *testing.T) {
	db := startPostgres(t)
	m := app.NewMigrator(db).AllowDestructive(true)
	if err := m.Apply(context.Background(), rpCfg("public.can_see(id)", map[string]domain.Function{"can_see": rpCanSee()})); err != nil {
		t.Fatalf("fresh apply: %v", err)
	}
	if !policyExists(t, db, "members", "members_select_0") || !functionExists(t, db, "can_see") {
		t.Fatal("policy and function must both exist")
	}
}

func TestIntegration_PolicyCallingRPC_Lifecycle(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db).AllowDestructive(true)
	step := func(name string, cfg *domain.Config) error {
		t.Helper()
		return m.Apply(ctx, cfg)
	}
	if err := step("v1 tables only", rpCfg("", nil)); err != nil {
		t.Fatal(err)
	}
	if err := step("v2 add rpc + policy on existing db", rpCfg("public.can_see(id)", map[string]domain.Function{"can_see": rpCanSee()})); err != nil {
		t.Fatalf("v2: %v", err)
	}
	if !policyExists(t, db, "members", "members_select_0") {
		t.Fatal("v2 policy missing")
	}
	// Signature change while the policy still calls it: the policy is dropped and recreated, no CASCADE.
	sig := rpCanSee(domain.FuncArg{Name: "strict", Type: "boolean", Default: true})
	if err := step("v3 signature change", rpCfg("public.can_see(id)", map[string]domain.Function{"can_see": sig})); err != nil {
		t.Fatalf("v3: %v", err)
	}
	if got := pronargs(t, db); got != "2" || !policyExists(t, db, "members", "members_select_0") {
		t.Fatalf("v3: pronargs=%s policy=%v", got, policyExists(t, db, "members", "members_select_0"))
	}
	// Policy edited away from the rpc and rpc removed in one change.
	if err := step("v4 edit policy + drop rpc", rpCfg("true", nil)); err != nil {
		t.Fatalf("v4: %v", err)
	}
	if functionExists(t, db, "can_see") || !policyExists(t, db, "members", "members_select_0") {
		t.Fatal("v4: rpc must be gone, edited policy kept")
	}
	if err := step("v5 re-add both", rpCfg("public.can_see(id)", map[string]domain.Function{"can_see": rpCanSee()})); err != nil {
		t.Fatalf("v5: %v", err)
	}
	if err := step("v6 remove both", rpCfg("", nil)); err != nil {
		t.Fatalf("v6 remove both: %v", err)
	}
	if functionExists(t, db, "can_see") || policyExists(t, db, "members", "members_select_0") {
		t.Fatal("v6: both must be gone")
	}
}

func TestIntegration_RPCDropRefusesUnmanagedPolicy(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db).AllowDestructive(true)
	if err := m.Apply(ctx, rpCfg("public.can_see(id)", map[string]domain.Function{"can_see": rpCanSee()})); err != nil {
		t.Fatal(err)
	}
	if err := db.ExecDDL(ctx, `CREATE POLICY manual_peek ON public.members FOR SELECT USING (public.can_see(id))`); err != nil {
		t.Fatal(err)
	}
	err := m.Apply(ctx, rpCfg("", nil))
	if err == nil || !strings.Contains(err.Error(), "still used by policy manual_peek") {
		t.Fatalf("want a clear refusal, got %v", err)
	}
	if !functionExists(t, db, "can_see") || !policyExists(t, db, "members", "manual_peek") {
		t.Fatal("failed migration must roll back: function and manual policy intact")
	}
}

func TestIntegration_RPCDropWithManagedPolicyStillCalling(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db).AllowDestructive(true)
	if err := m.Apply(ctx, rpCfg("public.can_see(id)", map[string]domain.Function{"can_see": rpCanSee()})); err != nil {
		t.Fatal(err)
	}
	err := m.Apply(ctx, rpCfg("public.can_see(id)", nil))
	if err == nil || !strings.Contains(err.Error(), "can_see") {
		t.Fatalf("want an error naming can_see, got %v", err)
	}
	if !functionExists(t, db, "can_see") {
		t.Fatal("rolled back: function must still exist")
	}
}
