package http

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/adapter/http/postgrest"
)

func TestParseWhere_NotIn(t *testing.T) {
	table := testTable()
	// URL-encoded: status=not.in.(a,b,c)
	c := testContext("status=not.in.%28a%2Cb%2Cc%29")
	w, err := postgrest.ParseWhere(c.Request.URL.Query(), "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w == nil || len(w.Children) != 1 {
		t.Fatalf("expected 1 child, got %+v", w)
	}
	node := w.Children[0]
	if !node.Not {
		t.Errorf("expected Not=true, got %+v", node)
	}
	if node.Leaf == nil || node.Leaf.Operator != "in" {
		t.Errorf("leaf = %+v", node.Leaf)
	}
	sql, args, _ := w.BuildSQL(1)
	if !strings.Contains(sql, "NOT (status IN ($1, $2, $3))") {
		t.Errorf("SQL = %q", sql)
	}
	if len(args) != 3 || args[0] != "a" || args[1] != "b" || args[2] != "c" {
		t.Errorf("args = %v", args)
	}
}

func TestParseWhere_NotInInsideLogicList(t *testing.T) {
	table := testTable()
	c := testContext("or=%28status.not.in.%28a%2Cb%29%2Cpriority.eq.1%29")
	w, err := postgrest.ParseWhere(c.Request.URL.Query(), "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql, _, _ := w.BuildSQL(1)
	if !strings.Contains(sql, "NOT (status IN") {
		t.Errorf("expected NOT IN inside OR, got: %s", sql)
	}
}
