//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

// These exercise the anti-dataloss guarantees against a real Postgres, because
// they depend on server-side behavior (FK dependency rejection, DDL rollback)
// that a config-level unit test can only assume.

func rowCount(t *testing.T, db *postgres.DB, table string) int {
	t.Helper()
	row, err := db.QueryRow(context.Background(), "SELECT COUNT(*) AS n FROM "+table)
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	switch n := row["n"].(type) {
	case int64:
		return int(n)
	case int:
		return n
	default:
		t.Fatalf("unexpected count type %T", row["n"])
		return 0
	}
}

func fkConfig(pk, fk string) *domain.Config {
	return &domain.Config{Tables: map[string]domain.Table{
		"parent": {Fields: []domain.Field{
			{Name: pk, Type: "uuid", PrimaryKey: true, Default: "gen_random_uuid()"},
		}},
		"child": {Fields: []domain.Field{
			{Name: "id", Type: "uuid", PrimaryKey: true, Default: "gen_random_uuid()"},
			{Name: fk, Type: "uuid", ForeignKey: &domain.ForeignKey{
				References: "parent." + pk, OnDelete: "cascade",
			}},
		}},
	}}
}

// A column rename arrives as drop+add. The gate must stop it and the data must
// still be there afterwards.
func TestIntegration_RenameColumnIsBlockedAndDataSurvives(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)

	v1 := &domain.Config{Tables: map[string]domain.Table{
		"notes": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "body", Type: "text"},
		}},
	}}

	if err := app.NewMigrator(db).Apply(ctx, v1); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO notes (body) VALUES ('keep me')"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Rename body -> content.
	v2 := &domain.Config{Tables: map[string]domain.Table{
		"notes": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "content", Type: "text"},
		}},
	}}

	err := app.NewMigrator(db).Apply(ctx, v2)
	if err == nil {
		t.Fatal("expected rename-as-drop to be blocked")
	}
	if !errors.Is(err, app.ErrDestructive) {
		t.Fatalf("expected ErrDestructive, got %v", err)
	}

	row, qerr := db.QueryRow(ctx, "SELECT body FROM notes LIMIT 1")
	if qerr != nil {
		t.Fatalf("original column must survive a blocked migration: %v", qerr)
	}
	if row["body"] != "keep me" {
		t.Errorf("data lost: got %v", row["body"])
	}
}

// DROP TABLE without CASCADE must be rejected by Postgres while a child FK
// depends on it, and the whole migration must roll back. This is what makes the
// removed CASCADE load-bearing rather than cosmetic.
func TestIntegration_DropParentTableWithChildFKRollsBack(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)

	v1 := fkConfig("id", "parent_id")
	if err := app.NewMigrator(db).Apply(ctx, v1); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	if _, err := db.Exec(ctx, `
		WITH p AS (INSERT INTO parent DEFAULT VALUES RETURNING id)
		INSERT INTO child (parent_id) SELECT id FROM p`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Drop parent while child still references it. Opt in, so the gate is not
	// what stops this — Postgres is.
	v2 := &domain.Config{Tables: map[string]domain.Table{
		"child": v1.Tables["child"],
	}}
	if err := app.NewMigrator(db).AllowDestructive(true).Apply(ctx, v2); err == nil {
		t.Fatal("expected Postgres to reject dropping a table with a dependent FK")
	}

	// Rollback must leave both tables and their rows untouched.
	if !tableExists(t, db, "parent") {
		t.Error("parent table should survive the rolled-back migration")
	}
	if got := rowCount(t, db, "parent"); got != 1 {
		t.Errorf("parent rows = %d, want 1", got)
	}
	if got := rowCount(t, db, "child"); got != 1 {
		t.Errorf("child rows = %d, want 1", got)
	}

	// The FK must still be enforced — a CASCADE drop would have silently
	// removed it and left this insert succeeding.
	if _, err := db.Exec(ctx,
		"INSERT INTO child (parent_id) VALUES ('00000000-0000-0000-0000-000000000000')"); err == nil {
		t.Error("child FK constraint was lost: orphan insert succeeded")
	}
}

// --- renamed_from ---

// The point of renamed_from: the data is still there under the new name.
func TestIntegration_RenamedFromColumnPreservesData(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)

	v1 := &domain.Config{Tables: map[string]domain.Table{
		"notes": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "body", Type: "text"},
		}},
	}}
	if err := app.NewMigrator(db).Apply(ctx, v1); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO notes (body) VALUES ('survives')"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	v2 := &domain.Config{Tables: map[string]domain.Table{
		"notes": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "content", Type: "text", RenamedFrom: "body"},
		}},
	}}
	if err := app.NewMigrator(db).Apply(ctx, v2); err != nil {
		t.Fatalf("rename must apply without the destructive opt-in: %v", err)
	}

	row, err := db.QueryRow(ctx, "SELECT content FROM notes LIMIT 1")
	if err != nil {
		t.Fatalf("select renamed column: %v", err)
	}
	if row["content"] != "survives" {
		t.Errorf("content = %v, want 'survives'", row["content"])
	}
	if got := rowCount(t, db, "notes"); got != 1 {
		t.Errorf("row count = %d, want 1", got)
	}
}

// Re-applying a config that still carries renamed_from must be a clean no-op,
// not an attempt to rename a column that is already renamed.
func TestIntegration_RenamedFromIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)

	v1 := &domain.Config{Tables: map[string]domain.Table{
		"notes": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "body", Type: "text"},
		}},
	}}
	if err := app.NewMigrator(db).Apply(ctx, v1); err != nil {
		t.Fatalf("apply v1: %v", err)
	}

	v2 := &domain.Config{Version: 2, Tables: map[string]domain.Table{
		"notes": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "content", Type: "text", RenamedFrom: "body"},
		}},
	}}
	if err := app.NewMigrator(db).Apply(ctx, v2); err != nil {
		t.Fatalf("apply v2: %v", err)
	}

	// Same config, bumped version so the checksum differs and the plan re-runs.
	v3 := &domain.Config{Version: 3, Tables: v2.Tables}
	if err := app.NewMigrator(db).Apply(ctx, v3); err != nil {
		t.Fatalf("re-applying a completed rename must be a no-op: %v", err)
	}

	if _, err := db.QueryRow(ctx, "SELECT content FROM notes LIMIT 1"); err != nil {
		t.Errorf("column should still be there: %v", err)
	}
}

// Renaming a table must carry its rows and its children's FK with it.
func TestIntegration_RenamedFromTablePreservesDataAndFK(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)

	if err := app.NewMigrator(db).Apply(ctx, fkConfig("id", "parent_id")); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	if _, err := db.Exec(ctx, `
		WITH p AS (INSERT INTO parent DEFAULT VALUES RETURNING id)
		INSERT INTO child (parent_id) SELECT id FROM p`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// parent -> owners, with child's FK following the rename.
	v2 := &domain.Config{Tables: map[string]domain.Table{
		"owners": {
			RenamedFrom: "parent",
			Fields: []domain.Field{
				{Name: "id", Type: "uuid", PrimaryKey: true, Default: "gen_random_uuid()"},
			},
		},
		"child": {Fields: []domain.Field{
			{Name: "id", Type: "uuid", PrimaryKey: true, Default: "gen_random_uuid()"},
			{Name: "parent_id", Type: "uuid", ForeignKey: &domain.ForeignKey{
				References: "owners.id", OnDelete: "cascade",
			}},
		}},
	}}
	if err := app.NewMigrator(db).Apply(ctx, v2); err != nil {
		t.Fatalf("table rename must apply without the destructive opt-in: %v", err)
	}

	if tableExists(t, db, "parent") {
		t.Error("old table name should be gone")
	}
	if got := rowCount(t, db, "owners"); got != 1 {
		t.Errorf("owners rows = %d, want 1", got)
	}
	if got := rowCount(t, db, "child"); got != 1 {
		t.Errorf("child rows = %d, want 1", got)
	}
	// Postgres renames follow the constraint, so the FK must still bite.
	if _, err := db.Exec(ctx,
		"INSERT INTO child (parent_id) VALUES ('00000000-0000-0000-0000-000000000000')"); err == nil {
		t.Error("FK should still be enforced after the table rename")
	}
}

// Renaming a child FK column keeps both the values and the constraint.
func TestIntegration_RenamedFromChildFKColumnPreservesData(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)

	if err := app.NewMigrator(db).Apply(ctx, fkConfig("id", "parent_id")); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	if _, err := db.Exec(ctx, `
		WITH p AS (INSERT INTO parent DEFAULT VALUES RETURNING id)
		INSERT INTO child (parent_id) SELECT id FROM p`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	v2 := fkConfig("id", "owner_id")
	child := v2.Tables["child"]
	child.Fields[1].RenamedFrom = "parent_id"
	v2.Tables["child"] = child

	if err := app.NewMigrator(db).Apply(ctx, v2); err != nil {
		t.Fatalf("FK column rename must apply cleanly: %v", err)
	}

	row, err := db.QueryRow(ctx, "SELECT owner_id FROM child LIMIT 1")
	if err != nil {
		t.Fatalf("select renamed FK column: %v", err)
	}
	if row["owner_id"] == nil {
		t.Error("FK value was lost in the rename")
	}
	if _, err := db.Exec(ctx,
		"INSERT INTO child (owner_id) VALUES ('00000000-0000-0000-0000-000000000000')"); err == nil {
		t.Error("FK should still be enforced after the column rename")
	}
}

// Renaming a child FK column is the silent case: the drop takes the column data
// and the constraint with it. The gate must refuse.
func TestIntegration_RenameChildFKColumnIsBlocked(t *testing.T) {
	ctx := context.Background()
	db := startPostgres(t)

	if err := app.NewMigrator(db).Apply(ctx, fkConfig("id", "parent_id")); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	if _, err := db.Exec(ctx, `
		WITH p AS (INSERT INTO parent DEFAULT VALUES RETURNING id)
		INSERT INTO child (parent_id) SELECT id FROM p`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := app.NewMigrator(db).Apply(ctx, fkConfig("id", "owner_id"))
	if !errors.Is(err, app.ErrDestructive) {
		t.Fatalf("expected ErrDestructive, got %v", err)
	}

	row, qerr := db.QueryRow(ctx, "SELECT parent_id FROM child LIMIT 1")
	if qerr != nil {
		t.Fatalf("child.parent_id must survive: %v", qerr)
	}
	if row["parent_id"] == nil {
		t.Error("FK value was lost")
	}
}
