package app

import (
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestDiffTable_NewColumn(t *testing.T) {
	existing := []existingColumn{
		{Name: "id", DataType: "bigint", Nullable: false},
		{Name: "title", DataType: "text", Nullable: false},
	}
	desired := domain.Table{
		Fields: map[string]domain.Field{
			"id":          {Type: "bigserial", PrimaryKey: true},
			"title":       {Type: "text", Required: true},
			"description": {Type: "text"},
		},
	}

	ddl := diffTable("todos", existing, desired)
	if len(ddl) == 0 {
		t.Fatal("expected ALTER TABLE for new column")
	}

	found := false
	for _, stmt := range ddl {
		if strings.Contains(stmt, "ADD COLUMN") && strings.Contains(stmt, "description") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ADD COLUMN description, got: %v", ddl)
	}
}

func TestDiffTable_NoChanges(t *testing.T) {
	existing := []existingColumn{
		{Name: "id", DataType: "bigint", Nullable: false},
		{Name: "title", DataType: "text", Nullable: false},
	}
	desired := domain.Table{
		Fields: map[string]domain.Field{
			"id":    {Type: "bigserial", PrimaryKey: true},
			"title": {Type: "text", Required: true},
		},
	}

	ddl := diffTable("todos", existing, desired)
	if len(ddl) != 0 {
		t.Errorf("expected no changes, got: %v", ddl)
	}
}

func TestDiffTable_NullabilityChange(t *testing.T) {
	existing := []existingColumn{
		{Name: "id", DataType: "bigint", Nullable: false},
		{Name: "title", DataType: "text", Nullable: true}, // currently nullable
	}
	desired := domain.Table{
		Fields: map[string]domain.Field{
			"id":    {Type: "bigserial", PrimaryKey: true},
			"title": {Type: "text", Required: true}, // now required (NOT NULL)
		},
	}

	ddl := diffTable("todos", existing, desired)
	found := false
	for _, stmt := range ddl {
		if strings.Contains(stmt, "SET NOT NULL") && strings.Contains(stmt, "title") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SET NOT NULL for title, got: %v", ddl)
	}
}

func TestDiffTable_MultipleNewColumns(t *testing.T) {
	existing := []existingColumn{
		{Name: "id", DataType: "bigint", Nullable: false},
	}
	desired := domain.Table{
		Fields: map[string]domain.Field{
			"id":     {Type: "bigserial", PrimaryKey: true},
			"title":  {Type: "text", Required: true},
			"status": {Type: "text"},
			"count":  {Type: "integer"},
		},
	}

	ddl := diffTable("todos", existing, desired)
	// Should have 3 ADD COLUMN statements
	addCount := 0
	for _, stmt := range ddl {
		if strings.Contains(stmt, "ADD COLUMN") {
			addCount++
		}
	}
	if addCount != 3 {
		t.Errorf("expected 3 ADD COLUMN statements, got %d: %v", addCount, ddl)
	}
}

func TestDiffTable_FKFieldNoType(t *testing.T) {
	// FK fields with empty type should infer bigint and not produce spurious ALTER TYPE
	existing := []existingColumn{
		{Name: "id", DataType: "bigint", Nullable: false},
		{Name: "team_id", DataType: "bigint", Nullable: true},
	}
	desired := domain.Table{
		Fields: map[string]domain.Field{
			"id":      {Type: "bigserial", PrimaryKey: true},
			"team_id": {Type: "", ForeignKey: &domain.ForeignKey{References: "teams.id", OnDelete: "cascade"}},
		},
	}

	ddl := diffTable("categories", existing, desired)
	for _, stmt := range ddl {
		if strings.Contains(stmt, "TYPE") {
			t.Errorf("unexpected ALTER TYPE statement for FK field with inferred type: %s", stmt)
		}
	}
}

func TestNormalizeType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"bigserial", "bigint"},
		{"serial", "integer"},
		{"varchar(255)", "varchar"},
		{"bool", "boolean"},
		{"text", "text"},
		{"integer", "integer"},
		{"timestamptz", "timestamptz"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeType(tt.input)
			if got != tt.want {
				t.Errorf("normalizeType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeInformationSchemaType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"bigint", "bigint"},
		{"integer", "integer"},
		{"character varying", "varchar"},
		{"text", "text"},
		{"boolean", "boolean"},
		{"timestamp with time zone", "timestamptz"},
		{"uuid", "uuid"},
		{"jsonb", "jsonb"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeInformationSchemaType(tt.input)
			if got != tt.want {
				t.Errorf("normalizeInformationSchemaType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
