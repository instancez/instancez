//go:build integration

package app_test

import (
	"context"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

// TestSchemaGrantsCoverAuthAndStorage confirms that anon, authenticated, and
// service_role receive the expected privileges on auth.users and
// storage.objects after a migrator apply with auth + storage configured.
// service_role is also checked for INSERT and UPDATE (it expects full DML).
func TestSchemaGrantsCoverAuthAndStorage(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()

	cfg := &domain.Config{
		Version: 1,
		Auth:    &domain.Auth{},
		Storage: map[string]domain.Bucket{"avatars": {Public: true}},
	}
	if err := app.NewMigrator(db).Apply(ctx, cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	selectPrivs := []struct{ role, fqn string }{
		{"anon", "auth.users"},
		{"anon", "storage.objects"},
		{"authenticated", "auth.users"},
		{"authenticated", "storage.objects"},
		{"service_role", "auth.users"},
		{"service_role", "storage.objects"},
	}
	for _, tc := range selectPrivs {
		row, err := db.QueryRow(ctx,
			`SELECT has_table_privilege($1, $2, 'SELECT')`, tc.role, tc.fqn)
		if err != nil {
			t.Fatalf("has_table_privilege(%s, %s, SELECT): %v", tc.role, tc.fqn, err)
		}
		if v, ok := row["has_table_privilege"].(bool); !ok || !v {
			t.Errorf("expected %s to have SELECT on %s", tc.role, tc.fqn)
		}
	}

	// service_role expects full DML on both auth and storage tables.
	dmlPrivs := []struct{ fqn, priv string }{
		{"auth.users", "INSERT"},
		{"auth.users", "UPDATE"},
		{"storage.objects", "INSERT"},
		{"storage.objects", "UPDATE"},
	}
	for _, tc := range dmlPrivs {
		row, err := db.QueryRow(ctx,
			`SELECT has_table_privilege($1, $2, $3)`, "service_role", tc.fqn, tc.priv)
		if err != nil {
			t.Fatalf("has_table_privilege(service_role, %s, %s): %v", tc.fqn, tc.priv, err)
		}
		if v, ok := row["has_table_privilege"].(bool); !ok || !v {
			t.Errorf("expected service_role to have %s on %s", tc.priv, tc.fqn)
		}
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
