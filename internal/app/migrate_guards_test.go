package app

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func guardTable() domain.Table {
	return domain.Table{Fields: []domain.Field{
		{Name: "id", Type: "bigserial", PrimaryKey: true},
		{Name: "owner", Type: "text", Immutable: true},
		{Name: "updated_at", Type: "timestamptz", AutoUpdatedAt: true},
	}}
}

func TestGenerateIndexes_Method(t *testing.T) {
	tbl := domain.Table{Indexes: []domain.Index{
		{Columns: []string{"doc"}, Method: "gin"},
		{Columns: []string{"a"}},
		{Columns: []string{"b"}, Method: "btree"},
	}}
	got := strings.Join(generateIndexes("t", tbl), "\n")
	mustContain(t, got, "idx_t_doc ON t USING gin (doc)")
	mustContain(t, got, "idx_t_a ON t (a)")
	mustContain(t, got, "idx_t_b ON t (b)")
}

func TestDiffRemovedIndexes_MethodChangeDropsIndex(t *testing.T) {
	mk := func(m string) *domain.Config {
		return &domain.Config{Tables: map[string]domain.Table{"t": {Indexes: []domain.Index{{Columns: []string{"doc"}, Method: m}}}}}
	}
	if got := diffRemovedIndexes(mk(""), mk("gin")); len(got) != 1 {
		t.Fatalf("method change must drop index, got %v", got)
	}
	if got := diffRemovedIndexes(mk(""), mk("btree")); len(got) != 0 {
		t.Fatalf("btree == default, got %v", got)
	}
}

func TestGenerateGuards(t *testing.T) {
	got := strings.Join(generateGuards("notes", guardTable()), "\n")
	mustContain(t, got, "CREATE OR REPLACE FUNCTION inz_guard_notes()")
	mustContain(t, got, "IF NEW.owner IS DISTINCT FROM OLD.owner THEN")
	mustContain(t, got, "ERRCODE = '23514'")
	mustContain(t, got, "NEW.updated_at := now();")
	mustContain(t, got, "CREATE OR REPLACE TRIGGER inz_guard BEFORE UPDATE ON notes")
}

func TestGenerateGuards_SchemaQualifiedAndEmpty(t *testing.T) {
	tbl := guardTable()
	tbl.Schema = "app"
	mustContain(t, strings.Join(generateGuards("notes", tbl), "\n"), "ON app.notes")
	if got := generateGuards("t", domain.Table{Fields: []domain.Field{{Name: "id"}}}); len(got) != 0 {
		t.Fatalf("no options must emit nothing, got %v", got)
	}
}

func TestPlan_GuardsEmittedAndIdempotent(t *testing.T) {
	cfg := &domain.Config{Tables: map[string]domain.Table{"notes": guardTable()}}
	if !strings.Contains(strings.Join(planUpdateStatements(cfg, cfg, domain.DefaultRoles()), "\n"), "CREATE OR REPLACE TRIGGER inz_guard") {
		t.Fatal("re-apply must re-emit trigger")
	}
	if !strings.Contains(strings.Join(planFromScratchStatements(cfg, domain.DefaultRoles()), "\n"), "CREATE OR REPLACE TRIGGER inz_guard") {
		t.Fatal("fresh plan must emit trigger")
	}
}

func TestDiffRemovedGuards(t *testing.T) {
	plain := domain.Table{Fields: []domain.Field{{Name: "id", Type: "bigserial"}}}
	old := &domain.Config{Tables: map[string]domain.Table{"notes": guardTable(), "gone": guardTable()}}
	nu := &domain.Config{Tables: map[string]domain.Table{"notes": plain}}
	got := strings.Join(diffRemovedGuards(old, nu), "\n")
	mustContain(t, got, "DROP TRIGGER IF EXISTS inz_guard ON notes;")
	mustContain(t, got, "DROP FUNCTION IF EXISTS inz_guard_notes();")
	mustContain(t, got, "DROP FUNCTION IF EXISTS inz_guard_gone();")
	if strings.Contains(got, "ON gone") {
		t.Fatalf("table drop must not emit DROP TRIGGER on missing table:\n%s", got)
	}
	if got := diffRemovedGuards(old, old); len(got) != 0 {
		t.Fatalf("unchanged must emit nothing, got %v", got)
	}
}

func TestGuardFuncName_LongNamesStayUnique(t *testing.T) {
	a := strings.Repeat("a", 70) + "1"
	b := strings.Repeat("a", 70) + "2"
	fa, fb := guardFuncName(a, guardTable()), guardFuncName(b, guardTable())
	if len(fa) > 63 || len(fb) > 63 {
		t.Fatalf("name over 63 bytes: %d, %d", len(fa), len(fb))
	}
	if fa == fb {
		t.Fatalf("collision: %s", fa)
	}
	if fa != guardFuncName(a, guardTable()) {
		t.Fatal("not deterministic")
	}
	if got := guardFuncName("notes", guardTable()); got != "inz_guard_notes" {
		t.Fatalf("short name changed: %s", got)
	}
}

func TestDiff_RenameTableDropsOldGuardFunction(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{"notes": guardTable()}}
	renamed := guardTable()
	renamed.RenamedFrom = "notes"
	nu := &domain.Config{Tables: map[string]domain.Table{"memos": renamed}}
	stmts := strings.Join(planUpdateStatements(old, nu, domain.DefaultRoles()), "\n")
	drop := strings.Index(stmts, "DROP FUNCTION IF EXISTS inz_guard_notes() CASCADE;")
	create := strings.Index(stmts, "CREATE OR REPLACE FUNCTION inz_guard_memos()")
	if drop < 0 || create < 0 || drop > create {
		t.Fatalf("want old guard dropped before new created:\n%s", stmts)
	}
}

func TestDiffRemovedGuards_SchemaMoveDropsOldFunction(t *testing.T) {
	moved := guardTable()
	moved.Schema = "app"
	old := &domain.Config{Tables: map[string]domain.Table{"notes": guardTable()}}
	nu := &domain.Config{Tables: map[string]domain.Table{"notes": moved}}
	mustContain(t, strings.Join(diffRemovedGuards(old, nu), "\n"), "DROP FUNCTION IF EXISTS inz_guard_notes() CASCADE;")
}
