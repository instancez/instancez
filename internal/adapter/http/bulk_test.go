package http

import (
	"strings"
	"testing"
)

func TestUnionColumns(t *testing.T) {
	records := []map[string]any{
		{"id": 1, "title": "a"},
		{"id": 2, "title": "b", "status": "x"},
		{"id": 3, "note": "z"},
	}
	cols := unionColumns(records)
	want := []string{"id", "note", "status", "title"}
	if len(cols) != len(want) {
		t.Fatalf("got %v, want %v", cols, want)
	}
	for i, c := range cols {
		if c != want[i] {
			t.Errorf("cols[%d] = %q, want %q", i, c, want[i])
		}
	}
}

func TestBuildBulkInsertQuery_Uniform(t *testing.T) {
	records := []map[string]any{
		{"title": "a", "status": "active"},
		{"title": "b", "status": "done"},
	}
	sql, args := buildBulkInsertQuery("todos", records, false)
	// columns sorted: status, title
	want := "INSERT INTO todos (status, title) VALUES ($1, $2), ($3, $4)"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 4 {
		t.Errorf("args = %d, want 4", len(args))
	}
	// order is status, title in cols, so args = [active,a,done,b]
	if args[0] != "active" || args[1] != "a" || args[2] != "done" || args[3] != "b" {
		t.Errorf("args = %v", args)
	}
}

func TestBuildBulkInsertQuery_HeterogeneousUsesDefault(t *testing.T) {
	records := []map[string]any{
		{"title": "a"},
		{"title": "b", "status": "done"},
	}
	sql, args := buildBulkInsertQuery("todos", records, false)
	// cols: status, title
	// row1: status missing → DEFAULT
	if !strings.Contains(sql, "(DEFAULT, $1)") {
		t.Errorf("expected DEFAULT for missing status, got: %s", sql)
	}
	if !strings.Contains(sql, "($2, $3)") {
		t.Errorf("expected second row placeholders, got: %s", sql)
	}
	if len(args) != 3 {
		t.Errorf("args = %d, want 3", len(args))
	}
}

func TestBuildBulkInsertQuery_SingleRecord(t *testing.T) {
	records := []map[string]any{{"title": "a"}}
	sql, args := buildBulkInsertQuery("todos", records, true)
	if !strings.HasPrefix(sql, "INSERT INTO todos (title) VALUES ($1)") {
		t.Errorf("SQL = %q", sql)
	}
	if !strings.HasSuffix(sql, " RETURNING *") {
		t.Errorf("expected RETURNING *: %s", sql)
	}
	if len(args) != 1 {
		t.Errorf("args = %d, want 1", len(args))
	}
}

func TestBuildBulkUpsertQuery_Merge(t *testing.T) {
	records := []map[string]any{
		{"id": 1, "title": "a"},
		{"id": 2, "title": "b"},
	}
	sql, args := buildBulkUpsertQuery("todos", records, []string{"id"}, "merge", false)
	if !strings.Contains(sql, "ON CONFLICT (id)") {
		t.Errorf("missing ON CONFLICT: %s", sql)
	}
	if !strings.Contains(sql, "DO UPDATE SET title = EXCLUDED.title") {
		t.Errorf("missing DO UPDATE: %s", sql)
	}
	if strings.Contains(sql, "id = EXCLUDED.id") {
		t.Errorf("should not self-assign PK: %s", sql)
	}
	if len(args) != 4 {
		t.Errorf("args = %d, want 4", len(args))
	}
}

func TestBuildBulkUpsertQuery_Ignore(t *testing.T) {
	records := []map[string]any{{"id": 1, "title": "a"}, {"id": 2, "title": "b"}}
	sql, _ := buildBulkUpsertQuery("todos", records, []string{"id"}, "ignore", false)
	if !strings.Contains(sql, "DO NOTHING") {
		t.Errorf("expected DO NOTHING: %s", sql)
	}
}

func TestRenderRowTuples_ArgIndexing(t *testing.T) {
	records := []map[string]any{
		{"a": 1, "b": 2},
		{"a": 3, "b": 4},
	}
	cols := []string{"a", "b"}
	args, rows := renderRowTuples(records, cols, 5)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0] != "($5, $6)" || rows[1] != "($7, $8)" {
		t.Errorf("rows = %v", rows)
	}
	if len(args) != 4 {
		t.Errorf("args = %d, want 4", len(args))
	}
}
