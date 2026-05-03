package http

import (
	"strings"
	"testing"

	"github.com/saedx1/ultrabase/internal/domain"
)

func TestParseSelectItem(t *testing.T) {
	tests := []struct {
		in    string
		alias string
		col   string
		cast  string
	}{
		{"title", "", "title", ""},
		{"*", "", "*", ""},
		{"nick:name", "nick", "name", ""},
		{"age::text", "", "age", "text"},
		{"label:age::text", "label", "age", "text"},
		{"metadata->>theme", "", "metadata->>theme", ""},
		{"theme:metadata->>theme", "theme", "metadata->>theme", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got := parseSelectItem(tt.in)
			if got.Alias != tt.alias || got.Col != tt.col || got.Cast != tt.cast {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func TestValidateSelectItem(t *testing.T) {
	table := testTable()
	cases := []struct {
		name    string
		item    SelectItem
		wantErr bool
	}{
		{"known col", SelectItem{Col: "title"}, false},
		{"star", SelectItem{Col: "*"}, false},
		{"aliased known", SelectItem{Alias: "t", Col: "title"}, false},
		{"cast known", SelectItem{Col: "priority", Cast: "text"}, false},
		{"alias + cast", SelectItem{Alias: "p", Col: "priority", Cast: "text"}, false},
		{"jsonb", SelectItem{Col: "metadata->>theme"}, false},

		{"unknown col", SelectItem{Col: "bogus"}, true},
		{"bad alias", SelectItem{Alias: "a b", Col: "title"}, true},
		{"alias sqli", SelectItem{Alias: "x;DROP", Col: "title"}, true},
		{"bad cast", SelectItem{Col: "priority", Cast: "text; DROP"}, true},
		{"alias on star", SelectItem{Alias: "x", Col: "*"}, true},
		{"cast on star", SelectItem{Col: "*", Cast: "text"}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSelectItem(table, tt.item)
			if tt.wantErr && err == nil {
				t.Errorf("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRenderSelectItem(t *testing.T) {
	tests := []struct {
		name string
		item SelectItem
		want string
	}{
		{"plain col", SelectItem{Col: "title"}, "todos.title"},
		{"star", SelectItem{Col: "*"}, "todos.*"},
		{"alias", SelectItem{Alias: "t", Col: "title"}, "todos.title AS t"},
		{"cast", SelectItem{Col: "priority", Cast: "text"}, "(todos.priority)::text"},
		{"alias + cast", SelectItem{Alias: "p", Col: "priority", Cast: "text"}, "(todos.priority)::text AS p"},
		{"jsonb", SelectItem{Col: "metadata->>theme"}, "todos.metadata->>'theme'"},
		{"jsonb aliased", SelectItem{Alias: "theme", Col: "metadata->>theme"}, "todos.metadata->>'theme' AS theme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderSelectItem("todos", tt.item)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildSelectQuery_WithAliasAndCast(t *testing.T) {
	table := testTable()
	qp := &QueryParams{
		Select: []string{"id", "label:title", "p:priority::text"},
		Limit:  20,
	}
	sql, _ := buildSelectQuery("todos", qp, table)
	if !strings.Contains(sql, "todos.title AS label") {
		t.Errorf("missing alias: %s", sql)
	}
	if !strings.Contains(sql, "(todos.priority)::text AS p") {
		t.Errorf("missing cast+alias: %s", sql)
	}
}

func TestBuildSelectQuery_WithJSONBInSelect(t *testing.T) {
	table := testTable()
	qp := &QueryParams{
		Select: []string{"id", "theme:metadata->>theme"},
		Limit:  20,
	}
	sql, _ := buildSelectQuery("todos", qp, table)
	if !strings.Contains(sql, "todos.metadata->>'theme' AS theme") {
		t.Errorf("SQL = %q", sql)
	}
}

func TestParseQueryParams_RejectsBadAlias(t *testing.T) {
	table := testTable()
	c := testContext("select=id,bad%20alias:title")
	if _, err := parseQueryParams(c, "todos", table, nil); err == nil {
		t.Error("expected rejection for alias with space")
	}
}

func TestParseQueryParams_RejectsBadCast(t *testing.T) {
	table := testTable()
	c := testContext("select=id,priority::text%3BDROP")
	if _, err := parseQueryParams(c, "todos", table, nil); err == nil {
		t.Error("expected rejection for cast with semicolon")
	}
}

func TestParseEmbedHint(t *testing.T) {
	cases := []struct {
		in     string
		name   string
		inner  bool
		fkHint string
	}{
		{"author", "author", false, ""},
		{"author!inner", "author", true, ""},
		{"author!left", "author", false, ""},
		{"tasks!assignee", "tasks", false, "assignee"},
		{"tasks!assignee!inner", "tasks", true, "assignee"},
		{"tasks!inner!assignee", "tasks", true, "assignee"},
	}
	for _, tc := range cases {
		name, inner, fk := parseEmbedHint(tc.in)
		if name != tc.name || inner != tc.inner || fk != tc.fkHint {
			t.Errorf("%q → (%q, %v, %q), want (%q, %v, %q)", tc.in, name, inner, fk, tc.name, tc.inner, tc.fkHint)
		}
	}
}

// TestResolveEmbeds_LeftHint asserts `!left` resolves to Inner=false
// (the LEFT JOIN default). This is a no-op that nevertheless needs a
// regression test so future refactors of parseEmbedHint can't silently
// break clients that explicitly request LEFT.
// TestParseSelectItem_Aggregate covers the PostgREST-style aggregate
// suffix: `col.count()`, aliased `total:id.count()`, bare `count()`,
// cast `id.sum()::text`, and all five supported aggregate names. The
// parser must isolate the Agg field and leave Col empty for bare count().
func TestParseSelectItem_Aggregate(t *testing.T) {
	cases := []struct {
		in    string
		alias string
		col   string
		cast  string
		agg   string
	}{
		{"id.count()", "", "id", "", "count"},
		{"total:id.count()", "total", "id", "", "count"},
		{"hours.sum()", "", "hours", "", "sum"},
		{"score.avg()", "", "score", "", "avg"},
		{"price.min()", "", "price", "", "min"},
		{"price.max()", "", "price", "", "max"},
		{"count()", "", "", "", "count"},
		{"n:count()", "n", "", "", "count"},
		{"id.sum()::text", "", "id", "text", "sum"},
	}
	for _, tt := range cases {
		t.Run(tt.in, func(t *testing.T) {
			got := parseSelectItem(tt.in)
			if got.Alias != tt.alias || got.Col != tt.col || got.Cast != tt.cast || got.Agg != tt.agg {
				t.Errorf("got %+v", got)
			}
		})
	}
}

// TestRenderSelectItem_Aggregate asserts SQL emission for each aggregate
// shape: column form → AGGNAME(tbl.col) AS agg; bare count() → COUNT(*) AS
// count; explicit alias wins over the default; cast wraps the whole
// aggregate expression.
func TestRenderSelectItem_Aggregate(t *testing.T) {
	tests := []struct {
		name string
		item SelectItem
		want string
	}{
		{"count col", SelectItem{Col: "id", Agg: "count"}, "COUNT(todos.id) AS count"},
		{"count col aliased", SelectItem{Alias: "total", Col: "id", Agg: "count"}, "COUNT(todos.id) AS total"},
		{"bare count", SelectItem{Agg: "count"}, "COUNT(*) AS count"},
		{"sum", SelectItem{Col: "hours", Agg: "sum"}, "SUM(todos.hours) AS sum"},
		{"avg", SelectItem{Col: "score", Agg: "avg"}, "AVG(todos.score) AS avg"},
		{"min", SelectItem{Col: "price", Agg: "min"}, "MIN(todos.price) AS min"},
		{"max", SelectItem{Col: "price", Agg: "max"}, "MAX(todos.price) AS max"},
		{"cast", SelectItem{Col: "id", Agg: "sum", Cast: "text"}, "(SUM(todos.id))::text AS sum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderSelectItem("todos", tt.item)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIsAggSelectEntry ensures aggregate entries are distinguished from
// embeds by the select parser. Both shapes contain parentheses, so a false
// negative here sends aggregates into the embed parser (which tries to
// resolve an FK and 400s), and a false positive leaks embed syntax through
// into validateSelectItem.
func TestIsAggSelectEntry(t *testing.T) {
	cases := map[string]bool{
		"id.count()":       true,
		"count()":          true,
		"total:id.sum()":   true,
		"id.sum()::text":   true,
		"author(*)":        false,
		"author!inner(id)": false,
		"title":            false,
		"metadata->>theme": false,
	}
	for in, want := range cases {
		if got := isAggSelectEntry(in); got != want {
			t.Errorf("%q → %v, want %v", in, got, want)
		}
	}
}

// TestBuildSelectQuery_AggregateWithGroupBy combines a plain column with
// an aggregate; GROUP BY must be auto-added over the non-aggregate column.
func TestBuildSelectQuery_AggregateWithGroupBy(t *testing.T) {
	table := testTable()
	qp := &QueryParams{
		Select: []string{"status", "id.count()"},
		Limit:  20,
	}
	sql, _ := buildSelectQuery("todos", qp, table)
	if !strings.Contains(sql, "COUNT(todos.id) AS count") {
		t.Errorf("missing count: %s", sql)
	}
	if !strings.Contains(sql, "GROUP BY todos.status") {
		t.Errorf("missing GROUP BY: %s", sql)
	}
}

// TestBuildSelectQuery_BareCountNoGroupBy documents that a pure aggregate
// query (no plain columns) must not emit GROUP BY — Postgres would reject
// it as empty.
func TestBuildSelectQuery_BareCountNoGroupBy(t *testing.T) {
	table := testTable()
	qp := &QueryParams{
		Select: []string{"count()"},
		Limit:  20,
	}
	sql, _ := buildSelectQuery("todos", qp, table)
	if !strings.Contains(sql, "COUNT(*) AS count") {
		t.Errorf("missing COUNT(*): %s", sql)
	}
	if strings.Contains(sql, "GROUP BY") {
		t.Errorf("bare count() should not emit GROUP BY: %s", sql)
	}
}

func TestResolveEmbeds_LeftHint(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "author_id", ForeignKey: &domain.ForeignKey{References: "users.id"}},
		},
	}
	allTables := map[string]domain.Table{
		"todos": table,
		"users": {Fields: []domain.Field{{Name: "id"}, {Name: "name"}}},
	}
	embeds, err := resolveEmbeds("todos", table, []string{"author!left(id,name)"}, allTables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embeds) != 1 || embeds[0].Inner {
		t.Errorf("!left should produce Inner=false, got %+v", embeds)
	}
	qp := &QueryParams{
		Select: []string{"id", "author!left(id,name)"},
		Embeds: embeds,
	}
	sql, _ := buildSelectQuery("todos", qp, table)
	if strings.Contains(sql, "INNER JOIN") {
		t.Errorf("!left must not emit INNER JOIN, got: %s", sql)
	}
}

func TestBuildSelectQuery_InnerJoinEmbed(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text"},
			{Name: "author_id", ForeignKey: &domain.ForeignKey{References: "users.id"}},
		},
	}
	qp := &QueryParams{
		Select: []string{"id", "author!inner(id,name)"},
		Embeds: []Embed{
			{
				Name: "author", Columns: []string{"id", "name"},
				FKColumn: "author_id", RefTable: "users", RefColumn: "id",
				Inner: true,
			},
		},
		Limit: 20,
	}
	sql, _ := buildSelectQuery("todos", qp, table)
	if !strings.Contains(sql, "INNER JOIN users AS _emb_author") {
		t.Errorf("expected INNER JOIN, got: %s", sql)
	}
}

func TestResolveEmbeds_InnerHint(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "author_id", ForeignKey: &domain.ForeignKey{References: "users.id"}},
		},
	}
	allTables := map[string]domain.Table{
		"todos": table,
		"users": {Fields: []domain.Field{{Name: "id"}, {Name: "name"}}},
	}
	embeds, err := resolveEmbeds("todos", table, []string{"author!inner(id,name)"}, allTables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embeds) != 1 || !embeds[0].Inner {
		t.Errorf("expected inner embed, got %+v", embeds)
	}
}
