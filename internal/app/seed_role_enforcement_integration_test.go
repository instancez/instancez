//go:build integration

package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

// TestSeedRoleEnforcement is the end-to-end proof that run_sql's seed role is
// confined by Postgres, not by any application-layer string check. It mirrors a
// production app database as closely as the test harness allows:
//
//   - dbboot provisions the owner + authenticator + anon/authenticated/
//     service_role layout exactly like the deployer's CreateDatabaseAndRoles.
//   - The test then creates the per-app seed role the same way the deployer does
//     (NOLOGIN BYPASSRLS, granted to the authenticator), the one piece dbboot
//     does not yet know about.
//   - The REAL migrator (app.NewMigrator with Seed set) builds the schema and
//     applies the actual generateSchemaGrants / generateExistingObjectGrants
//     output — no hand-written grant SQL.
//   - Statements run as the seed role through a raw authenticator connection
//     with SET LOCAL ROLE, which is exactly what data's execSQL does.
//
// Every scenario the seed role is supposed to allow or deny is asserted as a
// subtest, so a regression in either the deployer role setup or the engine
// grant logic fails a specific, named case.
func TestSeedRoleEnforcement(t *testing.T) {
	owner, _, rawAuth, superURL := dbboot.StartContainerWithRawAuth(t)
	ctx := context.Background()

	const seedRole = "app_test_seed"

	// Deployer mirror: create the seed role and let the authenticator switch
	// into it. The migrator grants its privileges; here we only make it exist.
	// A BYPASSRLS role can only be created by a superuser on Postgres < 16, and
	// production provisions roles from a superuser DSN — so we dial the
	// superuser connection here rather than the CREATEROLE owner login.
	super, err := pgx.Connect(ctx, superURL)
	require.NoError(t, err, "connect superuser")
	for _, stmt := range []string{
		"CREATE ROLE " + seedRole + " NOLOGIN BYPASSRLS",
		"GRANT " + seedRole + " TO authenticator",
	} {
		if _, err := super.Exec(ctx, stmt); err != nil {
			t.Fatalf("seed role setup %q: %v", stmt, err)
		}
	}
	require.NoError(t, super.Close(ctx))

	// Roles handed to the migrator: defaults plus the seed role. With Seed set,
	// generateSchemaGrants / generateExistingObjectGrants emit the seed grants.
	roles := domain.DefaultRoles()
	roles.Seed = seedRole

	cfg := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{},
		Storage: map[string]domain.Bucket{"avatars": {Public: true}},
		Tables: map[string]domain.Table{
			// public user table
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text", Required: true},
				},
			},
			// custom (non-reserved) user schema
			"items": {
				Schema: "shop",
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "label", Type: "text", Required: true},
				},
			},
		},
	}
	if err := app.NewMigrator(owner, roles).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate with seed role: %v", err)
	}

	// Owner seeds one row into every place we later probe.
	for _, stmt := range []string{
		"INSERT INTO public.todos (title) VALUES ('owner-seeded')",
		"INSERT INTO shop.items (label) VALUES ('owner-seeded')",
		// RLS deny-all table to prove BYPASSRLS still applies under the seed role.
		"CREATE TABLE public.secret (id bigserial PRIMARY KEY, body text)",
		"ALTER TABLE public.secret ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY deny_all ON public.secret USING (false) WITH CHECK (false)",
		"GRANT SELECT ON public.secret TO " + seedRole,
		"INSERT INTO public.secret (body) VALUES ('classified')",
	} {
		if _, err := owner.Exec(ctx, stmt); err != nil {
			t.Fatalf("owner seed %q: %v", stmt, err)
		}
	}

	// runAsSeed opens a fresh transaction on the raw authenticator connection,
	// switches into the seed role with SET LOCAL ROLE (exactly as execSQL does),
	// runs fn, then rolls back so each scenario is isolated.
	runAsSeed := func(t *testing.T, fn func(ctx context.Context, tx domain.Tx)) {
		t.Helper()
		tx, err := rawAuth.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback(ctx) }()
		_, err = tx.Exec(ctx, "SET LOCAL ROLE "+seedRole)
		require.NoError(t, err, "SET LOCAL ROLE must be permitted (membership grant)")
		fn(ctx, tx)
	}

	// --- the seed role IS the effective role ---
	t.Run("effective role is the seed role", func(t *testing.T) {
		runAsSeed(t, func(ctx context.Context, tx domain.Tx) {
			row, err := tx.QueryRow(ctx, "SELECT current_user AS who")
			require.NoError(t, err)
			assert.Equal(t, seedRole, row["who"])
		})
	})

	// --- ALLOWED: DML on user schemas (public + custom) ---
	allowed := []struct {
		name, sql string
	}{
		{"select public.todos", "SELECT count(*) FROM public.todos"},
		{"insert public.todos", "INSERT INTO public.todos (title) VALUES ('seed-wrote')"},
		{"update public.todos", "UPDATE public.todos SET title = 'x' WHERE title = 'owner-seeded'"},
		{"delete public.todos", "DELETE FROM public.todos WHERE title = 'nope'"},
		{"select shop.items (custom schema)", "SELECT count(*) FROM shop.items"},
		{"insert shop.items (custom schema)", "INSERT INTO shop.items (label) VALUES ('seed-wrote')"},
	}
	for _, tc := range allowed {
		t.Run("ALLOW "+tc.name, func(t *testing.T) {
			runAsSeed(t, func(ctx context.Context, tx domain.Tx) {
				_, err := tx.Exec(ctx, tc.sql)
				assert.NoError(t, err, "seed role must be allowed: %s", tc.sql)
			})
		})
	}

	// --- DENIED: reserved schemas, framework table, and all DDL ---
	denied := []struct {
		name, sql, wantSubstr string
	}{
		{"select auth.users", "SELECT count(*) FROM auth.users", "permission denied"},
		{"insert auth.users", "INSERT INTO auth.users (id) VALUES (gen_random_uuid())", "permission denied"},
		{"select storage.objects", "SELECT count(*) FROM storage.objects", "permission denied"},
		{"insert storage.objects", "INSERT INTO storage.objects (id) VALUES (gen_random_uuid())", "permission denied"},
		{"select _instancez_migrations", "SELECT count(*) FROM _instancez_migrations", "permission denied"},
		{"delete _instancez_migrations", "DELETE FROM _instancez_migrations", "permission denied"},
		{"create table (DDL)", "CREATE TABLE public.evil (id int)", "permission denied"},
		{"drop table (DDL)", "DROP TABLE public.todos", "must be owner"},
		{"alter table (DDL)", "ALTER TABLE public.todos ADD COLUMN evil int", "must be owner"},
		{"create schema (DDL)", "CREATE SCHEMA evil", "permission denied"},
	}
	for _, tc := range denied {
		t.Run("DENY "+tc.name, func(t *testing.T) {
			runAsSeed(t, func(ctx context.Context, tx domain.Tx) {
				_, err := tx.Exec(ctx, tc.sql)
				require.Error(t, err, "seed role must be denied: %s", tc.sql)
				assert.Truef(t, strings.Contains(strings.ToLower(err.Error()), tc.wantSubstr),
					"error for %q should mention %q, got: %v", tc.sql, tc.wantSubstr, err)
			})
		})
	}

	// --- BYPASSRLS: the seed role sees rows a deny-all policy would hide ---
	t.Run("BYPASSRLS reads RLS-protected rows", func(t *testing.T) {
		runAsSeed(t, func(ctx context.Context, tx domain.Tx) {
			row, err := tx.QueryRow(ctx, "SELECT count(*) AS n FROM public.secret")
			require.NoError(t, err, "seed role has SELECT on the table; BYPASSRLS skips the deny-all policy")
			assert.EqualValues(t, 1, row["n"], "BYPASSRLS must let the seed role see the protected row")
		})
	})
}
