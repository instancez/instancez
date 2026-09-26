package http

import (
	"math"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/adapter/http/postgrest"
	"github.com/instancez/instancez/internal/domain"
)

func TestParseQueryParams_NoDefaultLimit(t *testing.T) {
	qp, err := parseQueryParams(testContext(""), "todos", testTable(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if qp.Limit != postgrest.NoLimit {
		t.Fatalf("Limit = %d, want NoLimit (PostgREST has no default limit)", qp.Limit)
	}
	sql, _ := buildSelectQuery("todos", qp, testTable())
	if strings.Contains(sql, "LIMIT") || strings.Contains(sql, "OFFSET") {
		t.Fatalf("unexpected paging in %q", sql)
	}
}

func TestParseQueryParams_LimitValidation(t *testing.T) {
	for _, raw := range []string{"limit=-1", "limit=NaN", "limit=1.5", "limit=99999999999999999999", "limit=", "offset=-1", "offset=NaN"} {
		_, err := parseQueryParams(testContext(raw), "todos", testTable(), nil)
		if raw == "limit=" {
			if err != nil {
				t.Errorf("empty limit must be ignored, got %v", err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: expected error", raw)
		}
	}
	qp, err := parseQueryParams(testContext("limit=0"), "todos", testTable(), nil)
	if err != nil || qp.Limit != 0 {
		t.Fatalf("limit=0: Limit=%d err=%v", qp.Limit, err)
	}
}

func TestBuildSelectQuery_NoLimitKeepsOffset(t *testing.T) {
	sql, _ := buildSelectQuery("todos", &QueryParams{Limit: postgrest.NoLimit, Offset: 5}, testTable())
	if !strings.HasSuffix(sql, " OFFSET 5") || strings.Contains(sql, "LIMIT") {
		t.Fatalf("got %q", sql)
	}
	sql, _ = buildSelectQuery("todos", &QueryParams{Limit: 0}, testTable())
	if !strings.HasSuffix(sql, " LIMIT 0 OFFSET 0") {
		t.Fatalf("limit=0 must stay LIMIT 0, got %q", sql)
	}
}

func TestCapLimit(t *testing.T) {
	cases := []struct{ limit, maxRows, want int }{
		{postgrest.NoLimit, 1000, 1000},
		{5, 1000, 5},
		{1000, 1000, 1000},
		{1001, 1000, 1000},
		{math.MaxInt, 1000, 1000},
		{0, 1000, 0},
		{math.MinInt, 1000, 1000}, // Range: 0-MaxInt overflows end-start+1
		{postgrest.NoLimit, 0, postgrest.NoLimit},
		{5000, 0, 5000},
		{math.MinInt, 0, math.MinInt},
		{postgrest.NoLimit, -1, postgrest.NoLimit},
		{5000, -1, 5000},
	}
	for _, c := range cases {
		if got := capLimit(c.limit, c.maxRows); got != c.want {
			t.Errorf("capLimit(%d, %d) = %d, want %d", c.limit, c.maxRows, got, c.want)
		}
	}
}

func TestCapEmbeds(t *testing.T) {
	tables := postsAuthorTables()
	parse := func(table, raw string) *QueryParams {
		t.Helper()
		qp, err := parseQueryParams(testContext(raw), table, tables[table], tables)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		return qp
	}
	cases := []struct {
		raw     string
		maxRows int
		want    int // -1 = Limit stays nil
	}{
		{"select=*,posts(*)", 1000, 1000},
		{"select=*,posts(*)&posts.limit=5", 1000, 5},
		{"select=*,posts(*)&posts.limit=0", 1000, 0},
		{"select=*,posts(*)&posts.limit=5000", 1000, 1000},
		{"select=*,posts(*)", 0, -1},
		{"select=*,posts(*)", -1, -1},
		{"select=*,posts(*)&posts.limit=5000", -1, 5000},
	}
	for _, c := range cases {
		qp := parse("authors", c.raw)
		capEmbeds(qp.Embeds, c.maxRows)
		got := qp.Embeds[0].Limit
		if c.want < 0 {
			if got != nil {
				t.Errorf("%s max=%d: Limit=%d, want nil", c.raw, c.maxRows, *got)
			}
			continue
		}
		if got == nil {
			t.Errorf("%s max=%d: Limit=nil, want %d", c.raw, c.maxRows, c.want)
		} else if *got != c.want {
			t.Errorf("%s max=%d: Limit=%d, want %d", c.raw, c.maxRows, *got, c.want)
		}
	}
	capEmbeds(nil, 1000)

	qp := parse("authors", "select=*,posts(*)")
	capEmbeds(qp.Embeds, 1000)
	if sql, _ := buildSelectQueryFull("authors", qp, tables["authors"], tables); !strings.Contains(sql, "LIMIT 1000") {
		t.Errorf("capped has-many embed must render LIMIT 1000: %s", sql)
	}
	// Belongs-to returns one object, so it is never capped.
	bt := parse("posts", "select=*,authors(*)")
	capEmbeds(bt.Embeds, 1000)
	if bt.Embeds[0].Limit != nil {
		t.Errorf("belongs-to embed must not get a limit")
	}
}

func TestParseRPCChain_MaxRowsCap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fn := domain.Function{ReturnCategory: "setof", Returns: domain.FuncReturn{Type: "setof todos"}}
	cases := []struct {
		query   string
		maxRows int
		wantHas bool
		want    int
	}{
		{"", 2, true, 2},
		{"limit=1", 2, true, 1},
		{"limit=0", 2, true, 0},
		{"limit=5", 2, true, 2},
		{"", 0, false, 0},
		{"", -1, false, 0},
		{"limit=5", -1, true, 5},
	}
	for _, tc := range cases {
		h := &CRUDHandler{cfg: &domain.Config{
			Server: domain.Server{MaxLimit: tc.maxRows},
			Tables: map[string]domain.Table{"todos": testTable()},
		}}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/rpc/f?"+tc.query, nil)
		chain, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1)
		if err != nil {
			t.Fatalf("%q: %v", tc.query, err)
		}
		if chain.hasLimit != tc.wantHas || (tc.wantHas && chain.limit != tc.want) {
			t.Errorf("%q max=%d: hasLimit=%v limit=%d, want %v/%d", tc.query, tc.maxRows, chain.hasLimit, chain.limit, tc.wantHas, tc.want)
		}
	}
}
