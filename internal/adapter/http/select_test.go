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
		in    string
		name  string
		inner bool
	}{
		{"author", "author", false},
		{"author!inner", "author", true},
		{"author!left", "author", false},
	}
	for _, tc := range cases {
		name, inner := parseEmbedHint(tc.in)
		if name != tc.name || inner != tc.inner {
			t.Errorf("%q → (%q, %v), want (%q, %v)", tc.in, name, inner, tc.name, tc.inner)
		}
	}
}

func TestBuildSelectQuery_InnerJoinEmbed(t *testing.T) {
	table := domain.Table{
		Fields: map[string]domain.Field{
			"id":        {Type: "bigserial", PrimaryKey: true},
			"title":     {Type: "text"},
			"author_id": {ForeignKey: &domain.ForeignKey{References: "users.id"}},
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
		Fields: map[string]domain.Field{
			"id":        {Type: "bigserial", PrimaryKey: true},
			"author_id": {ForeignKey: &domain.ForeignKey{References: "users.id"}},
		},
	}
	allTables := map[string]domain.Table{
		"todos": table,
		"users": {Fields: map[string]domain.Field{"id": {}, "name": {}}},
	}
	embeds, err := resolveEmbeds("todos", table, []string{"author!inner(id,name)"}, allTables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(embeds) != 1 || !embeds[0].Inner {
		t.Errorf("expected inner embed, got %+v", embeds)
	}
}
