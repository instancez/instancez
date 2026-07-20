package app

import (
	"context"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// `renamed_from` is the escape hatch for the one change the diff cannot infer:
// a rename is indistinguishable from drop+add at the config level. These lock
// down that it renames in place, never destroys, and is safe to leave in the
// YAML afterwards.

func tableWith(fields ...domain.Field) domain.Table {
	return domain.Table{Fields: fields}
}

func TestRenameColumnEmitsRenameNotDropAndAdd(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(
			domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true},
			domain.Field{Name: "body", Type: "text"},
		),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(
			domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true},
			domain.Field{Name: "content", Type: "text", RenamedFrom: "body"},
		),
	}}

	diff := diffConfigs(old, updated)
	all := strings.Join(append(append(diff.Renames, diff.Removals...), diff.Additions...), "\n")

	if !strings.Contains(all, "ALTER TABLE notes RENAME COLUMN body TO content;") {
		t.Errorf("expected rename statement, got:\n%s", all)
	}
	if strings.Contains(all, "DROP COLUMN") {
		t.Errorf("rename must not drop, got:\n%s", all)
	}
	if strings.Contains(all, "ADD COLUMN") {
		t.Errorf("rename must not re-add, got:\n%s", all)
	}
	if len(diff.Destroys) != 0 {
		t.Errorf("rename destroys nothing, got %v", diff.Destroys)
	}
}

// A declared rename must sail through the destructive gate.
func TestRenameIsNotDestructive(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(domain.Field{Name: "body", Type: "text"}),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(domain.Field{Name: "content", Type: "text", RenamedFrom: "body"}),
	}}

	if _, err := NewMigrator(nil, domain.DefaultRoles()).
		PlanStatements(context.Background(), old, updated); err != nil {
		t.Fatalf("declared rename must not trip the gate, got %v", err)
	}
}

func TestRenameTableEmitsRename(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"tomes": tableWith(domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true}),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"books": {
			RenamedFrom: "tomes",
			Fields:      []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}},
		},
	}}

	diff := diffConfigs(old, updated)
	all := strings.Join(append(append(diff.Renames, diff.Removals...), diff.Additions...), "\n")

	if !strings.Contains(all, "ALTER TABLE IF EXISTS tomes RENAME TO books;") {
		t.Errorf("expected table rename, got:\n%s", all)
	}
	if strings.Contains(all, "DROP TABLE") || strings.Contains(all, "CREATE TABLE") {
		t.Errorf("table rename must not drop or recreate, got:\n%s", all)
	}
	if len(diff.Destroys) != 0 {
		t.Errorf("table rename destroys nothing, got %v", diff.Destroys)
	}
}

// Once the rename has been applied, the stored config already carries the new
// name. Leaving `renamed_from` in the YAML must then be a no-op, not an attempt
// to rename a column that no longer exists.
func TestRenameIsIdempotentAfterApply(t *testing.T) {
	applied := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(domain.Field{Name: "content", Type: "text", RenamedFrom: "body"}),
	}}

	diff := diffConfigs(applied, applied)
	if len(diff.Renames) != 0 {
		t.Errorf("re-applying a completed rename must emit nothing, got %v", diff.Renames)
	}
	if len(diff.Removals) != 0 || len(diff.Additions) != 0 {
		t.Errorf("expected no-op, got removals=%v additions=%v", diff.Removals, diff.Additions)
	}
}

// Pointing renamed_from at something that was never there is just a new column.
func TestRenameFromUnknownColumnIsPlainAdd(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true}),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(
			domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true},
			domain.Field{Name: "content", Type: "text", RenamedFrom: "never_existed"},
		),
	}}

	diff := diffConfigs(old, updated)
	if len(diff.Renames) != 0 {
		t.Errorf("nothing to rename, got %v", diff.Renames)
	}
	if !strings.Contains(strings.Join(diff.Additions, "\n"), "ADD COLUMN content") {
		t.Errorf("expected plain add, got %v", diff.Additions)
	}
}

// If both names are live in the old config, renaming would collide. Treat the
// target as an ordinary existing column and leave the source to the normal
// drop path (which the gate still guards).
func TestRenameSkippedWhenTargetAlreadyExists(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(
			domain.Field{Name: "body", Type: "text"},
			domain.Field{Name: "content", Type: "text"},
		),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(domain.Field{Name: "content", Type: "text", RenamedFrom: "body"}),
	}}

	diff := diffConfigs(old, updated)
	if len(diff.Renames) != 0 {
		t.Errorf("must not rename onto an existing column, got %v", diff.Renames)
	}
	if !strings.Contains(strings.Join(diff.Destroys, ","), "notes.body") {
		t.Errorf("dropping the source column is still destructive, got %v", diff.Destroys)
	}
}

// Renaming a table and one of its columns in a single edit: the table rename
// must land first so the column rename can address the new table name.
func TestRenameTableAndColumnTogether(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"tomes": tableWith(domain.Field{Name: "body", Type: "text"}),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"books": {
			RenamedFrom: "tomes",
			Fields:      []domain.Field{{Name: "content", Type: "text", RenamedFrom: "body"}},
		},
	}}

	diff := diffConfigs(old, updated)
	if len(diff.Renames) != 2 {
		t.Fatalf("expected 2 renames, got %v", diff.Renames)
	}
	if !strings.Contains(diff.Renames[0], "ALTER TABLE IF EXISTS tomes RENAME TO books;") {
		t.Errorf("table rename must come first, got %v", diff.Renames)
	}
	if !strings.Contains(diff.Renames[1], "ALTER TABLE books RENAME COLUMN body TO content;") {
		t.Errorf("column rename must use the new table name, got %v", diff.Renames)
	}
	if len(diff.Destroys) != 0 {
		t.Errorf("nothing should be destroyed, got %v", diff.Destroys)
	}
}

// A rename that also changes the column type should rename first, then alter.
func TestRenameWithTypeChange(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(domain.Field{Name: "count", Type: "integer"}),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(domain.Field{Name: "total", Type: "bigint", RenamedFrom: "count"}),
	}}

	diff := diffConfigs(old, updated)
	if len(diff.Renames) != 1 || !strings.Contains(diff.Renames[0], "RENAME COLUMN count TO total;") {
		t.Fatalf("expected rename, got %v", diff.Renames)
	}
	alterations := strings.Join(diff.Alterations, "\n")
	if !strings.Contains(alterations, "ALTER TABLE notes ALTER COLUMN total TYPE bigint;") {
		t.Errorf("expected type change on the new name, got:\n%s", alterations)
	}
}

// The diff must not mutate the config it was handed.
func TestRenameDoesNotMutateInputConfig(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(domain.Field{Name: "body", Type: "text"}),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(domain.Field{Name: "content", Type: "text", RenamedFrom: "body"}),
	}}

	diffConfigs(old, updated)

	if got := old.Tables["notes"].Fields[0].Name; got != "body" {
		t.Errorf("old config was mutated: field is now %q", got)
	}
}

// Renames must be emitted before any other DDL in the applied plan.
func TestRenamesRunBeforeOtherStatements(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(domain.Field{Name: "body", Type: "text"}),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"notes": tableWith(
			domain.Field{Name: "content", Type: "text", RenamedFrom: "body"},
			domain.Field{Name: "extra", Type: "text"},
		),
	}}

	stmts, err := NewMigrator(nil, domain.DefaultRoles()).
		PlanStatements(context.Background(), old, updated)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	renameIdx, addIdx := -1, -1
	for i, s := range stmts {
		if strings.Contains(s, "RENAME COLUMN") && renameIdx < 0 {
			renameIdx = i
		}
		if strings.Contains(s, "ADD COLUMN extra") && addIdx < 0 {
			addIdx = i
		}
	}
	if renameIdx < 0 || addIdx < 0 {
		t.Fatalf("expected both rename and add, got %v", stmts)
	}
	if renameIdx > addIdx {
		t.Errorf("rename (%d) must precede add (%d)", renameIdx, addIdx)
	}
}
