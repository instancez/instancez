package http

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestPrimaryKeyColumns(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text"},
			{Name: "sub", Type: "uuid", PrimaryKey: true},
		},
	}
	pks := table.PrimaryKeyColumns()
	if len(pks) != 2 {
		t.Fatalf("got %v, want 2 pks", pks)
	}
	if pks[0] != "id" || pks[1] != "sub" {
		t.Errorf("pks = %v", pks)
	}
}

func TestPrimaryKeyColumns_Empty(t *testing.T) {
	table := domain.Table{Fields: []domain.Field{{Name: "title", Type: "text"}}}
	if len(table.PrimaryKeyColumns()) != 0 {
		t.Error("expected no pks")
	}
}

func TestParseResolutionPrefer(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"return=minimal", ""},
		{"resolution=merge-duplicates", "merge"},
		{"resolution=ignore-duplicates", "ignore"},
		{"return=representation, resolution=merge-duplicates", "merge"},
		{"resolution=unknown", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := parseResolutionPrefer(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildUpsertQuery_Merge(t *testing.T) {
	record := map[string]any{"id": 1, "title": "t", "status": "active"}
	sql, args := buildUpsertQuery("todos", record, []string{"id"}, "merge", false)

	if !strings.HasPrefix(sql, "INSERT INTO todos") {
		t.Errorf("SQL = %q", sql)
	}
	if !strings.Contains(sql, "ON CONFLICT (id)") {
		t.Errorf("missing ON CONFLICT (id): %s", sql)
	}
	if !strings.Contains(sql, "DO UPDATE SET") {
		t.Errorf("missing DO UPDATE: %s", sql)
	}
	if strings.Contains(sql, "id = EXCLUDED.id") {
		t.Errorf("should not self-assign PK: %s", sql)
	}
	if !strings.Contains(sql, "status = EXCLUDED.status") {
		t.Errorf("missing status update: %s", sql)
	}
	if !strings.Contains(sql, "title = EXCLUDED.title") {
		t.Errorf("missing title update: %s", sql)
	}
	if len(args) != 3 {
		t.Errorf("args = %d, want 3", len(args))
	}
}

func TestBuildUpsertQuery_MergeReturning(t *testing.T) {
	record := map[string]any{"id": 1, "title": "t"}
	sql, _ := buildUpsertQuery("todos", record, []string{"id"}, "merge", true)
	if !strings.HasSuffix(sql, " RETURNING *") {
		t.Errorf("missing RETURNING: %s", sql)
	}
}

func TestBuildUpsertQuery_Ignore(t *testing.T) {
	record := map[string]any{"id": 1, "title": "t"}
	sql, _ := buildUpsertQuery("todos", record, []string{"id"}, "ignore", false)
	if !strings.Contains(sql, "DO NOTHING") {
		t.Errorf("expected DO NOTHING, got: %s", sql)
	}
}

func TestBuildUpsertQuery_KeyOnlyBodyUpdatesKeysToReturnRow(t *testing.T) {
	sql, _ := buildUpsertQuery("todos", map[string]any{"id": 1}, []string{"id"}, "merge", true)
	if !strings.Contains(sql, "DO UPDATE SET id = EXCLUDED.id") || !strings.HasSuffix(sql, " RETURNING *") {
		t.Errorf("key-only merge must DO UPDATE so RETURNING yields the row: %s", sql)
	}
	sql, _ = buildUpsertQuery("j", map[string]any{"a": 1, "b": 2}, []string{"a", "b"}, "merge", true)
	if !strings.Contains(sql, "DO UPDATE SET a = EXCLUDED.a, b = EXCLUDED.b") {
		t.Errorf("composite key-only merge: %s", sql)
	}
}

func TestBuildUpsertQuery_KeyOnlyIgnoreStaysDoNothing(t *testing.T) {
	sql, _ := buildUpsertQuery("todos", map[string]any{"id": 1}, []string{"id"}, "ignore", true)
	if !strings.Contains(sql, "DO NOTHING") {
		t.Errorf("ignore must stay DO NOTHING: %s", sql)
	}
}

func TestBuildUpsertQuery_EmptyRecordDoesNotEmitEmptySet(t *testing.T) {
	sql, _ := buildUpsertQuery("todos", map[string]any{}, []string{"id"}, "merge", false)
	if strings.Contains(sql, "DO UPDATE SET ") {
		t.Errorf("empty SET list: %s", sql)
	}
}

func TestBuildUpsertQuery_CompositeKey(t *testing.T) {
	record := map[string]any{"tenant": "a", "id": 1, "title": "t"}
	sql, _ := buildUpsertQuery("items", record, []string{"tenant", "id"}, "merge", false)
	if !strings.Contains(sql, "ON CONFLICT (tenant, id)") {
		t.Errorf("expected composite conflict target, got: %s", sql)
	}
	if strings.Contains(sql, "tenant = EXCLUDED.tenant") || strings.Contains(sql, "id = EXCLUDED.id") {
		t.Errorf("should not self-assign composite keys: %s", sql)
	}
	if !strings.Contains(sql, "title = EXCLUDED.title") {
		t.Errorf("missing non-key update: %s", sql)
	}
}
