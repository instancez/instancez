package http

import (
	"strings"
	"testing"
)

func TestParseOnConflictParam_Empty(t *testing.T) {
	cols, err := parseOnConflictParam("", testTable())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cols != nil {
		t.Errorf("expected nil, got %v", cols)
	}
}

func TestParseOnConflictParam_Single(t *testing.T) {
	cols, err := parseOnConflictParam("title", testTable())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 1 || cols[0] != "title" {
		t.Errorf("got %v", cols)
	}
}

func TestParseOnConflictParam_Multiple(t *testing.T) {
	cols, err := parseOnConflictParam("status,priority", testTable())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 2 || cols[0] != "status" || cols[1] != "priority" {
		t.Errorf("got %v", cols)
	}
}

func TestParseOnConflictParam_RejectsUnknown(t *testing.T) {
	if _, err := parseOnConflictParam("bogus", testTable()); err == nil {
		t.Error("expected rejection for unknown column")
	}
}

func TestParseOnConflictParam_RejectsInjection(t *testing.T) {
	if _, err := parseOnConflictParam("id) DROP TABLE x--", testTable()); err == nil {
		t.Error("expected rejection for injection attempt")
	}
}

func TestBuildBulkUpsertQuery_WithCustomConflictCols(t *testing.T) {
	records := []map[string]any{{"slug": "a", "title": "A"}}
	sql, _ := buildBulkUpsertQuery("posts", records, []string{"slug"}, "merge", false)
	if !strings.Contains(sql, "ON CONFLICT (slug)") {
		t.Errorf("expected conflict on slug, got: %s", sql)
	}
	if !strings.Contains(sql, "title = EXCLUDED.title") {
		t.Errorf("expected title updated, got: %s", sql)
	}
	if strings.Contains(sql, "slug = EXCLUDED.slug") {
		t.Errorf("should not self-assign conflict col: %s", sql)
	}
}
