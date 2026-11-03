//go:build integration

package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
	"github.com/jackc/pgx/v5"
)

const resetYAML = `project:
  name: t
auth: {}
tables:
  todos:
    fields:
      - {name: id, type: uuid, primary_key: true}
  notes:
    schema: app
    fields:
      - {name: id, type: uuid, primary_key: true}
storage:
  files: {public: true}
`

func provisionAndMigrate(t *testing.T, ctx context.Context) {
	t.Helper()
	cfg, _, err := config.ParseBytesLocal([]byte(resetYAML), "instancez.yaml")
	if err != nil {
		t.Fatal(err)
	}
	owner, auth, roles, err := dbConnections(ctx, domain.PoolConfig{Max: 2}, "")
	if err != nil {
		t.Fatalf("dbConnections: %v", err)
	}
	defer owner.Close()
	defer auth.Close()
	if err := app.NewMigrator(owner.Database, roles).Apply(ctx, cfg); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func resetIntegrationSetup(t *testing.T) (string, *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	dsn := dbboot.StartRawContainer(t)
	if _, err := localDSNHosts(dsn); err != nil {
		t.Skipf("container is not local: %v", err)
	}
	t.Chdir(t.TempDir())
	if err := os.WriteFile("instancez.yaml", []byte(resetYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INSTANCEZ_DATABASE_URL", dsn)
	t.Setenv("INSTANCEZ_OWNER_DATABASE_URL", "")
	t.Setenv("INSTANCEZ_AUTH_DATABASE_URL", "")
	provisionAndMigrate(t, ctx)

	su, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = su.Close(context.Background()) })
	for _, q := range []string{
		"INSERT INTO public.todos (id) VALUES (gen_random_uuid())",
		"CREATE SCHEMA keepme", "CREATE TABLE keepme.t (id int)", "INSERT INTO keepme.t VALUES (1)",
		"CREATE VIEW keepme.v AS SELECT id FROM public.todos",
		"CREATE TYPE public.mood AS ENUM ('a')", "CREATE TABLE keepme.moods (m public.mood)", "CREATE TYPE keepme.bundle AS (m public.mood)",
	} {
		if _, err := su.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return dsn, su
}

func count(t *testing.T, c *pgx.Conn, q string) int {
	t.Helper()
	var n int
	if err := c.QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestDBResetIntegration(t *testing.T) {
	ctx := context.Background()
	_, su := resetIntegrationSetup(t)

	var prompt string
	t.Cleanup(swapPromptConfirm(func(p string) bool { prompt = p; return true }))
	var out bytes.Buffer
	err := runDBReset(ctx, dbResetOpts{configPath: "instancez.yaml", getenv: os.Getenv,
		tty: func() bool { return true }, out: &out})
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !strings.Contains(prompt, "will also be dropped") || !strings.Contains(prompt, "view keepme.v") {
		t.Errorf("prompt does not list the dependent view: %q", prompt)
	}
	if !strings.Contains(prompt, "column keepme.moods.m") {
		t.Errorf("prompt does not list the column that uses a dropped enum: %q", prompt)
	}
	if !strings.Contains(prompt, "column keepme.bundle.m") {
		t.Errorf("prompt does not list the composite-type attribute: %q", prompt)
	}
	if n := count(t, su, "SELECT count(*) FROM pg_views WHERE viewname = 'v'"); n != 0 {
		t.Error("dependent view survived CASCADE; the prompt would be lying")
	}
	if n := count(t, su, "SELECT count(*) FROM pg_namespace WHERE nspname IN ('auth','storage','app')"); n != 0 {
		t.Errorf("managed schemas left: %d", n)
	}
	if n := count(t, su, "SELECT count(*) FROM pg_namespace WHERE nspname = 'public'"); n != 1 {
		t.Errorf("public schema count = %d, want 1", n)
	}
	if n := count(t, su, "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'"); n != 0 {
		t.Errorf("public tables = %d, want 0 (incl. _instancez_migrations)", n)
	}
	if n := count(t, su, "SELECT count(*) FROM keepme.t"); n != 1 {
		t.Errorf("schema outside the chosen set was touched: keepme.t rows = %d", n)
	}

	// Next inz dev: bootstrap + migrate must work and provision auth/storage again.
	provisionAndMigrate(t, ctx)
	if n := count(t, su, "SELECT count(*) FROM public.todos"); n != 0 {
		t.Errorf("todos rows = %d, want 0", n)
	}
	if n := count(t, su, "SELECT count(*) FROM pg_namespace WHERE nspname IN ('auth','storage','app')"); n != 3 {
		t.Errorf("auth/storage/app schemas after re-migrate = %d, want 3", n)
	}
	if n := count(t, su, "SELECT count(*) FROM information_schema.tables WHERE table_schema='auth' AND table_name='users'"); n != 1 {
		t.Error("auth.users not recreated")
	}
}

func TestDBResetLockTimeoutIntegration(t *testing.T) {
	ctx := context.Background()
	dsn, su := resetIntegrationSetup(t)

	holder, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close(ctx) }()
	if _, err := holder.Exec(ctx, "BEGIN; LOCK TABLE public.todos IN ACCESS SHARE MODE"); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err = runDBReset(ctx, dbResetOpts{configPath: "instancez.yaml", yes: true, getenv: os.Getenv,
		tty: func() bool { return false }, out: &bytes.Buffer{}})
	if time.Since(start) > 15*time.Second {
		t.Errorf("reset took %v", time.Since(start))
	}
	if err == nil || !strings.Contains(err.Error(), "inz dev running") {
		t.Fatalf("err = %v", err)
	}
	if n := count(t, su, "SELECT count(*) FROM pg_tables WHERE tablename = 'todos'"); n != 1 {
		t.Error("todos dropped despite failed reset")
	}
}

func TestDBResetScopedPairIntegration(t *testing.T) {
	ctx := context.Background()
	dsn, su := resetIntegrationSetup(t)

	ownerDSN, authDSN, err := bootstrapDB(ctx, dsn, rolesFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("INSTANCEZ_OWNER_DATABASE_URL", ownerDSN)
	t.Setenv("INSTANCEZ_AUTH_DATABASE_URL", authDSN)

	err = runDBReset(ctx, dbResetOpts{configPath: "instancez.yaml", yes: true, getenv: os.Getenv,
		tty: func() bool { return false }, out: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("pair reset: %v", err)
	}
	if n := count(t, su, "SELECT count(*) FROM pg_namespace WHERE nspname IN ('auth','storage','app')"); n != 0 {
		t.Errorf("managed schemas left: %d", n)
	}
	provisionAndMigrate(t, ctx)
	if n := count(t, su, "SELECT count(*) FROM public.todos"); n != 0 {
		t.Errorf("todos rows = %d, want 0", n)
	}
}
