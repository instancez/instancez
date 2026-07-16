package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/adapter/http/postgrest"
	"github.com/instancez/instancez/internal/domain"
)

func testTable() domain.Table {
	return domain.Table{
		Fields: []domain.Field{
			{Name: "id", Type: "bigserial", PrimaryKey: true},
			{Name: "title", Type: "text"},
			{Name: "status", Type: "text"},
			{Name: "priority", Type: "int"},
			{Name: "metadata", Type: "jsonb"},
		},
	}
}

func testContext(rawQuery string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodGet, "/?"+rawQuery, nil)
	c.Request = req
	return c
}

func TestValidateColumn(t *testing.T) {
	table := testTable()
	tests := []struct {
		name    string
		col     string
		wantErr bool
	}{
		{"known simple column", "title", false},
		{"known int column", "priority", false},
		{"jsonb base column", "metadata", false},
		{"jsonb text path", "metadata->>theme", false},
		{"jsonb arrow path", "metadata->nested", false},
		{"jsonb path with underscore and digit", "metadata->>key_1", false},

		{"empty column", "", true},
		{"unknown column", "bogus", true},
		{"unknown jsonb base", "other->>foo", true},

		{"sqli semicolon", "id; DROP TABLE users", true},
		{"sqli comment", "title--comment", true},
		{"sqli union", "id UNION SELECT 1", true},
		{"sqli quoted", "id' OR '1'='1", true},

		{"jsonb key with quote", "metadata->>theme'; DROP--", true},
		{"jsonb key with space", "metadata->>my key", true},
		{"jsonb empty key", "metadata->>", true},
		{"jsonb key with dash", "metadata->>my-key", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateColumn(table, tt.col)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for %q, got nil", tt.col)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for %q: %v", tt.col, err)
			}
		})
	}
}

func TestParseFilters_RejectsUnknownColumn(t *testing.T) {
	table := testTable()
	c := testContext("bogus=eq.1")
	_, err := postgrest.ParseWhere(c.Request.URL.Query(), "todos", table)
	if err == nil {
		t.Fatal("expected error for unknown filter column")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention offending column, got: %v", err)
	}
}

func TestParseFilters_RejectsSQLInjectionColumn(t *testing.T) {
	cases := []string{
		"id%3B+DROP+TABLE+users=eq.1",
		"title--=eq.1",
	}
	table := testTable()
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			c := testContext(raw)
			if _, err := postgrest.ParseWhere(c.Request.URL.Query(), "todos", table); err == nil {
				t.Errorf("expected rejection for %q", raw)
			}
		})
	}
}

func TestParseFilters_RejectsJSONBKeyInjection(t *testing.T) {
	table := testTable()
	// metadata->>theme'; DROP-- — URL-encoded
	raw := "metadata-%3E%3Etheme%27%3B+DROP--=eq.dark"
	c := testContext(raw)
	if _, err := postgrest.ParseWhere(c.Request.URL.Query(), "todos", table); err == nil {
		t.Error("expected rejection for JSONB key with SQL injection")
	}
}

func TestParseFilters_AcceptsKnownColumn(t *testing.T) {
	table := testTable()
	c := testContext("status=eq.active&priority=gte.3")
	where, err := postgrest.ParseWhere(c.Request.URL.Query(), "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if where == nil || len(where.Children) != 2 {
		t.Errorf("expected 2 children, got %+v", where)
	}
}

func TestParseFilters_AcceptsJSONBPath(t *testing.T) {
	table := testTable()
	c := testContext("metadata-%3E%3Etheme=eq.dark")
	where, err := postgrest.ParseWhere(c.Request.URL.Query(), "todos", table)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if where == nil || len(where.Children) != 1 {
		t.Fatalf("expected 1 child, got %+v", where)
	}
	leaf := where.Children[0].Leaf
	if leaf == nil || leaf.Column != "metadata->>theme" {
		t.Errorf("leaf = %+v, want column metadata->>theme", leaf)
	}
}

func TestParseQueryParams_RejectsUnknownOrderColumn(t *testing.T) {
	table := testTable()
	c := testContext("order=bogus.desc")
	if _, err := parseQueryParams(c, "todos", table, nil); err == nil {
		t.Error("expected rejection for unknown order column")
	}
}

func TestParseQueryParams_AcceptsKnownOrder(t *testing.T) {
	table := testTable()
	c := testContext("order=priority.desc,title.asc")
	qp, err := parseQueryParams(c, "todos", table, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(qp.Order) != 2 {
		t.Fatalf("got %d order clauses, want 2", len(qp.Order))
	}
	if qp.Order[0].Column != "priority" || !qp.Order[0].Desc {
		t.Errorf("order[0] = %+v", qp.Order[0])
	}
	if qp.Order[1].Column != "title" || qp.Order[1].Desc {
		t.Errorf("order[1] = %+v", qp.Order[1])
	}
}

func TestParseQueryParams_RejectsUnknownSelectColumn(t *testing.T) {
	table := testTable()
	c := testContext("select=id,bogus")
	if _, err := parseQueryParams(c, "todos", table, nil); err == nil {
		t.Error("expected rejection for unknown select column")
	}
}

func TestParseQueryParams_AcceptsStarAndEmbed(t *testing.T) {
	table := testTable()
	c := testContext("select=*,author(id,name)")
	qp, err := parseQueryParams(c, "todos", table, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(qp.Select) != 2 {
		t.Fatalf("got %d select entries, want 2", len(qp.Select))
	}
}

func TestParseQueryParams_AcceptsKnownSelect(t *testing.T) {
	table := testTable()
	c := testContext("select=id,title,metadata-%3E%3Etheme")
	qp, err := parseQueryParams(c, "todos", table, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(qp.Select) != 3 {
		t.Fatalf("got %d select entries, want 3", len(qp.Select))
	}
}
