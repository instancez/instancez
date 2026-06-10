package http

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestContentTypeIsCSV(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"text/csv", true},
		{"text/csv; charset=utf-8", true},
		{"TEXT/CSV", true},
		{"application/json", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := contentTypeIsCSV(tc.in); got != tc.ok {
			t.Errorf("contentTypeIsCSV(%q) = %v, want %v", tc.in, got, tc.ok)
		}
	}
}

func TestAcceptsCSV(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"text/csv", true},
		{"application/json, text/csv;q=0.5", true},
		{"application/json", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := acceptsCSV(tc.in); got != tc.ok {
			t.Errorf("acceptsCSV(%q) = %v, want %v", tc.in, got, tc.ok)
		}
	}
}

func TestCsvReadRecords_Basic(t *testing.T) {
	body := "title,status\na,active\nb,done\n"
	recs, err := csvReadRecords([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d rows, want 2", len(recs))
	}
	if recs[0]["title"] != "a" || recs[0]["status"] != "active" {
		t.Errorf("recs[0] = %+v", recs[0])
	}
	if recs[1]["title"] != "b" || recs[1]["status"] != "done" {
		t.Errorf("recs[1] = %+v", recs[1])
	}
}

func TestCsvReadRecords_EmptyCellBecomesEmptyString(t *testing.T) {
	body := "title,status\na,\n"
	recs, err := csvReadRecords([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if recs[0]["status"] != "" {
		t.Errorf("expected empty string, got %+v", recs[0]["status"])
	}
}

func TestCsvReadRecords_NoHeaderRejected(t *testing.T) {
	if _, err := csvReadRecords([]byte("")); err == nil {
		t.Error("expected error for empty CSV")
	}
}

func TestCsvRenderRows_HeaderSortedAndComplete(t *testing.T) {
	rows := []map[string]any{
		{"title": "a", "status": "active"},
		{"title": "b", "priority": 5},
	}
	out, err := csvRenderRows(rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header+2): %q", len(lines), s)
	}
	// columns are union across rows, sorted: priority, status, title
	if lines[0] != "priority,status,title" {
		t.Errorf("header = %q", lines[0])
	}
	if lines[1] != ",active,a" {
		t.Errorf("row0 = %q", lines[1])
	}
	if lines[2] != "5,,b" {
		t.Errorf("row1 = %q", lines[2])
	}
}

func TestCsvRenderRows_EscapesCommasAndQuotes(t *testing.T) {
	rows := []map[string]any{
		{"title": `a,b`, "note": `he said "hi"`},
	}
	out, err := csvRenderRows(rows)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, `"a,b"`) {
		t.Errorf("expected quoted comma value: %s", s)
	}
	if !strings.Contains(s, `"he said ""hi"""`) {
		t.Errorf("expected escaped quotes: %s", s)
	}
}

func TestCsvRenderRows_EmptyReturnsOnlyHeader(t *testing.T) {
	out, err := csvRenderRows(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "" {
		t.Errorf("empty input should yield empty output, got %q", out)
	}
}

func TestCoerceCSVValue(t *testing.T) {
	tests := []struct {
		val     string
		pgType  string
		want    any
		changed bool
	}{
		// Integers
		{"42", "integer", int64(42), true},
		{"42", "int4", int64(42), true},
		{"42", "bigint", int64(42), true},
		{"42", "smallint", int64(42), true},
		{"42", "serial", int64(42), true},

		// Floats
		{"3.14", "real", float64(float32(3.14)), true},
		{"3.14", "double precision", 3.14, true},
		{"3.14", "float8", 3.14, true},

		// Numeric stays as string (arbitrary precision)
		{"99999.99", "numeric", "99999.99", false},
		{"99999.99", "numeric(10,2)", "99999.99", false},

		// Booleans
		{"true", "boolean", true, true},
		{"false", "bool", false, true},
		{"t", "boolean", true, true},
		{"f", "boolean", false, true},
		{"yes", "boolean", true, true},
		{"no", "boolean", false, true},
		{"1", "boolean", true, true},
		{"0", "boolean", false, true},

		// Text types stay as-is
		{"hello", "text", "hello", false},
		{"hello", "varchar(255)", "hello", false},
		{"hello", "citext", "hello", false},

		// Empty string → nil for non-text
		{"", "integer", nil, true},
		{"", "boolean", nil, true},
		{"", "uuid", nil, true},
		// Empty string stays for text types
		{"", "text", "", false},
		{"", "varchar", "", false},

		// UUID, date, timestamp — pass through as string
		{"550e8400-e29b-41d4-a716-446655440000", "uuid", "550e8400-e29b-41d4-a716-446655440000", false},
		{"2024-01-15", "date", "2024-01-15", false},
		{"2024-01-15T10:30:00Z", "timestamptz", "2024-01-15T10:30:00Z", false},

		// JSON validation
		{`{"key":"val"}`, "jsonb", `{"key":"val"}`, false},
		{`not json`, "jsonb", `not json`, false}, // invalid JSON passes through

		// Invalid numbers stay as string
		{"abc", "integer", "abc", false},
	}
	for _, tc := range tests {
		got, changed := coerceCSVValue(tc.val, tc.pgType)
		if changed != tc.changed {
			t.Errorf("coerceCSVValue(%q, %q) changed=%v, want %v", tc.val, tc.pgType, changed, tc.changed)
			continue
		}
		if changed && got != tc.want {
			t.Errorf("coerceCSVValue(%q, %q) = %v (%T), want %v (%T)", tc.val, tc.pgType, got, got, tc.want, tc.want)
		}
	}
}

func TestCsvCoerceRecords(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "integer"},
			{Name: "name", Type: "text"},
			{Name: "active", Type: "boolean"},
			{Name: "score", Type: "float8"},
		},
	}
	records := []map[string]any{
		{"id": "1", "name": "Alice", "active": "true", "score": "9.5"},
		{"id": "2", "name": "Bob", "active": "false", "score": ""},
	}
	out := csvCoerceRecords(records, table)
	if out[0]["id"] != int64(1) {
		t.Errorf("id = %v (%T), want int64(1)", out[0]["id"], out[0]["id"])
	}
	if out[0]["name"] != "Alice" {
		t.Errorf("name = %v", out[0]["name"])
	}
	if out[0]["active"] != true {
		t.Errorf("active = %v", out[0]["active"])
	}
	if out[0]["score"] != 9.5 {
		t.Errorf("score = %v", out[0]["score"])
	}
	if out[1]["score"] != nil {
		t.Errorf("empty float should be nil, got %v", out[1]["score"])
	}
}
