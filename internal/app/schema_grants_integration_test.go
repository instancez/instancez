//go:build integration

package app_test

import (
	"context"
	"testing"

	"github.com/saedx1/ultrabase/internal/app"
	"github.com/saedx1/ultrabase/internal/domain"
	"github.com/saedx1/ultrabase/internal/testutil/dbboot"
)

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
