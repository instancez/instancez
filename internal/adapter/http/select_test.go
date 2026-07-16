package http

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/adapter/http/postgrest"
	"github.com/instancez/instancez/internal/domain"
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
			got := postgrest.ParseSelectItem(tt.in)
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
		item    postgrest.SelectItem
		wantErr bool
	}{
		{"known col", postgrest.SelectItem{Col: "title"}, false},
		{"star", postgrest.SelectItem{Col: "*"}, false},
		{"aliased known", postgrest.SelectItem{Alias: "t", Col: "title"}, false},
		{"cast known", postgrest.SelectItem{Col: "priority", Cast: "text"}, false},
		{"alias + cast", postgrest.SelectItem{Alias: "p", Col: "priority", Cast: "text"}, false},
		{"jsonb", postgrest.SelectItem{Col: "metadata->>theme"}, false},

		{"unknown col", postgrest.SelectItem{Col: "bogus"}, true},
		{"bad alias", postgrest.SelectItem{Alias: "a b", Col: "title"}, true},
		{"alias sqli", postgrest.SelectItem{Alias: "x;DROP", Col: "title"}, true},
		{"bad cast", postgrest.SelectItem{Col: "priority", Cast: "text; DROP"}, true},
		{"alias on star", postgrest.SelectItem{Alias: "x", Col: "*"}, true},
		{"cast on star", postgrest.SelectItem{Col: "*", Cast: "text"}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := postgrest.ValidateSelectItem(table, tt.item)
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
		item postgrest.SelectItem
		want string
	}{
		{"plain col", postgrest.SelectItem{Col: "title"}, "todos.title"},
		{"star", postgrest.SelectItem{Col: "*"}, "todos.*"},
		{"alias", postgrest.SelectItem{Alias: "t", Col: "title"}, "todos.title AS t"},
		{"cast", postgrest.SelectItem{Col: "priority", Cast: "text"}, "(todos.priority)::text"},
		{"alias + cast", postgrest.SelectItem{Alias: "p", Col: "priority", Cast: "text"}, "(todos.priority)::text AS p"},
		{"jsonb", postgrest.SelectItem{Col: "metadata->>theme"}, "todos.metadata->>'theme'"},
		{"jsonb aliased", postgrest.SelectItem{Alias: "theme", Col: "metadata->>theme"}, "todos.metadata->>'theme' AS theme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := postgrest.RenderSelectItem("todos", tt.item)
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
		name, inner, fk := postgrest.ParseEmbedHint(tc.in)
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
			got := postgrest.ParseSelectItem(tt.in)
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
		item postgrest.SelectItem
		want string
	}{
		{"count col", postgrest.SelectItem{Col: "id", Agg: "count"}, "COUNT(todos.id) AS count"},
		{"count col aliased", postgrest.SelectItem{Alias: "total", Col: "id", Agg: "count"}, "COUNT(todos.id) AS total"},
		{"bare count", postgrest.SelectItem{Agg: "count"}, "COUNT(*) AS count"},
		{"sum", postgrest.SelectItem{Col: "hours", Agg: "sum"}, "SUM(todos.hours) AS sum"},
		{"avg", postgrest.SelectItem{Col: "score", Agg: "avg"}, "AVG(todos.score) AS avg"},
		{"min", postgrest.SelectItem{Col: "price", Agg: "min"}, "MIN(todos.price) AS min"},
		{"max", postgrest.SelectItem{Col: "price", Agg: "max"}, "MAX(todos.price) AS max"},
		{"cast", postgrest.SelectItem{Col: "id", Agg: "sum", Cast: "text"}, "(SUM(todos.id))::text AS sum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := postgrest.RenderSelectItem("todos", tt.item)
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
		if got := postgrest.IsAggSelectEntry(in); got != want {
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

// TestSplitEmbedAlias covers the PostgREST "alias:relation" prefix used to
// rename an embed in the response. A "::" sequence is a cast and must not be
// treated as an alias separator.
func TestSplitEmbedAlias(t *testing.T) {
	cases := []struct {
		in        string
		wantAlias string
		wantName  string
	}{
		{"author", "", "author"},
		{"category:categories", "category", "categories"},
		{"category:categories!left", "category", "categories!left"},
		{"category:categories!inner", "category", "categories!inner"},
		{"category:categories!fk_hint", "category", "categories!fk_hint"},
		{"price::numeric", "", "price::numeric"},
		{"a::b:c", "", "a::b:c"},
	}
	for _, tc := range cases {
		alias, name := postgrest.SplitEmbedAlias(tc.in)
		if alias != tc.wantAlias || name != tc.wantName {
			t.Errorf("%q → (%q, %q), want (%q, %q)", tc.in, alias, name, tc.wantAlias, tc.wantName)
		}
	}
}

// TestParseEmbedParam_Alias verifies the PostgREST "alias:relation" prefix is
// stripped from the relation name and surfaced via the returned alias slot.
// Combined with !left/!inner hints, the alias precedes the hint marker.
func TestParseEmbedParam_Alias(t *testing.T) {
	cases := []struct {
		in        string
		wantName  string
		wantAlias string
		wantCols  []string
	}{
		{"category:categories(id,slug,name)", "categories", "category", []string{"id", "slug", "name"}},
		{"category:categories!left(id,slug,name)", "categories!left", "category", []string{"id", "slug", "name"}},
		{"category:categories!inner(id,slug,name)", "categories!inner", "category", []string{"id", "slug", "name"}},
		{"author(id,name)", "author", "", []string{"id", "name"}},
	}
	for _, tc := range cases {
		name, alias, cols, _, _ := parseEmbedParam(tc.in)
		if name != tc.wantName || alias != tc.wantAlias {
			t.Errorf("%q → name=%q alias=%q, want name=%q alias=%q", tc.in, name, alias, tc.wantName, tc.wantAlias)
		}
		if len(cols) != len(tc.wantCols) {
			t.Fatalf("%q cols len = %d, want %d", tc.in, len(cols), len(tc.wantCols))
		}
		for i := range cols {
			if cols[i] != tc.wantCols[i] {
				t.Errorf("%q cols[%d] = %q, want %q", tc.in, i, cols[i], tc.wantCols[i])
			}
		}
	}
}

// TestResolveEmbeds_Alias asserts the PostgREST "alias:relation" prefix
// resolves the relationship by the post-colon name and surfaces the embed
// under the alias both as JSON output key and as the internal SQL alias.
// This is the regression test for the docs/examples/gearstore bug where
// "category:categories!left(...)" was rejected with "could not find a
// relationship between 'products' and 'category:categories'".
func TestResolveEmbeds_Alias(t *testing.T) {
	table := domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "name", Type: "text"},
			{Name: "category_id", ForeignKey: &domain.ForeignKey{References: "categories.id"}},
		},
	}
	allTables := map[string]domain.Table{
		"products":   table,
		"categories": {Fields: []domain.Field{{Name: "id"}, {Name: "slug"}, {Name: "name"}}},
	}

	t.Run("belongs-to with !left", func(t *testing.T) {
		embeds, err := resolveEmbeds("products", table, []string{"category:categories!left(id,slug,name)"}, allTables)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(embeds) != 1 {
			t.Fatalf("expected 1 embed, got %d", len(embeds))
		}
		e := embeds[0]
		if e.Name != "categories" || e.Alias != "category" {
			t.Errorf("Name=%q Alias=%q, want categories/category", e.Name, e.Alias)
		}
		if e.RefTable != "categories" || e.FKColumn != "category_id" {
			t.Errorf("RefTable=%q FKColumn=%q", e.RefTable, e.FKColumn)
		}
		if e.IsReverse || e.Inner {
			t.Errorf("expected belongs-to LEFT, got IsReverse=%v Inner=%v", e.IsReverse, e.Inner)
		}
		if e.OutputKey() != "category" {
			t.Errorf("outputKey = %q, want category", e.OutputKey())
		}
	})

	t.Run("belongs-to with !inner", func(t *testing.T) {
		embeds, err := resolveEmbeds("products", table, []string{"category:categories!inner(id)"}, allTables)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(embeds) != 1 || !embeds[0].Inner || embeds[0].Alias != "category" {
			t.Errorf("expected inner aliased embed, got %+v", embeds)
		}
	})

	t.Run("SQL emits alias as JSON key and SQL alias", func(t *testing.T) {
		embeds, err := resolveEmbeds("products", table, []string{"category:categories!left(id,name)"}, allTables)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		qp := &QueryParams{Select: []string{"id", "category:categories!left(id,name)"}, Embeds: embeds}
		sql, _ := buildSelectQuery("products", qp, table)
		// JSON output key must be the alias, not the table name.
		if !strings.Contains(sql, "AS category") {
			t.Errorf("expected `AS category` in select, got: %s", sql)
		}
		if strings.Contains(sql, "AS categories") {
			t.Errorf("must not emit `AS categories` (the relation name) when aliased: %s", sql)
		}
		// Internal SQL alias for the JOIN must be derived from the alias too,
		// so multiple embeds to the same table cannot collide.
		if !strings.Contains(sql, "LEFT JOIN categories AS _emb_category") {
			t.Errorf("expected JOIN alias `_emb_category`, got: %s", sql)
		}
	})

	t.Run("has-many with alias", func(t *testing.T) {
		// reviews → products via product_id; from products embed reviews.
		ptable := domain.Table{
			Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}},
		}
		all := map[string]domain.Table{
			"products": ptable,
			"reviews": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "rating", Type: "int"},
					{Name: "product_id", ForeignKey: &domain.ForeignKey{References: "products.id"}},
				},
			},
		}
		embeds, err := resolveEmbeds("products", ptable, []string{"feedback:reviews(id,rating)"}, all)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(embeds) != 1 || embeds[0].Name != "reviews" || embeds[0].Alias != "feedback" || !embeds[0].IsReverse {
			t.Errorf("expected aliased has-many, got %+v", embeds)
		}
		qp := &QueryParams{Select: []string{"id", "feedback:reviews(id,rating)"}, Embeds: embeds}
		sql, _ := buildSelectQuery("products", qp, ptable)
		if !strings.Contains(sql, "AS feedback") {
			t.Errorf("expected `AS feedback`, got: %s", sql)
		}
	})

	t.Run("alias on spread is rejected", func(t *testing.T) {
		_, err := resolveEmbeds("products", table, []string{"...cat:categories(name)"}, allTables)
		if err == nil {
			t.Error("expected error: alias on spread embed is meaningless")
		}
	})

	t.Run("two embeds to same table disambiguated by alias", func(t *testing.T) {
		// Build a schema where products has two FKs to categories so both
		// belongs-to embeds can resolve via FK hints. Without the alias-aware
		// SQL alias seed (`_emb_<outputKey>`), both embeds collide on
		// `_emb_categories` and the generated SQL is invalid.
		dual := domain.Table{
			Fields: []domain.Field{
				{Name: "id", Type: "bigserial", PrimaryKey: true},
				{Name: "primary_category_id", ForeignKey: &domain.ForeignKey{References: "categories.id"}},
				{Name: "secondary_category_id", ForeignKey: &domain.ForeignKey{References: "categories.id"}},
			},
		}
		all := map[string]domain.Table{
			"products":   dual,
			"categories": {Fields: []domain.Field{{Name: "id"}, {Name: "name"}}},
		}
		embeds, err := resolveEmbeds("products", dual,
			[]string{"primary:categories!primary_category(name)", "secondary:categories!secondary_category(name)"},
			all)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(embeds) != 2 {
			t.Fatalf("expected 2 embeds, got %d", len(embeds))
		}
		qp := &QueryParams{
			Select: []string{"id", "primary:categories!primary_category(name)", "secondary:categories!secondary_category(name)"},
			Embeds: embeds,
		}
		sql, _ := buildSelectQuery("products", qp, dual)
		if !strings.Contains(sql, "_emb_primary") || !strings.Contains(sql, "_emb_secondary") {
			t.Errorf("expected distinct join aliases _emb_primary and _emb_secondary, got: %s", sql)
		}
	})
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
