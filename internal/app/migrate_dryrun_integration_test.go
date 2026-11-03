//go:build integration

package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

func dryRunCfg(using string) *domain.Config {
	return &domain.Config{Version: 1, Auth: &domain.Auth{}, Tables: map[string]domain.Table{"todos": {
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "user_id", Type: "uuid"},
		},
		RLS: []domain.RLSPolicy{{Operations: []string{"select"}, Using: using}},
	}}}
}

func sqlRPC(body string) domain.Function {
	return domain.Function{Language: "sql", Volatility: "stable", Returns: domain.FuncReturn{Type: "text"}, Body: body}
}

func regclassNull(t *testing.T, db *postgres.DB, name string) bool {
	t.Helper()
	row, err := db.QueryRow(context.Background(), "SELECT to_regclass($1) IS NULL AS n", name)
	if err != nil {
		t.Fatal(err)
	}
	return row["n"].(bool)
}

func TestDryRun_MissingColumnInRLS(t *testing.T) {
	db := startPostgres(t)
	err := app.NewMigrator(db).DryRun(context.Background(), dryRunCfg("nonexist = auth.uid()"))
	if err == nil || !strings.Contains(err.Error(), `column "nonexist" does not exist`) {
		t.Fatalf("got %v", err)
	}
}

func TestDryRun_RPCBodyMissingTable(t *testing.T) {
	db := startPostgres(t)
	cfg := dryRunCfg("user_id = auth.uid()")
	cfg.RPC = map[string]domain.Function{"bad": sqlRPC("select * from nope")}
	err := app.NewMigrator(db).DryRun(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), `relation "nope" does not exist`) {
		t.Fatalf("got %v", err)
	}
}

func TestDryRun_ValidConfigOK(t *testing.T) {
	db := startPostgres(t)
	cfg := dryRunCfg("user_id = auth.uid()")
	cfg.RPC = map[string]domain.Function{"ok": sqlRPC("select 'x'::text")}
	if err := app.NewMigrator(db).DryRun(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDryRun_LeavesNoTrace(t *testing.T) {
	db := startPostgres(t)
	m := app.NewMigrator(db)
	ctx := context.Background()
	if err := m.DryRun(ctx, dryRunCfg("user_id = auth.uid()")); err != nil {
		t.Fatal(err)
	}
	if err := m.DryRun(ctx, dryRunCfg("nonexist = auth.uid()")); err == nil {
		t.Fatal("want failure")
	}
	if !regclassNull(t, db, "public.todos") {
		t.Fatal("todos table leaked")
	}
	if !regclassNull(t, db, "_instancez_migrations") {
		t.Fatal("migrations table leaked")
	}
}

func TestDryRun_DoesNotRecordOverAppliedState(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	if err := m.Apply(ctx, dryRunCfg("user_id = auth.uid()")); err != nil {
		t.Fatal(err)
	}
	before := countRows(t, db, "SELECT COUNT(*) AS n FROM _instancez_migrations")
	if err := m.DryRun(ctx, dryRunCfg("true")); err != nil {
		t.Fatal(err)
	}
	if after := countRows(t, db, "SELECT COUNT(*) AS n FROM _instancez_migrations"); after != before {
		t.Fatalf("migration rows %d -> %d", before, after)
	}
}

func TestDryRun_DestructiveAllowed(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	v1 := dryRunCfg("user_id = auth.uid()")
	tb1 := v1.Tables["todos"]
	tb1.Fields = append(tb1.Fields, domain.Field{Name: "note", Type: "text"})
	v1.Tables["todos"] = tb1
	if err := m.Apply(ctx, v1); err != nil {
		t.Fatal(err)
	}
	dropped := dryRunCfg("user_id = auth.uid()")
	tb := dropped.Tables["todos"]
	dropped.Tables["todos"] = tb
	if err := m.DryRun(ctx, dropped); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	row, err := db.QueryRow(ctx, "SELECT COUNT(*) AS n FROM information_schema.columns WHERE table_name='todos' AND column_name='note'")
	if err != nil || row["n"].(int64) != 1 {
		t.Fatalf("column dropped by dry run: %v %v", row, err)
	}
}

func TestDryRun_InSyncNoop(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	cfg := dryRunCfg("user_id = auth.uid()")
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := m.DryRun(ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDryRun_MissingRoleHint(t *testing.T) {
	url := dbboot.StartRawContainer(t)
	owner, err := postgres.NewOwner(context.Background(), url, domain.PoolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	err = app.NewMigrator(owner.Database).DryRun(context.Background(), dryRunCfg("true"))
	if err == nil || !strings.Contains(err.Error(), "hint:") {
		t.Fatalf("got %v", err)
	}
}

func TestDryRun_LockTimeoutDoesNotHang(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	if err := m.Apply(ctx, dryRunCfg("true")); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "LOCK TABLE public.todos IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	next := dryRunCfg("true")
	tb := next.Tables["todos"]
	tb.Fields = append(tb.Fields, domain.Field{Name: "extra", Type: "text"})
	next.Tables["todos"] = tb
	err = m.DryRun(ctx, next)
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("want lock timeout, got %v", err)
	}
}

func TestDryRun_InSyncStillChecksRPCBodies(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	cfg := dryRunCfg("user_id = auth.uid()")
	cfg.RPC = map[string]domain.Function{"bad": sqlRPC("select * from nope")}
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("apply creates bodies unchecked: %v", err)
	}
	err := m.DryRun(ctx, cfg)
	if err == nil || !strings.Contains(err.Error(), `relation "nope" does not exist`) {
		t.Fatalf("in-sync dry run must still check bodies, got %v", err)
	}
}

func TestDryRun_AdvisoryLockContentionTimesOut(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	holder, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", domain.MigrationLockKey); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = app.NewMigrator(db).DryRun(ctx, dryRunCfg("true"))
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("want lock timeout, got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("waited %s, lock_timeout should bound it", time.Since(start))
	}
}

func TestDryRun_CancelledContextLeavesNoTrace(t *testing.T) {
	db := startPostgres(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.NewMigrator(db).DryRun(ctx, dryRunCfg("true")); err == nil {
		t.Fatal("want error from a cancelled context")
	}
	if !regclassNull(t, db, "public.todos") || !regclassNull(t, db, "_instancez_migrations") {
		t.Fatal("cancelled dry run left objects behind")
	}
}

func TestDryRun_NoCreatePrivilegeHint(t *testing.T) {
	url := dbboot.StartRawContainer(t)
	super, err := postgres.NewOwner(context.Background(), url, domain.PoolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = super.Close() })
	ctx := context.Background()
	for _, q := range []string{
		"CREATE ROLE dryrun_low LOGIN PASSWORD 'low'",
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		"GRANT CONNECT ON DATABASE instancez_test TO dryrun_low",
	} {
		if _, err := super.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	low, err := postgres.NewOwner(ctx, strings.Replace(url, "postgres:postgres@", "dryrun_low:low@", 1), domain.PoolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = low.Close() })
	err = app.NewMigrator(low.Database).DryRun(ctx, dryRunCfg("true"))
	if err == nil || !strings.Contains(err.Error(), "owner-class DSN") || !strings.Contains(err.Error(), "create migrations table") {
		t.Fatalf("got %v", err)
	}
}
