package http

import (
	"strings"
	"testing"
)

func TestParseQueryParams_OrderNullsFirst(t *testing.T) {
	table := testTable()
	c := testContext("order=title.asc.nullsfirst")
	qp, err := parseQueryParams(c, "todos", table, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(qp.Order) != 1 {
		t.Fatalf("got %d order, want 1", len(qp.Order))
	}
	o := qp.Order[0]
	if o.Column != "title" || o.Desc || o.Nulls != "first" {
		t.Errorf("order = %+v", o)
	}
}

func TestParseQueryParams_OrderNullsLast(t *testing.T) {
	table := testTable()
	c := testContext("order=priority.desc.nullslast")
	qp, err := parseQueryParams(c, "todos", table, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	o := qp.Order[0]
	if o.Column != "priority" || !o.Desc || o.Nulls != "last" {
		t.Errorf("order = %+v", o)
	}
}

func TestParseQueryParams_OrderNullsOnly(t *testing.T) {
	table := testTable()
	c := testContext("order=priority.nullsfirst")
	qp, err := parseQueryParams(c, "todos", table, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	o := qp.Order[0]
	if o.Column != "priority" || o.Desc || o.Nulls != "first" {
		t.Errorf("order = %+v", o)
	}
}

func TestBuildSelectQuery_OrderNullsFirst(t *testing.T) {
	table := testTable()
	qp := &QueryParams{
		Order: []OrderClause{
			{Column: "priority", Desc: true, Nulls: "last"},
			{Column: "title", Nulls: "first"},
		},
		Limit: 20,
	}
	sql, _ := buildSelectQuery("todos", qp, table)
	if !strings.Contains(sql, "priority DESC NULLS LAST") {
		t.Errorf("missing NULLS LAST: %s", sql)
	}
	if !strings.Contains(sql, "title ASC NULLS FIRST") {
		t.Errorf("missing NULLS FIRST: %s", sql)
	}
}

func TestParseQueryParams_OrderMultipleWithNulls(t *testing.T) {
	table := testTable()
	c := testContext("order=priority.desc.nullslast,title.asc")
	qp, err := parseQueryParams(c, "todos", table, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(qp.Order) != 2 {
		t.Fatalf("got %d, want 2", len(qp.Order))
	}
	if qp.Order[0].Nulls != "last" || qp.Order[1].Nulls != "" {
		t.Errorf("order = %+v", qp.Order)
	}
}

func TestParseQueryParams_OrderRejectsUnknownColAfterStrip(t *testing.T) {
	table := testTable()
	c := testContext("order=bogus.desc.nullsfirst")
	if _, err := parseQueryParams(c, "todos", table, nil); err == nil {
		t.Error("expected unknown-column rejection")
	}
}
