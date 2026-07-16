package http

import "testing"

func TestParseColumnsParam_Empty(t *testing.T) {
	cols, err := parseColumnsParam("", testTable())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cols != nil {
		t.Errorf("expected nil, got %v", cols)
	}
}

func TestParseColumnsParam_Single(t *testing.T) {
	cols, err := parseColumnsParam("title", testTable())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 1 || !cols["title"] {
		t.Errorf("got %v", cols)
	}
}

func TestParseColumnsParam_Multiple(t *testing.T) {
	cols, err := parseColumnsParam("title,status,priority", testTable())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 3 || !cols["title"] || !cols["status"] || !cols["priority"] {
		t.Errorf("got %v", cols)
	}
}

func TestParseColumnsParam_RejectsUnknown(t *testing.T) {
	if _, err := parseColumnsParam("title,bogus", testTable()); err == nil {
		t.Error("expected rejection for unknown column")
	}
}

func TestFilterRecordsByColumns_DropsExtras(t *testing.T) {
	records := []map[string]any{
		{"title": "a", "status": "active", "extra": "x"},
		{"title": "b", "status": "done"},
	}
	cols := map[string]bool{"title": true, "status": true}
	out := filterRecordsByColumns(records, cols)
	if len(out) != 2 {
		t.Fatalf("got %d, want 2", len(out))
	}
	if _, ok := out[0]["extra"]; ok {
		t.Errorf("extra should be dropped: %+v", out[0])
	}
	if out[0]["title"] != "a" || out[0]["status"] != "active" {
		t.Errorf("out[0] = %+v", out[0])
	}
}

func TestFilterRecordsByColumns_Nil(t *testing.T) {
	records := []map[string]any{{"title": "a"}}
	if out := filterRecordsByColumns(records, nil); len(out) != 1 || out[0]["title"] != "a" {
		t.Errorf("nil cols should pass records through unchanged: %+v", out)
	}
}
