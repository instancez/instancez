//go:build integration

package app_test

import (
	"context"
	"errors"
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
	// The managed policy is dropped first, so the failure comes from recreating it, not from DROP FUNCTION.
	err := m.Apply(ctx, rpCfg("public.can_see(id)", nil))
	if err == nil || !strings.Contains(err.Error(), "public.can_see(bigint) does not exist") || strings.Contains(err.Error(), "depend on it") {
		t.Fatalf("want the recreate to fail on the missing function, got %v", err)
	}
	if !functionExists(t, db, "can_see") || !policyExists(t, db, "members", "members_select_0") {
		t.Fatal("rolled back: function and policy must still exist")
	}
}

func TestIntegration_RPCDropWithInertManagedPolicy(t *testing.T) {
	for name, next := range map[string]map[string]domain.Function{
		"drop":      nil,
		"signature": {"can_see": rpCanSee(domain.FuncArg{Name: "strict", Type: "boolean", Default: true})},
	} {
		t.Run(name, func(t *testing.T) {
			db := startPostgres(t)
			ctx := context.Background()
			m := app.NewMigrator(db).AllowDestructive(true)
			if err := m.Apply(ctx, rpCfg("public.can_see(id)", map[string]domain.Function{"can_see": rpCanSee()})); err != nil {
				t.Fatal(err)
			}
			off := false
			inert := rpCfg("public.can_see(id)", map[string]domain.Function{"can_see": rpCanSee()})
			members := inert.Tables["members"]
			members.RLSEnabled = &off
			inert.Tables["members"] = members
			if err := m.Apply(ctx, inert); err != nil {
				t.Fatalf("v2 rls off: %v", err)
			}
			if !policyExists(t, db, "members", "members_select_0") {
				t.Fatal("v2: the inert policy must stay in the DB for this case")
			}
			inert.RPC = next
			if err := m.Apply(ctx, inert); err != nil {
				t.Fatalf("v3: %v", err)
			}
			if policyExists(t, db, "members", "members_select_0") {
				t.Fatal("v3: inert managed policy must be dropped")
			}
		})
	}
}

func TestIntegration_RPCDropRefusesSameNamedPolicyOnOtherTable(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db).AllowDestructive(true)
	if err := m.Apply(ctx, rpCfg("public.can_see(id)", map[string]domain.Function{"can_see": rpCanSee()})); err != nil {
		t.Fatal(err)
	}
	if err := db.ExecDDL(ctx, `CREATE POLICY members_select_0 ON public.allow_list FOR SELECT USING (public.can_see(member_id))`); err != nil {
		t.Fatal(err)
	}
	// members_select_0 stays managed on members, so only the table tells the two apart.
	err := m.Apply(ctx, rpCfg("true", nil))
	if err == nil || !strings.Contains(err.Error(), "still used by policy members_select_0 on public.allow_list") {
		t.Fatalf("want a refusal naming allow_list, got %v", err)
	}
	if !functionExists(t, db, "can_see") || !policyExists(t, db, "allow_list", "members_select_0") {
		t.Fatal("failed migration must roll back")
	}
}

func rpStorage(using string) map[string]domain.Bucket {
	return map[string]domain.Bucket{"docs": {RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: using}}}}
}

func TestIntegration_StoragePolicyRPCSignatureChange(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db).AllowDestructive(true)
	cfg := rpCfg("", map[string]domain.Function{"can_see": rpCanSee()})
	cfg.Auth, cfg.Storage = &domain.Auth{}, rpStorage("public.can_see(1)")
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.RPC = map[string]domain.Function{"can_see": rpCanSee(domain.FuncArg{Name: "strict", Type: "boolean", Default: true})}
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("signature change: %v", err)
	}
	if pronargs(t, db) != "2" || !policyExists(t, db, "storage.objects", "storage_docs_select_0") {
		t.Fatal("storage policy must be recreated on the new signature")
	}
}

func TestIntegration_ProvisionIdempotentDeniesPoliciesCallingMissingRPC(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	v1 := rpCfg("", nil)
	members := v1.Tables["members"]
	members.Fields = append(members.Fields, domain.Field{Name: "note", Type: "text"})
	v1.Tables["members"] = members
	v1.Auth = &domain.Auth{}
	if err := m.Apply(ctx, v1); err != nil {
		t.Fatal(err)
	}
	pending := rpCfg("", map[string]domain.Function{"can_see": rpCanSee()})
	pending.Auth, pending.Storage = &domain.Auth{}, rpStorage("public.can_see(1)")
	if err := m.Apply(ctx, pending); !errors.Is(err, app.ErrDestructive) {
		t.Fatalf("want ErrDestructive, got %v", err)
	}
	if err := m.ProvisionIdempotent(ctx, pending); err != nil {
		t.Fatalf("provision: %v", err)
	}
	row, err := db.QueryRow(ctx, `SELECT c.relrowsecurity AS rls,
		(SELECT qual FROM pg_policies WHERE schemaname='storage' AND tablename='objects' AND policyname='storage_docs_select_0') AS qual,
		(SELECT count(*) FROM pg_policies WHERE schemaname='storage' AND tablename='objects') AS n
		FROM pg_class c WHERE c.oid = 'storage.objects'::regclass`)
	if err != nil {
		t.Fatal(err)
	}
	if row["rls"] != true || fmt.Sprint(row["n"]) != "1" || !strings.Contains(fmt.Sprint(row["qual"]), "false") {
		t.Fatalf("bucket must stay closed: %v", row)
	}
	if functionExists(t, db, "can_see") {
		t.Fatal("the rpc belongs to the blocked plan")
	}

	// Once the blocked plan is allowed, the real policy replaces the false one.
	if err := app.NewMigrator(db).AllowDestructive(true).Apply(ctx, pending); err != nil {
		t.Fatalf("unblocked apply: %v", err)
	}
	row, err = db.QueryRow(ctx, `SELECT qual, (SELECT count(*) FROM pg_policies WHERE schemaname='storage' AND tablename='objects') AS n
		FROM pg_policies WHERE schemaname='storage' AND tablename='objects' AND policyname='storage_docs_select_0'`)
	if err != nil {
		t.Fatal(err)
	}
	if qual := fmt.Sprint(row["qual"]); !strings.Contains(qual, "can_see") || fmt.Sprint(row["n"]) != "1" {
		t.Fatalf("real policy must replace the false one: %v", row)
	}
	if !functionExists(t, db, "can_see") {
		t.Fatal("the unblocked plan creates the rpc")
	}
}

func TestIntegration_RPCArgDefaultNull_BootsAndCallableWithoutArg(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db).AllowDestructive(true)
	fn := domain.Function{
		Language: "sql", Volatility: "stable", Security: "invoker",
		Args:    []domain.FuncArg{{Name: "limit_to", Type: "bigint", Default: domain.NullDefault}},
		Returns: domain.FuncReturn{Type: "text"},
		Body:    "SELECT coalesce(limit_to::text, 'none')",
	}
	cfg := &domain.Config{Version: 1, RPC: map[string]domain.Function{"pick": fn}}
	for i := 0; i < 2; i++ {
		if err := m.Apply(ctx, cfg); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	row, err := db.QueryRow(ctx, `SELECT public.pick() AS v`)
	if err != nil {
		t.Fatal(err)
	}
	if row["v"] != "none" {
		t.Fatalf("pick() = %v, want none", row["v"])
	}
}
