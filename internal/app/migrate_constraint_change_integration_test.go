//go:build integration

package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

func constraintCfg(onDelete string, check string) *domain.Config {
	qty := domain.Field{Name: "qty", Type: "integer", Check: check}
	return &domain.Config{Version: 1, Tables: map[string]domain.Table{
		"parents": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		"kids": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "parent_id", Type: "bigint", ForeignKey: &domain.ForeignKey{References: "parents.id", OnDelete: onDelete}},
			qty,
		}},
	}}
}

// Regression: on_delete and check changes on existing columns were never applied.
func TestMigrate_ConstraintChangesApplyToExistingDatabase(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db).AllowDestructive(true)

	if err := m.Apply(ctx, constraintCfg("cascade", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO parents DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO kids (parent_id, qty) VALUES (1, -3)"); err != nil {
		t.Fatalf("control: negative qty must be allowed before the check: %v", err)
	}
	if _, err := db.Exec(ctx, "DELETE FROM kids"); err != nil {
		t.Fatal(err)
	}

	if err := m.Apply(ctx, constraintCfg("restrict", "qty > 0")); err != nil {
		t.Fatalf("apply constraint change: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO kids (parent_id, qty) VALUES (1, -3)"); err == nil || !strings.Contains(err.Error(), "check") {
		t.Fatalf("check not applied: %v", err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO kids (parent_id, qty) VALUES (1, 3)"); err != nil {
		t.Fatalf("valid row rejected: %v", err)
	}
	if _, err := db.Exec(ctx, "DELETE FROM parents"); err == nil || !strings.Contains(err.Error(), "foreign key") {
		t.Fatalf("on_delete restrict not applied: %v", err)
	}

	// Reapplying the same config is a no-op, and dropping the check lifts it.
	if err := m.Apply(ctx, constraintCfg("restrict", "qty > 0")); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, constraintCfg("restrict", "")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO kids (parent_id, qty) VALUES (1, -9)"); err != nil {
		t.Fatalf("check not dropped: %v", err)
	}
	row, err := db.QueryRow(ctx, "SELECT count(*) FILTER (WHERE contype='f') AS fks FROM pg_constraint WHERE conrelid='kids'::regclass")
	if err != nil || row["fks"].(int64) != 1 {
		t.Fatalf("expected exactly one FK after changes, got %v err %v", row, err)
	}
}

// Adding a check that existing rows violate must fail loudly, not silently succeed.
func TestMigrate_AddingViolatedCheckFailsLoudly(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db).AllowDestructive(true)
	if err := m.Apply(ctx, constraintCfg("cascade", "")); err != nil {
		t.Fatal(err)
	}
	_, _ = db.Exec(ctx, "INSERT INTO parents DEFAULT VALUES")
	_, _ = db.Exec(ctx, "INSERT INTO kids (parent_id, qty) VALUES (1, -3)")
	if err := m.Apply(ctx, constraintCfg("cascade", "qty > 0")); err == nil {
		t.Fatal("expected migration error for violating rows")
	}
}
