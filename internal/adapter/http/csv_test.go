package http

import (
	"strings"
	"testing"
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
