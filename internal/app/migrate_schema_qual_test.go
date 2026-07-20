package app

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// Tables can live in a user-declared schema (anything but auth/storage). The
// incremental diff must schema-qualify every ALTER/DROP it emits, or the DDL
// runs against the wrong search_path and either errors or silently no-ops.

func reportingTable(fields ...domain.Field) domain.Table {
	return domain.Table{Schema: "reporting", Fields: fields}
}

func joinAll(d configDiff) string {
	return strings.Join(append(append(append(
		append([]string{}, d.Renames...), d.Removals...), d.Additions...), d.Alterations...), "\n")
}

func TestSchemaQualified_DropTableAndColumn(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"notes": reportingTable(
			domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true},
			domain.Field{Name: "body", Type: "text"},
		),
		"stale": reportingTable(domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true}),
	}}
	// Drop the "stale" table and the "body" column.
	updated := &domain.Config{Tables: map[string]domain.Table{
		"notes": reportingTable(domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true}),
	}}

	all := joinAll(diffConfigs(old, updated))
	for _, want := range []string{
		"DROP TABLE IF EXISTS reporting.stale;",
		"ALTER TABLE reporting.notes DROP COLUMN IF EXISTS body;",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

func TestSchemaQualified_AddColumnAndAlter(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"notes": reportingTable(domain.Field{Name: "n", Type: "integer"}),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"notes": reportingTable(
			domain.Field{Name: "n", Type: "bigint"},              // type change
			domain.Field{Name: "tag", Type: "text", Unique: true}, // new column + constraint
		),
	}}

	all := joinAll(diffConfigs(old, updated))
	for _, want := range []string{
		"ALTER TABLE reporting.notes ADD COLUMN tag",
		"ALTER TABLE reporting.notes ADD UNIQUE (tag);",
		"ALTER TABLE reporting.notes ALTER COLUMN n TYPE bigint;",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

func TestSchemaQualified_Rename(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"tomes": reportingTable(domain.Field{Name: "body", Type: "text"}),
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"books": {
			Schema:      "reporting",
			RenamedFrom: "tomes",
			Fields:      []domain.Field{{Name: "content", Type: "text", RenamedFrom: "body"}},
		},
	}}

	renames := strings.Join(diffConfigs(old, updated).Renames, "\n")
	// Source side qualified; RENAME TO takes a bare name.
	if !strings.Contains(renames, "ALTER TABLE reporting.tomes RENAME TO books;") {
		t.Errorf("table rename not schema-qualified:\n%s", renames)
	}
	if !strings.Contains(renames, "ALTER TABLE reporting.books RENAME COLUMN body TO content;") {
		t.Errorf("column rename not schema-qualified:\n%s", renames)
	}
}

func TestSchemaQualified_DropIndexAndPolicy(t *testing.T) {
	base := reportingTable(domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true})
	old := &domain.Config{Tables: map[string]domain.Table{"notes": {
		Schema:  "reporting",
		Fields:  base.Fields,
		Indexes: []domain.Index{{Columns: []string{"id"}}},
		RLS:     []domain.RLSPolicy{{Operations: []string{"select"}, Using: "true"}},
	}}}
	// Drop the index and all RLS.
	updated := &domain.Config{Tables: map[string]domain.Table{"notes": {
		Schema: "reporting",
		Fields: base.Fields,
	}}}

	all := joinAll(diffConfigs(old, updated))
	for _, want := range []string{
		"DROP INDEX IF EXISTS reporting.idx_notes_id;",
		"ON reporting.notes;",                                    // DROP POLICY ... ON reporting.notes
		"ALTER TABLE reporting.notes DISABLE ROW LEVEL SECURITY;",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

// Public-schema tables must stay bare (no "public." prefix).
func TestSchemaQualified_PublicStaysBare(t *testing.T) {
	old := &domain.Config{Tables: map[string]domain.Table{
		"notes": {Fields: []domain.Field{
			domain.Field{Name: "id", Type: "bigserial", PrimaryKey: true},
			domain.Field{Name: "body", Type: "text"},
		}},
	}}
	updated := &domain.Config{Tables: map[string]domain.Table{
		"notes": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
	}}

	all := joinAll(diffConfigs(old, updated))
	if strings.Contains(all, "public.") {
		t.Errorf("public schema must not be qualified:\n%s", all)
	}
	if !strings.Contains(all, "ALTER TABLE notes DROP COLUMN IF EXISTS body;") {
		t.Errorf("expected bare-name DDL:\n%s", all)
	}
}
