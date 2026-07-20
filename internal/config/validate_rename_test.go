package config

import (
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

// renamed_from flows into ALTER TABLE ... RENAME, so it must clear the same
// identifier check as every other name before it can reach the DDL layer.

// One case is enough to prove field.renamed_from is routed through
// validateIdent; the identifier and reserved-keyword branches are
// validateIdent's own to cover.
func TestValidate_FieldRenamedFromInvalidIdent(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.Fields = append(table.Fields, domain.Field{
		Name: "content", Type: "text", RenamedFrom: "bad name;DROP",
	})
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.fields.content.renamed_from")
}

func TestValidate_TableRenamedFromInvalidIdent(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.RenamedFrom = "1bad"
	cfg.Tables["todos"] = table

	errs := Validate(cfg)
	assertHasErrorAt(t, errs, "tables.todos.renamed_from")
}

// A well-formed renamed_from must not raise a validation error.
func TestValidate_RenamedFromValidIsAccepted(t *testing.T) {
	cfg := validBaseConfig()
	table := cfg.Tables["todos"]
	table.RenamedFrom = "tasks"
	table.Fields = append(table.Fields, domain.Field{
		Name: "content", Type: "text", RenamedFrom: "body",
	})
	cfg.Tables["todos"] = table

	if errs := Validate(cfg); errs != nil {
		t.Fatalf("valid renamed_from must pass, got: %v", errs)
	}
}
