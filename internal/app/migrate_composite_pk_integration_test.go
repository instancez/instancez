//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

func compositePKConfig() *domain.Config {
	return &domain.Config{Version: 1, Tables: map[string]domain.Table{
		"teams": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		"players": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "team_id", ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}},
		}},
		"memberships": {Fields: []domain.Field{
			{Name: "note", Type: "text"},
			{Name: "team_id", ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}, PrimaryKey: true},
			{Name: "user_key", Type: "text", PrimaryKey: true},
		}},
		"grid": {Fields: []domain.Field{
			{Name: "x", Type: "int", PrimaryKey: true},
			{Name: "y", Type: "int", PrimaryKey: true},
			{Name: "z", Type: "int", PrimaryKey: true},
		}},
	}}
}

func pkColumns(t *testing.T, ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) (map[string]any, error)
}, table string) string {
	t.Helper()
	row, err := db.QueryRow(ctx, `SELECT string_agg(a.attname, ',' ORDER BY k.ord) AS cols
		FROM pg_constraint c
		CROSS JOIN LATERAL unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = k.attnum
		WHERE c.contype = 'p' AND c.conrelid = ('public.' || $1)::regclass`, table)
	if err != nil {
		t.Fatalf("pk of %s: %v", table, err)
	}
	return fmt.Sprint(row["cols"])
}

func TestIntegration_CompositePrimaryKeyFreshDeploy(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	cfg := compositePKConfig()
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("fresh deploy: %v", err)
	}
	for table, want := range map[string]string{"teams": "id", "players": "id", "memberships": "team_id,user_key", "grid": "x,y,z"} {
		if got := pkColumns(t, ctx, db, table); got != want {
			t.Errorf("%s pk = %q, want %q", table, got, want)
		}
	}

	if _, err := db.Exec(ctx, `INSERT INTO teams DEFAULT VALUES`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO players (team_id) VALUES (1)`); err != nil {
		t.Fatalf("single-PK FK must be unaffected: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO memberships (team_id, user_key) VALUES (1, 'a'), (1, 'b')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO memberships (team_id, user_key) VALUES (1, 'a')`); err == nil || !strings.Contains(err.Error(), "23505") && !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("duplicate composite key must be rejected, got %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO memberships (team_id, user_key) VALUES (1, NULL)`); err == nil {
		t.Fatal("composite PK columns must be NOT NULL")
	}

	// Re-applying the same config (and a harmless column add) keeps the key.
	cfg.Tables["memberships"] = domain.Table{Fields: append(cfg.Tables["memberships"].Fields, domain.Field{Name: "since", Type: "timestamptz"})}
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("additive update: %v", err)
	}
	if got := pkColumns(t, ctx, db, "memberships"); got != "team_id,user_key" {
		t.Fatalf("pk after update = %q", got)
	}
}

func TestIntegration_CompositePrimaryKeyChangeRejected(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db).AllowDestructive(true)
	cfg := compositePKConfig()
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	grid := cfg.Tables["grid"]
	grid.Fields = []domain.Field{grid.Fields[0], grid.Fields[1], {Name: "z", Type: "int"}}
	cfg.Tables["grid"] = grid
	err := m.Apply(ctx, cfg)
	if !errors.Is(err, app.ErrPrimaryKeyChange) || !strings.Contains(err.Error(), "grid: (x, y, z) -> (x, y)") {
		t.Fatalf("want ErrPrimaryKeyChange naming grid, got %v", err)
	}
	if got := pkColumns(t, ctx, db, "grid"); got != "x,y,z" {
		t.Fatalf("pk must be untouched, got %q", got)
	}
}

// driftTo applies live, then records claimed as the applied config, like the old
// migrator did when a second primary_key flag was added to a live table.
func driftTo(t *testing.T, ctx context.Context, db interface {
	Exec(context.Context, string, ...any) (int64, error)
}, m *app.Migrator, live, claimed *domain.Config) {
	t.Helper()
	if err := m.Apply(ctx, live); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(claimed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE _instancez_migrations SET config_json = $1, checksum = 'drifted'
		WHERE id = (SELECT max(id) FROM _instancez_migrations)`, string(b)); err != nil {
		t.Fatal(err)
	}
}

func driftPairs(pk ...string) *domain.Config {
	fields := []domain.Field{{Name: "a", Type: "int"}, {Name: "b", Type: "int"}, {Name: "note", Type: "text"}}
	for i := range fields {
		fields[i].PrimaryKey = slices.Contains(pk, fields[i].Name)
	}
	return &domain.Config{Version: 1, Tables: map[string]domain.Table{
		"pairs": {Fields: fields},
		"refs": {Fields: []domain.Field{
			{Name: "id", Type: "int", PrimaryKey: true},
			{Name: "pair_a", ForeignKey: &domain.ForeignKey{References: "pairs.a"}},
		}},
	}}
}

func TestIntegration_CompositePKDriftCanBeReverted(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	driftTo(t, ctx, db, m, driftPairs("a"), driftPairs("a", "b"))

	if err := m.Apply(ctx, driftPairs("a")); err != nil {
		t.Fatalf("reverting the config to the live key must apply, got %v", err)
	}
	if got := pkColumns(t, ctx, db, "pairs"); got != "a" {
		t.Fatalf("pk = %q", got)
	}
	if _, err := db.Exec(ctx, `INSERT INTO pairs (a, b) VALUES (1, NULL)`); err != nil {
		t.Fatalf("b left the key in config, so it must be nullable again: %v", err)
	}
}

func TestIntegration_CompositePKDriftKeepsBooting(t *testing.T) {
	db := startPostgres(t)
	ctx := context.Background()
	m := app.NewMigrator(db)
	driftTo(t, ctx, db, m, driftPairs("a"), driftPairs("a", "b"))

	withNote := driftPairs("a", "b")
	withNote.Tables["pairs"] = domain.Table{Fields: append(withNote.Tables["pairs"].Fields, domain.Field{Name: "extra", Type: "text"})}
	if err := m.Apply(ctx, withNote); err != nil {
		t.Fatalf("an unrelated change on a drifted app must apply, got %v", err)
	}
	moved := driftPairs("b")
	err := m.Apply(ctx, moved)
	if !errors.Is(err, app.ErrPrimaryKeyChange) || !strings.Contains(err.Error(), "pairs: (a, b) -> (b)") {
		t.Fatalf("a key the live table doesn't have must still be rejected, got %v", err)
	}
}
