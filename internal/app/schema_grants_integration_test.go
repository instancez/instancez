//go:build integration

package app_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	adapterauth "github.com/instancez/instancez/internal/adapter/auth"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

// hasPriv reports has_table_privilege(role, fqn, priv).
func hasPriv(t *testing.T, db domain.Database, role, fqn, priv string) bool {
	t.Helper()
	row, err := db.QueryRow(context.Background(), `SELECT has_table_privilege($1, $2, $3) AS ok`, role, fqn, priv)
	if err != nil {
		t.Fatalf("has_table_privilege(%s, %s, %s): %v", role, fqn, priv, err)
	}
	return row["ok"] == true
}

// authDefaultACLGrants counts default-privilege entries in schema auth for anon/authenticated.
func authDefaultACLGrants(t *testing.T, db domain.Database) int64 {
	t.Helper()
	row, err := db.QueryRow(context.Background(), `
		SELECT count(*) AS n FROM pg_default_acl d
		JOIN pg_namespace n ON n.oid = d.defaclnamespace
		CROSS JOIN LATERAL aclexplode(d.defaclacl) a
		WHERE n.nspname = 'auth' AND a.grantee IN ('anon'::regrole, 'authenticated'::regrole)`)
	if err != nil {
		t.Fatalf("pg_default_acl: %v", err)
	}
	return row["n"].(int64)
}

func authTables(t *testing.T, db domain.Database) []string {
	t.Helper()
	rows, err := db.Query(context.Background(),
		`SELECT 'auth.' || tablename AS fqn FROM pg_tables WHERE schemaname = 'auth' ORDER BY 1`)
	if err != nil {
		t.Fatalf("list auth tables: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r["fqn"].(string))
	}
	if len(out) < 8 {
		t.Fatalf("expected the full auth schema, got %v", out)
	}
	return out
}

// assertAuthClosed: anon/authenticated reach storage.objects (RLS-guarded) but no
// auth.* table, sequence, or migration history.
func assertAuthClosed(t *testing.T, db domain.Database) {
	t.Helper()
	type priv struct {
		role, fqn, priv string
		want            bool
	}
	cases := []priv{
		{"anon", "storage.objects", "SELECT", true},
		{"authenticated", "storage.objects", "INSERT", true},
		{"service_role", "storage.objects", "UPDATE", true},
		{"service_role", "auth.users", "SELECT", true},
		{"service_role", "auth.users", "INSERT", true},
		{"service_role", "auth.refresh_tokens", "DELETE", true},
		{"service_role", "auth.jwt_keys", "SELECT", false},
		{"service_role", "_instancez_migrations", "SELECT", false},
	}
	for _, role := range []string{"anon", "authenticated"} {
		for _, fqn := range authTables(t, db) {
			for _, p := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
				cases = append(cases, priv{role, fqn, p, false})
			}
		}
		cases = append(cases, priv{role, "_instancez_migrations", "SELECT", false})
	}
	for _, c := range cases {
		if got := hasPriv(t, db, c.role, c.fqn, c.priv); got != c.want {
			t.Errorf("%s %s on %s = %v, want %v", c.role, c.priv, c.fqn, got, c.want)
		}
	}

	row, err := db.QueryRow(context.Background(), `SELECT has_sequence_privilege('anon', 'auth.refresh_tokens_id_seq', 'USAGE') AS anon_seq,
		has_sequence_privilege('service_role', 'auth.refresh_tokens_id_seq', 'USAGE') AS svc_seq`)
	if err != nil {
		t.Fatalf("sequence privs: %v", err)
	}
	if row["anon_seq"] == true || row["svc_seq"] != true {
		t.Errorf("sequence grants wrong: %v", row)
	}
	if n := authDefaultACLGrants(t, db); n != 0 {
		t.Errorf("auth default ACL still grants anon/authenticated (%d entries)", n)
	}
}

func TestSchemaGrants_AuthClosedStorageOpen(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	cfg := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{Email: &domain.AuthEmail{}},
		Storage: map[string]domain.Bucket{"avatars": {Public: true}},
	}
	if err := app.NewMigrator(db).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	assertAuthClosed(t, db)
}

// TestSchemaGrants_UpgradeClosesPreviouslyOpenAuth: a DB migrated by an older binary
// (auth.* granted to anon/authenticated) is closed by the next migration.
func TestSchemaGrants_UpgradeClosesPreviouslyOpenAuth(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	cfg := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{Email: &domain.AuthEmail{}},
		Storage: map[string]domain.Bucket{"avatars": {Public: true}},
	}
	if err := app.NewMigrator(db).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, q := range []string{
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA auth TO anon, authenticated, service_role",
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA auth TO anon, authenticated",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA auth GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO anon, authenticated",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA auth GRANT USAGE, SELECT ON SEQUENCES TO anon, authenticated",
		"GRANT SELECT ON _instancez_migrations TO anon, authenticated, service_role",
	} {
		if _, err := db.Exec(ctx, q); err != nil {
			t.Fatalf("reopen %q: %v", q, err)
		}
	}
	if !hasPriv(t, db, "anon", "auth.jwt_keys", "SELECT") {
		t.Fatal("setup: legacy grants not applied")
	}

	// A changed config forces a new migration instead of the checksum early return.
	cfg.Tables = map[string]domain.Table{"extra": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}}}
	if err := app.NewMigrator(db).Apply(ctx, cfg); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	assertAuthClosed(t, db)
}

// TestAuthSchemaClosed_HelpersFKsAndServiceStillWork: closing auth.* must not break
// auth.uid() in policies, FKs to auth.users, or the auth service's bare-context writes.
func TestAuthSchemaClosed_HelpersFKsAndServiceStillWork(t *testing.T) {
	owner, req := dbboot.StartContainer(t)
	ctx := context.Background()
	cfg := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{},
		Tables: map[string]domain.Table{
			"notes": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "user_id", Type: "uuid", ForeignKey: &domain.ForeignKey{References: "auth.users.id"}},
				},
				RLS: []domain.RLSPolicy{
					{Operations: []string{"insert"}, WithCheck: "user_id = auth.uid()"},
					{Operations: []string{"select"}, Using: "user_id = auth.uid()"},
				},
			},
		},
	}
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	uid := "11111111-1111-1111-1111-111111111111"
	if _, err := owner.Exec(ctx, `INSERT INTO auth.users (id, email) VALUES ($1::uuid, 'a@example.com')`, uid); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	anonCtx, _ := req.WithRLS(ctx, domain.Session{Role: "anon"})
	row, err := req.QueryRow(anonCtx, `SELECT auth.uid() IS NULL AS no_uid, auth.role() AS role`)
	if err != nil {
		t.Fatalf("anon auth helpers: %v", err)
	}
	if row["no_uid"] != true || row["role"] != "anon" {
		t.Fatalf("anon helpers = %v", row)
	}
	for _, q := range []string{
		"SELECT 1 FROM auth.users", "SELECT 1 FROM auth.jwt_keys",
		"SELECT 1 FROM auth.refresh_tokens", "SELECT 1 FROM _instancez_migrations",
	} {
		if _, err := req.Query(anonCtx, q); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("anon %s: want permission denied, got %v", q, err)
		}
	}

	userCtx, _ := req.WithRLS(ctx, domain.Session{Role: "authenticated", UserID: uid, IsAuthenticated: true})
	if _, err := req.Exec(userCtx, `INSERT INTO notes (user_id) VALUES ($1::uuid)`, uid); err != nil {
		t.Fatalf("authenticated insert with FK to auth.users: %v", err)
	}
	rows, err := req.Query(userCtx, `SELECT id FROM notes`)
	if err != nil || len(rows) != 1 {
		t.Fatalf("authenticated select under RLS: rows=%v err=%v", rows, err)
	}
	if _, err := req.Query(userCtx, "SELECT 1 FROM auth.users"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("authenticated auth.users: want permission denied, got %v", err)
	}

	svc := adapterauth.NewService(req.Database, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.InsertRefreshToken(context.Background(), uid, "tok-bare-ctx", domain.SessionMeta{}, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatalf("auth service on bare context must run as service_role: %v", err)
	}
}

// TestNonPublicSchema_AnonAccessWorks confirms that a table declared with
// schema: <custom> picks up USAGE + table grants for anon, so anon can
// touch it without "permission denied for schema" errors. This used to
// fail before generateSchemaGrants because only public got grants.
func TestNonPublicSchema_AnonAccessWorks(t *testing.T) {
	owner, auth := dbboot.StartContainer(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{},
		Tables: map[string]domain.Table{
			"items": {
				Schema: "shop",
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "label", Type: "text", Required: true},
				},
			},
		},
	}
	if err := app.NewMigrator(owner).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Owner seeds a row.
	if _, err := owner.Exec(ctx, "INSERT INTO shop.items (label) VALUES ('seeded')"); err != nil {
		t.Fatalf("owner insert: %v", err)
	}

	// Anon (request pool with SET LOCAL ROLE anon) should be able to read.
	rctx, _ := auth.WithRLS(ctx, domain.Session{Role: "anon"})
	tx, err := auth.Begin(rctx)
	if err != nil {
		t.Fatalf("auth begin: %v", err)
	}
	defer tx.Rollback(rctx)

	rows, err := tx.Query(rctx, "SELECT label FROM shop.items")
	if err != nil {
		t.Fatalf("anon select on non-public schema: %v", err)
	}
	if len(rows) != 1 || rows[0]["label"] != "seeded" {
		t.Fatalf("anon read: got %v, want one row labeled 'seeded'", rows)
	}

	// Anon should also be able to insert (no RLS policies on the table).
	if _, err := tx.Exec(rctx, "INSERT INTO shop.items (label) VALUES ('anon-write')"); err != nil {
		t.Fatalf("anon insert on non-public schema: %v", err)
	}
}
