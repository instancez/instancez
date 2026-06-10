package http

import (
	"strings"
	"testing"

	"github.com/saedx1/instancez/internal/domain"
)

func TestParseWhere_SimpleLeaf(t *testing.T) {
	table := testTable()
	c := testContext("status=eq.active")
	w, err := parseWhere(c, "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w == nil || w.Op != "and" || len(w.Children) != 1 {
		t.Fatalf("root = %+v", w)
	}
	leaf := w.Children[0]
	if leaf.Leaf == nil || leaf.Leaf.Column != "status" || leaf.Leaf.Operator != "eq" || leaf.Leaf.Value != "active" {
		t.Errorf("leaf = %+v", leaf)
	}
	if leaf.Not {
		t.Error("leaf unexpectedly negated")
	}
}

func TestParseWhere_NegatedLeaf(t *testing.T) {
	table := testTable()
	c := testContext("status=not.eq.done")
	w, err := parseWhere(c, "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	leaf := w.Children[0]
	if !leaf.Not {
		t.Error("expected Not=true")
	}
	if leaf.Leaf.Operator != "eq" || leaf.Leaf.Value != "done" {
		t.Errorf("leaf payload = %+v", leaf.Leaf)
	}

	sql, _, _ := w.buildSQL(1)
	if !strings.Contains(sql, "NOT (status = $1)") {
		t.Errorf("SQL = %q, want NOT (status = $1)", sql)
	}
}

func TestParseWhere_OrAtTopLevel(t *testing.T) {
	table := testTable()
	c := testContext("or=(status.eq.active,priority.gte.3)")
	w, err := parseWhere(c, "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(w.Children) != 1 {
		t.Fatalf("root children = %d, want 1", len(w.Children))
	}
	or := w.Children[0]
	if or.Op != "or" || len(or.Children) != 2 {
		t.Fatalf("or node = %+v", or)
	}
	sql, args, _ := w.buildSQL(1)
	want := "(status = $1 OR priority >= $2)"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 2 {
		t.Errorf("args = %d, want 2", len(args))
	}
}

func TestParseWhere_AndAtTopLevel(t *testing.T) {
	table := testTable()
	c := testContext("and=(status.eq.active,priority.gte.3)")
	w, err := parseWhere(c, "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql, _, _ := w.buildSQL(1)
	if !strings.Contains(sql, "status = $1") || !strings.Contains(sql, "priority >= $2") {
		t.Errorf("SQL = %q", sql)
	}
	if !strings.Contains(sql, " AND ") {
		t.Errorf("expected AND separator, got %q", sql)
	}
}

func TestParseWhere_NestedOrInsideAnd(t *testing.T) {
	table := testTable()
	c := testContext("and=(status.eq.active,or(priority.gte.3,priority.lt.0))")
	w, err := parseWhere(c, "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql, args, _ := w.buildSQL(1)
	want := "(status = $1 AND (priority >= $2 OR priority < $3))"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 3 {
		t.Errorf("args = %d, want 3", len(args))
	}
}

func TestParseWhere_TopLevelMixed(t *testing.T) {
	table := testTable()
	c := testContext("status=eq.active&or=(priority.lt.1,priority.gt.9)")
	w, err := parseWhere(c, "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql, args, _ := w.buildSQL(1)
	// Root is AND over both children. Child order is map-iteration-dependent,
	// so accept either ordering.
	ok := sql == "(status = $1 AND (priority < $2 OR priority > $3))" ||
		sql == "((priority < $1 OR priority > $2) AND status = $3)"
	if !ok {
		t.Errorf("SQL = %q", sql)
	}
	if len(args) != 3 {
		t.Errorf("args = %d, want 3", len(args))
	}
}

func TestParseWhere_LogicListWithNegatedLeaf(t *testing.T) {
	table := testTable()
	c := testContext("or=(status.not.eq.done,priority.eq.5)")
	w, err := parseWhere(c, "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql, _, _ := w.buildSQL(1)
	if !strings.Contains(sql, "NOT (status = $1)") {
		t.Errorf("SQL = %q, want NOT leaf inside", sql)
	}
	if !strings.Contains(sql, "OR priority = $2") {
		t.Errorf("SQL = %q", sql)
	}
}

func TestParseWhere_NotOnNestedGroup(t *testing.T) {
	table := testTable()
	c := testContext("and=(status.eq.active,not.or(priority.lt.1,priority.gt.9))")
	w, err := parseWhere(c, "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql, _, _ := w.buildSQL(1)
	if !strings.Contains(sql, "NOT (priority < $2 OR priority > $3)") {
		t.Errorf("SQL = %q", sql)
	}
}

func TestParseWhere_UnbalancedParens(t *testing.T) {
	table := testTable()
	c := testContext("or=(status.eq.a,priority.eq.1")
	if _, err := parseWhere(c, "todos", table); err == nil {
		t.Error("expected error for unbalanced parens")
	}
}

func TestParseWhere_UnknownColumnInsideLogicList(t *testing.T) {
	table := testTable()
	c := testContext("or=(bogus.eq.1,status.eq.x)")
	if _, err := parseWhere(c, "todos", table); err == nil {
		t.Error("expected rejection for unknown column inside logic list")
	}
}

func TestParseWhere_JSONBInsideLogicList(t *testing.T) {
	table := testTable()
	// metadata->>theme URL-encoded inside an or() list
	c := testContext("or=(metadata-%3E%3Etheme.eq.dark,status.eq.x)")
	w, err := parseWhere(c, "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql, _, _ := w.buildSQL(1)
	if !strings.Contains(sql, "metadata->>'theme' = $1") {
		t.Errorf("SQL = %q", sql)
	}
}

func TestParseWhere_Empty(t *testing.T) {
	table := testTable()
	c := testContext("")
	w, err := parseWhere(c, "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w != nil {
		t.Errorf("expected nil tree, got %+v", w)
	}
	sql, _, _ := w.buildSQL(1)
	if sql != "" {
		t.Errorf("buildSQL on nil = %q, want empty", sql)
	}
}

func TestWhereNodeBuildSQL_ArgIndexing(t *testing.T) {
	// An UPDATE path starts its WHERE args after the SET placeholders.
	w := andLeaves(
		Filter{Column: "status", Operator: "eq", Value: "active"},
		Filter{Column: "priority", Operator: "gt", Value: "3"},
	)
	sql, args, next := w.buildSQL(5)
	want := "(status = $5 AND priority > $6)"
	if sql != want {
		t.Errorf("SQL = %q, want %q", sql, want)
	}
	if len(args) != 2 {
		t.Errorf("args = %d, want 2", len(args))
	}
	if next != 7 {
		t.Errorf("next = %d, want 7", next)
	}
}

func TestSplitTopLevel(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{"a,and(b,c),d", []string{"a", "and(b,c)", "d"}},
		{"or(a,and(b,c)),d", []string{"or(a,and(b,c))", "d"}},
		{"", []string{""}},
	}
	for _, tc := range cases {
		got, err := splitTopLevel(tc.in, ',')
		if err != nil {
			t.Errorf("%q: unexpected error %v", tc.in, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q: got %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}

	if _, err := splitTopLevel("a,(b,c", ','); err == nil {
		t.Error("expected unbalanced-parens error")
	}
}

// FTS operators (fts/plfts/phfts/wfts) require a text-like or tsvector
// column. An int/jsonb/unknown column must be rejected at parse time so
// the handler can surface PGRST100 rather than sending malformed SQL to
// Postgres and returning a raw SQLSTATE.
func TestParseWhere_FTSColumnTypeValidation(t *testing.T) {
	tableWithTSVector := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text"},
			{Name: "priority", Type: "int"},
			{Name: "metadata", Type: "jsonb"},
			{Name: "doc", Type: "tsvector"},
			{Name: "short", Type: "varchar(255)"},
		},
	}
	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{"text column with fts accepted", "title=fts.cats", false},
		{"text column with plfts accepted", "title=plfts.cats", false},
		{"text column with phfts accepted", "title=phfts.happy+dog", false},
		{"text column with wfts accepted", "title=wfts.cat", false},
		{"tsvector column accepted", "doc=fts.cats", false},
		{"varchar column accepted", "short=fts.cats", false},
		{"int column rejected", "priority=fts.1", true},
		{"jsonb base column rejected", "metadata=fts.cats", true},
		{"jsonb ->> text path accepted", "metadata->>theme=fts.dark", false},
		{"jsonb -> path rejected", "metadata->nested=fts.dark", true},
		{"fts inside or() rejected on int", "or=(title.fts.cats,priority.fts.cats)", true},
		{"fts inside or() accepted when both text-ish", "or=(title.fts.cats,doc.fts.cats)", false},
		{"non-fts op on int still accepted", "priority=eq.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := testContext(tt.query)
			_, err := parseWhere(c, "todos", tableWithTSVector)
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsFTSCompatibleType(t *testing.T) {
	ok := []string{"text", "citext", "tsvector", "varchar", "varchar(255)",
		"character varying", "char(10)", "character(5)", "bpchar", "TEXT", " text "}
	for _, s := range ok {
		if !isFTSCompatibleType(s) {
			t.Errorf("%q: want compatible", s)
		}
	}
	bad := []string{"int", "bigint", "jsonb", "jsonb[]", "text[]", "boolean", "", "tsquery"}
	for _, s := range bad {
		if isFTSCompatibleType(s) {
			t.Errorf("%q: want incompatible", s)
		}
	}
}
