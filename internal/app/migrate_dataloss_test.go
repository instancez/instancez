package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// Regression tests for silent data loss in the config diff. Renames are
// indistinguishable from drop+add at the config level, so these lock down the
// three behaviors that keep a rename from quietly destroying data:
// deterministic removal order, no CASCADE on DROP TABLE, and an opt-in gate on
// destructive DDL.

// parentChild builds a two-table config where child.<fk> references parent.<pk>.
func parentChild(pk, fk string) *domain.Config {
	return &domain.Config{Tables: map[string]domain.Table{
		"parent": {Fields: []domain.Field{{Name: pk, Type: "uuid", PrimaryKey: true}}},
		"child": {Fields: []domain.Field{
			{Name: "id", Type: "uuid", PrimaryKey: true},
			{Name: fk, Type: "uuid", ForeignKey: &domain.ForeignKey{References: "parent." + pk}},
		}},
	}}
}

// A coupled rename (parent PK + child FK in one edit) must produce the same
// statement order every run. Map-iteration order previously decided whether
// Postgres rejected the migration or silently dropped both columns.
func TestRemovalOrderIsDeterministic(t *testing.T) {
	old, updated := parentChild("id", "parent_id"), parentChild("uid", "owner_id")

	first := strings.Join(diffConfigs(old, updated).Removals, "\n")
	for i := 0; i < 200; i++ {
		got := strings.Join(diffConfigs(old, updated).Removals, "\n")
		if got != first {
			t.Fatalf("removal order is nondeterministic\nrun 0:\n%s\n\nrun %d:\n%s", first, i, got)
		}
	}
}

// DROP TABLE ... CASCADE silently drops FK constraints on surviving child
// tables, and nothing re-creates them. Without CASCADE, Postgres refuses and
// the transaction rolls back, which is the outcome we want.
func TestDropTableDoesNotCascade(t *testing.T) {
	old := parentChild("id", "parent_id")
	updated := &domain.Config{Tables: map[string]domain.Table{
		"child": old.Tables["child"],
	}}

	joined := strings.Join(diffConfigs(old, updated).Removals, "\n")
	if !strings.Contains(joined, "DROP TABLE IF EXISTS parent") {
		t.Fatalf("expected parent table drop, got:\n%s", joined)
	}
	if strings.Contains(joined, "CASCADE") {
		t.Errorf("DROP TABLE must not CASCADE (silently destroys child FKs):\n%s", joined)
	}
}

// Dropping a parent and its child together must drop the child first, or
// Postgres rejects the parent drop on the still-live FK. Alphabetical order
// gets this wrong whenever the parent sorts first ("authors" before "books").
func TestTablesDropChildBeforeParent(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"authors": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		"books": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "author_id", Type: "bigint", ForeignKey: &domain.ForeignKey{References: "authors.id"}},
		}},
	}}

	removals := diffConfigs(old, &domain.Config{}).Removals
	var order []string
	for _, s := range removals {
		if strings.HasPrefix(s, "DROP TABLE") {
			order = append(order, s)
		}
	}
	if len(order) != 2 {
		t.Fatalf("expected 2 table drops, got %v", order)
	}
	if !strings.Contains(order[0], "books") {
		t.Errorf("child must drop first, got order: %v", order)
	}
}

func TestDestructiveMigrationBlockedWithoutOptIn(t *testing.T) {
	old := parentChild("id", "parent_id")
	updated := parentChild("uid", "owner_id")

	m := NewMigrator(nil, domain.DefaultRoles())
	_, err := m.PlanStatements(context.Background(), old, updated)
	if err == nil {
		t.Fatal("expected destructive plan to be rejected without opt-in")
	}
	if !errors.Is(err, ErrDestructive) {
		t.Fatalf("expected ErrDestructive, got %v", err)
	}
	// The message is the whole mitigation for renames: it must name what dies.
	for _, want := range []string{"parent.id", "child.parent_id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name dropped object %q, got: %v", want, err)
		}
	}
}

func TestDestructiveMigrationAllowedWithOptIn(t *testing.T) {
	old := parentChild("id", "parent_id")
	updated := parentChild("uid", "owner_id")

	m := NewMigrator(nil, domain.DefaultRoles()).AllowDestructive(true)
	m.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	stmts, err := m.PlanStatements(context.Background(), old, updated)
	if err != nil {
		t.Fatalf("opt-in plan must succeed, got %v", err)
	}
	if len(stmts) == 0 {
		t.Fatal("expected statements")
	}
}

// Additive changes must never trip the gate.
func TestAdditiveMigrationNeverBlocked(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"todos": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"todos": {Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text"},
		}},
		"tags": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
	}}

	if _, err := NewMigrator(nil, domain.DefaultRoles()).
		PlanStatements(context.Background(), old, updated); err != nil {
		t.Fatalf("additive plan must not be gated, got %v", err)
	}
}

// Dropping an index or an RLS policy is rebuildable, not data loss, so it must
// not require the destructive opt-in.
func TestRebuildableDropsAreNotDestructive(t *testing.T) {
	withIndex := &domain.Config{Tables: map[string]domain.Table{
		"todos": {
			Fields:  []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}},
			Indexes: []domain.Index{{Columns: []string{"id"}}},
		},
	}}
	withoutIndex := &domain.Config{Tables: map[string]domain.Table{
		"todos": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
	}}

	if _, err := NewMigrator(nil, domain.DefaultRoles()).
		PlanStatements(context.Background(), withIndex, withoutIndex); err != nil {
		t.Fatalf("index drop must not be gated, got %v", err)
	}
}
