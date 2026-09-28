package http

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/adapter/http/postgrest"
	"github.com/instancez/instancez/internal/domain"
)

// TestBuildRPCCall_OrdersByDeclaration ensures that callArgs provided in
// any order produce SQL with placeholders aligned to declaration order,
// so the pgx positional params slice always matches $1/$2/... in the
// same positions.
func TestBuildRPCCall_OrdersByDeclaration(t *testing.T) {
	fn := domain.Function{ReturnCategory: "setof",
		Args: []domain.FuncArg{
			{Name: "post_id", Type: "uuid"},
			{Name: "limit_n", Type: "int"},
		},
	}
	// Callers can pass args in any order; declaration order is what
	// drives the generated SQL so the $N slots are deterministic.
	callArgs := map[string]any{
		"limit_n": 10,
		"post_id": "abc",
	}
	sql, ph := buildRPCCall("get_comments_for_post", fn, callArgs)

	want := `SELECT * FROM public."get_comments_for_post"("post_id" => $1, "limit_n" => $2)`
	if sql != want {
		t.Errorf("sql mismatch:\nwant %q\ngot  %q", want, sql)
	}
	if len(ph) != 2 || ph[0] != "abc" || ph[1] != 10 {
		t.Errorf("placeholder mismatch: %v", ph)
	}
}

// TestBuildRPCCall_OmitsMissingArgs confirms that args absent from
// callArgs are dropped from the SQL entirely, letting Postgres fall
// back to the function's DEFAULT value.
func TestBuildRPCCall_OmitsMissingArgs(t *testing.T) {
	fn := domain.Function{ReturnCategory: "scalar",
		Args: []domain.FuncArg{
			{Name: "a", Type: "int"},
			{Name: "b", Type: "int"},
		},
	}
	sql, ph := buildRPCCall("f", fn, map[string]any{"a": 1})
	want := `SELECT public."f"("a" => $1) AS "f"`
	if sql != want {
		t.Errorf("sql mismatch:\nwant %q\ngot  %q", want, sql)
	}
	if len(ph) != 1 {
		t.Errorf("expected 1 placeholder, got %v", ph)
	}
}

// TestBuildRPCCall_NoArgs exercises the zero-arg function case — the
// parenthesis list must be empty and the alias must still be present
// so the scalar unwrap path can find the column.
func TestBuildRPCCall_NoArgs(t *testing.T) {
	fn := domain.Function{ReturnCategory: "scalar"}
	sql, ph := buildRPCCall("now_tz", fn, nil)
	want := `SELECT public."now_tz"() AS "now_tz"`
	if sql != want {
		t.Errorf("sql mismatch:\nwant %q\ngot  %q", want, sql)
	}
	if len(ph) != 0 {
		t.Errorf("expected 0 placeholders, got %v", ph)
	}
}

// TestCollectRPCArgs_RejectsUnknownKeys covers the primary SQL-injection
// defense at the request-body layer. Any JSON key that isn't in the
// function's declared arg list must be rejected outright — not silently
// dropped — so a typo surfaces and a probe for internal args fails.
func TestCollectRPCArgs_RejectsUnknownKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{}}
	fn := domain.Function{Args: []domain.FuncArg{{Name: "x", Type: "int"}}}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/rpc/f", strings.NewReader(`{"x": 1, "DROP TABLE": 2}`))
	c.Request.Header.Set("Content-Type", "application/json")

	_, err := h.collectRPCArgs(c, "f", fn)
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
	if !strings.Contains(err.Error(), "unknown argument") {
		t.Errorf("expected unknown-argument error, got %v", err)
	}
}

// TestCollectRPCArgs_RequiresRequired confirms that required args
// missing from the body surface a clean 400-worthy error before the
// request hits the database.
func TestCollectRPCArgs_RequiresRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{}}
	fn := domain.Function{Args: []domain.FuncArg{{Name: "x", Type: "int", Required: true}}}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/rpc/f", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")

	_, err := h.collectRPCArgs(c, "f", fn)
	if err == nil {
		t.Fatal("expected error for missing required arg")
	}
	if !strings.Contains(err.Error(), "missing required argument") {
		t.Errorf("expected missing-required error, got %v", err)
	}
}

// TestCollectRPCArgs_SingleObject verifies the Prefer: params=single-object
// code path: the entire JSON body becomes the value of the single
// declared arg, matching PostgREST's behavior for functions that take
// one json/jsonb parameter.
func TestCollectRPCArgs_SingleObject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{}}
	fn := domain.Function{Args: []domain.FuncArg{{Name: "payload", Type: "jsonb"}}}

	body := `{"a": 1, "b": [2, 3]}`
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/rpc/f", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Prefer", "params=single-object")

	out, err := h.collectRPCArgs(c, "f", fn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s, ok := out["payload"].(string); !ok || s != body {
		t.Errorf("expected payload=%q, got %v", body, out)
	}
}

// TestRPCEndpoint_VolatileRejectsGET: PostgREST blocks GET on volatile
// functions because URLs are retryable and cacheable. The failure has
// to be a structured 405 with a recognizable code, not a silent 404.
func TestRPCEndpoint_VolatileRejectsGET(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{
		Tables: map[string]domain.Table{},
		RPC: map[string]domain.Function{
			"write_stuff": {
				Volatility:     "volatile",
				Returns:        domain.FuncReturn{Type: "void"},
				ReturnCategory: "void",
				Body:           "BEGIN END;",
			},
		},
	}}
	r := gin.New()
	h.Mount(r.Group(""))

	req := httptest.NewRequest("GET", "/rest/v1/rpc/write_stuff", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Errorf("expected 405, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %s", w.Body.String())
	}
	if body["code"] != "PGRST102" {
		t.Errorf("expected code PGRST102, got %v", body["code"])
	}
}

// TestParseSetofTarget locks down the dispatch rule that turns a raw
// Postgres return type into a table name for column validation. The RPC
// chain falls back to permissive validation for anything this helper
// doesn't resolve, so its edge cases directly control whether
// user-friendly column errors appear.
func TestParseSetofTarget(t *testing.T) {
	cases := map[string]string{
		"setof users":           "users",
		"SETOF users":           "users",
		"setof public.users":    "users",
		`setof "users"`:         "users",
		"setof table(id int)":   "", // TABLE(...) form — no named target
		"int":                   "", // scalar
		"void":                  "", // void
		"table(id int, v text)": "", // missing setof keyword
	}
	for in, want := range cases {
		if got := parseSetofTarget(in); got != want {
			t.Errorf("parseSetofTarget(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRenderRPCChain_Empty ensures the identity path: no filters, no
// order, no pagination → empty suffix, empty args. That's the contract
// wrapRPCCallForChain relies on to leave scalar/void SQL untouched.
func TestRenderRPCChain_Empty(t *testing.T) {
	suffix, args := renderRPCChain(&rpcChainSQL{}, 1)
	if suffix != "" || len(args) != 0 {
		t.Errorf("empty chain must render empty, got %q %v", suffix, args)
	}
}

// TestRenderRPCChain_LimitOffset checks that LIMIT and OFFSET get
// positional placeholders starting at the caller's baseArgIdx (which is
// len(callArgs)+1 in real use). Off-by-one here silently corrupts the
// data query.
func TestRenderRPCChain_LimitOffset(t *testing.T) {
	chain := &rpcChainSQL{hasLimit: true, limit: 5, hasOffset: true, offset: 10}
	// Pretend the function call already consumed $1 (e.g. one arg).
	suffix, args := renderRPCChain(chain, 2)
	want := " LIMIT $2 OFFSET $3"
	if suffix != want {
		t.Errorf("suffix = %q, want %q", suffix, want)
	}
	if len(args) != 2 || args[0] != 5 || args[1] != 10 {
		t.Errorf("args = %v, want [5 10]", args)
	}
}

// TestWrapRPCCallForChain_PassThrough confirms an empty chain is a
// no-op wrapper — the generated SQL is byte-identical to the underlying
// call. This matters because scalar/void paths share the same wrapper
// site and must not gain a spurious subquery.
func TestWrapRPCCallForChain_PassThrough(t *testing.T) {
	call := `SELECT * FROM public."f"("a" => $1)`
	got, embedArgs := wrapRPCCallForChain(call, &rpcChainSQL{}, 2)
	if got != call {
		t.Errorf("expected pass-through, got %q", got)
	}
	if len(embedArgs) != 0 {
		t.Errorf("expected no embed args, got %v", embedArgs)
	}
}

// TestWrapRPCCallForChain_WithLimit verifies the subquery wrapping shape
// when any chain piece is present. The wrapper must alias the subquery
// as _rpc so the outer clauses have something to qualify against.
func TestWrapRPCCallForChain_WithLimit(t *testing.T) {
	call := `SELECT * FROM public."f"("a" => $1)`
	chain := &rpcChainSQL{hasLimit: true, limit: 3}
	got, _ := wrapRPCCallForChain(call, chain, 2)
	want := `SELECT * FROM (SELECT * FROM public."f"("a" => $1)) AS _rpc LIMIT $2`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// TestWrapRPCCallForChain_WithSelectProjection asserts the outer SELECT *
// is replaced when the client sent select=col1,col2. The projected columns
// must be qualified against the _rpc alias so they resolve from the
// derived table rather than the outer table namespace.
func TestWrapRPCCallForChain_WithSelectProjection(t *testing.T) {
	call := `SELECT * FROM public."f"()`
	chain := &rpcChainSQL{
		selectItems: []postgrest.SelectItem{
			{Col: "username"},
			{Col: "age"},
		},
	}
	got, _ := wrapRPCCallForChain(call, chain, 1)
	want := `SELECT _rpc.username, _rpc.age FROM (SELECT * FROM public."f"()) AS _rpc`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// TestWrapRPCCallForChain_SelectWithFilter combines a projection with a
// WHERE filter: both must coexist without trampling each other's
// placeholder numbering.
func TestWrapRPCCallForChain_SelectWithFilter(t *testing.T) {
	call := `SELECT * FROM public."f"()`
	where := postgrest.AndLeaves(postgrest.Filter{Column: "status", Operator: "eq", Value: "active"})
	chain := &rpcChainSQL{
		selectItems: []postgrest.SelectItem{{Col: "id"}},
		where:       where,
	}
	got, _ := wrapRPCCallForChain(call, chain, 1)
	want := `SELECT _rpc.id FROM (SELECT * FROM public."f"()) AS _rpc WHERE status = $1`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// TestWrapRPCCallForChain_HavingQualifiedToRPCAlias is a regression for the
// case where `?having=count.gt.5` (or `having=col.eq.x`) is layered onto a
// SETOF RPC. parseHavingParam pre-qualifies columns with the table name it
// was given, so the wrapped query — which exposes the underlying rows under
// the `_rpc` alias, not the original table name — would error with
// "missing FROM-clause entry for table" if any column qualifier was the
// raw table name. The fix is to qualify against `_rpc` for RPC chains.
func TestWrapRPCCallForChain_HavingQualifiedToRPCAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{
		Tables: map[string]domain.Table{
			"users": {
				Fields: []domain.Field{
					{Name: "id", Type: "int", PrimaryKey: true},
					{Name: "username", Type: "text"},
					{Name: "status", Type: "text"},
				},
			},
		},
	}}
	fn := domain.Function{ReturnCategory: "setof",
		Returns: domain.FuncReturn{Type: "setof users"},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET",
		"/rpc/users_by_status?select=id.count()&having=count.gt.5", nil)
	chain, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1)
	if err != nil {
		t.Fatalf("parseRPCChain: %v", err)
	}

	call := `SELECT * FROM public."users_by_status"()`
	got, _ := wrapRPCCallForChain(call, chain, 1)

	// The aggregate column inside COUNT(...) must reference the _rpc alias,
	// not the underlying table that's no longer in scope.
	if !strings.Contains(got, "COUNT(_rpc.id)") {
		t.Errorf("expected COUNT(_rpc.id) in HAVING, got: %s", got)
	}
	if strings.Contains(got, "COUNT(users.id)") {
		t.Errorf("HAVING should not reference unaliased table name, got: %s", got)
	}
}

// TestWrapRPCCallForChain_HavingNonAggregateQualifiedToRPCAlias covers the
// non-aggregate HAVING leaf branch, which qualifies the raw column
// (`status`) rather than expanding an aggregate alias.
func TestWrapRPCCallForChain_HavingNonAggregateQualifiedToRPCAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{
		Tables: map[string]domain.Table{
			"users": {
				Fields: []domain.Field{
					{Name: "id", Type: "int", PrimaryKey: true},
					{Name: "status", Type: "text"},
				},
			},
		},
	}}
	fn := domain.Function{ReturnCategory: "setof",
		Returns: domain.FuncReturn{Type: "setof users"},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET",
		"/rpc/users_by_status?select=status,id.count()&having=status.eq.active", nil)
	chain, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1)
	if err != nil {
		t.Fatalf("parseRPCChain: %v", err)
	}

	call := `SELECT * FROM public."users_by_status"()`
	got, _ := wrapRPCCallForChain(call, chain, 1)

	if !strings.Contains(got, " GROUP BY _rpc.status HAVING _rpc.status = $") {
		t.Errorf("expected GROUP BY _rpc.status before HAVING, got: %s", got)
	}
	if strings.Contains(got, "users.status =") {
		t.Errorf("HAVING should not reference table name, got: %s", got)
	}
}

// TestParseRPCChain_Select verifies that parseRPCChain reads select=…,
// validates columns against the target setof table, and stores parsed
// items on the chain. Unknown columns must be rejected up front so the
// error surfaces as a clean 400 rather than a Postgres execution error.
func TestParseRPCChain_Select(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{
		Tables: map[string]domain.Table{
			"users": {
				Fields: []domain.Field{
					{Name: "id", Type: "int", PrimaryKey: true},
					{Name: "username", Type: "text"},
					{Name: "status", Type: "text"},
				},
			},
		},
	}}
	fn := domain.Function{ReturnCategory: "setof",
		Returns: domain.FuncReturn{Type: "setof users"},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/rpc/users_by_status?select=id,username&status=eq.ONLINE", nil)
	chain, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1)
	if err != nil {
		t.Fatalf("parseRPCChain: %v", err)
	}
	if len(chain.selectItems) != 2 {
		t.Fatalf("selectItems = %+v", chain.selectItems)
	}
	if chain.selectItems[0].Col != "id" || chain.selectItems[1].Col != "username" {
		t.Errorf("unexpected items: %+v", chain.selectItems)
	}

	// Unknown column must fail.
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("GET", "/rpc/users_by_status?select=id,bogus", nil)
	if _, _, err := h.parseRPCChain(c2, fn, map[string]bool{}, 1); err == nil {
		t.Error("expected unknown-column rejection")
	}

	// Embeds on unknown tables must fail — no FK graph to resolve.
	fnUnknown := domain.Function{ReturnCategory: "setof",
		Returns: domain.FuncReturn{Type: "setof custom_type"},
	}
	c3, _ := gin.CreateTestContext(httptest.NewRecorder())
	c3.Request = httptest.NewRequest("GET", "/rpc/custom_fn?select=id,posts(*)", nil)
	if _, _, err := h.parseRPCChain(c3, fnUnknown, map[string]bool{}, 1); err == nil {
		t.Error("expected embed rejection on unknown table")
	}
}

// TestParseRPCChain_SelectStar documents the escape hatch: select=* on
// an RPC call clears any prior projection and reverts to SELECT *, so
// clients can keep default behavior when they don't want to narrow.
func TestParseRPCChain_SelectStar(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{}}
	fn := domain.Function{ReturnCategory: "setof", Returns: domain.FuncReturn{Type: "setof any"}}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/rpc/f?select=*", nil)
	chain, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1)
	if err != nil {
		t.Fatalf("parseRPCChain: %v", err)
	}
	if len(chain.selectItems) != 0 {
		t.Errorf("select=* should reset projection, got %+v", chain.selectItems)
	}
}

// TestParseRPCChain_EmbedOnKnownTable verifies that embeds are resolved
// when the RPC returns SETOF of a table with known FK relationships.
func TestParseRPCChain_EmbedOnKnownTable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{
		Tables: map[string]domain.Table{
			"posts": {
				Fields: []domain.Field{
					{Name: "id", Type: "int", PrimaryKey: true},
					{Name: "title", Type: "text"},
					{Name: "author_id", Type: "int", ForeignKey: &domain.ForeignKey{References: "authors.id"}},
				},
			},
			"authors": {
				Fields: []domain.Field{
					{Name: "id", Type: "int", PrimaryKey: true},
					{Name: "name", Type: "text"},
				},
			},
		},
	}}
	fn := domain.Function{ReturnCategory: "setof", Returns: domain.FuncReturn{Type: "setof posts"}}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/rpc/recent_posts?select=id,title,authors(name)", nil)
	chain, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1)
	if err != nil {
		t.Fatalf("parseRPCChain: %v", err)
	}
	if len(chain.selectItems) != 2 {
		t.Errorf("expected 2 select items, got %+v", chain.selectItems)
	}
	if len(chain.embeds) != 1 {
		t.Fatalf("expected 1 embed, got %+v", chain.embeds)
	}
	emb := chain.embeds[0]
	if emb.Name != "authors" || emb.FKColumn != "author_id" || emb.RefTable != "authors" {
		t.Errorf("embed = %+v", emb)
	}
}

func TestParseRPCChain_EmbedOnKnownTable_RejectsUnsafeColumn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{
		Tables: map[string]domain.Table{
			"posts": {
				Fields: []domain.Field{
					{Name: "id", Type: "int", PrimaryKey: true},
					{Name: "author_id", Type: "int", ForeignKey: &domain.ForeignKey{References: "authors.id"}},
				},
			},
			"authors": {
				Fields: []domain.Field{
					{Name: "id", Type: "int", PrimaryKey: true},
				},
			},
		},
	}}
	fn := domain.Function{ReturnCategory: "setof", Returns: domain.FuncReturn{Type: "setof posts"}}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/rpc/recent_posts?select="+url.QueryEscape("id,authors(id x)"), nil)
	if _, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1); err == nil {
		t.Fatal("unsafe embed column accepted on RPC embed path")
	}
}

// TestWrapRPCCallForChain_BelongsToEmbed verifies that a belongs-to embed
// produces a LEFT JOIN with the correct alias and row_to_json projection.
func TestWrapRPCCallForChain_BelongsToEmbed(t *testing.T) {
	call := `SELECT * FROM public."recent_posts"()`
	chain := &rpcChainSQL{
		embeds: []Embed{{
			Name:      "authors",
			FKColumn:  "author_id",
			RefTable:  "authors",
			RefColumn: "id",
		}},
	}
	got, embedArgs := wrapRPCCallForChain(call, chain, 1)
	if !strings.Contains(got, "LEFT JOIN authors AS _emb_authors ON _rpc.author_id = _emb_authors.id") {
		t.Errorf("missing LEFT JOIN: %s", got)
	}
	if !strings.Contains(got, "row_to_json(_emb_authors.*) AS authors") {
		t.Errorf("missing row_to_json projection: %s", got)
	}
	if !strings.Contains(got, "_rpc.*") {
		t.Errorf("should project _rpc.*: %s", got)
	}
	if len(embedArgs) != 0 {
		t.Errorf("belongs-to embed should have no args, got %v", embedArgs)
	}
}

// TestWrapRPCCallForChain_HasManyEmbed verifies that a has-many (reverse)
// embed produces a correlated scalar subselect with json_agg.
func TestWrapRPCCallForChain_HasManyEmbed(t *testing.T) {
	call := `SELECT * FROM public."get_authors"()`
	chain := &rpcChainSQL{
		embeds: []Embed{{
			Name:      "posts",
			FKColumn:  "author_id",
			RefTable:  "posts",
			RefColumn: "id",
			IsReverse: true,
		}},
	}
	got, _ := wrapRPCCallForChain(call, chain, 1)
	if !strings.Contains(got, "json_agg(") {
		t.Errorf("missing json_agg for has-many: %s", got)
	}
	if !strings.Contains(got, "posts.author_id = _rpc.id") {
		t.Errorf("missing correlated WHERE: %s", got)
	}
	if !strings.Contains(got, "AS posts") {
		t.Errorf("missing embed alias: %s", got)
	}
}

// TestWrapRPCCallForChain_BelongsToWithColumns verifies that specific
// column selection on a belongs-to embed uses json_build_object.
func TestWrapRPCCallForChain_BelongsToWithColumns(t *testing.T) {
	call := `SELECT * FROM public."f"()`
	chain := &rpcChainSQL{
		embeds: []Embed{{
			Name:      "author",
			Columns:   []string{"id", "name"},
			FKColumn:  "author_id",
			RefTable:  "authors",
			RefColumn: "id",
		}},
	}
	got, _ := wrapRPCCallForChain(call, chain, 1)
	if !strings.Contains(got, "json_build_object(") {
		t.Errorf("expected json_build_object for column-specific embed: %s", got)
	}
	if !strings.Contains(got, "'id', _emb_author.id") {
		t.Errorf("missing column projection: %s", got)
	}
	if !strings.Contains(got, "CASE WHEN _emb_author.id IS NULL THEN NULL ELSE json_build_object(") {
		t.Errorf("unmatched to-one embed must be null, not an object of nulls: %s", got)
	}
}

func TestParseRPCChain_UnknownSetofRejectsUnsafeIdentifiers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{}}
	fn := domain.Function{ReturnCategory: "setof", Returns: domain.FuncReturn{Type: "setof record"}}
	cases := []string{
		"/rpc/f?" + url.QueryEscape(`a"b`) + "=eq.1",
		"/rpc/f?" + url.QueryEscape("a b") + "=eq.1",
		"/rpc/f?" + url.QueryEscape("data->>k'x") + "=eq.1",
		"/rpc/f?or=" + url.QueryEscape("(a;b.eq.1)"),
		"/rpc/f?order=" + url.QueryEscape(`x"y.desc`),
	}
	for _, target := range cases {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", target, nil)
		if _, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1); err == nil {
			t.Errorf("%s: expected rejection", target)
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/rpc/f?name=eq.x&data->>k=eq.y&order=name.desc", nil)
	if _, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1); err != nil {
		t.Fatalf("safe identifiers rejected: %v", err)
	}
}

// "setof table(...)" isn't a valid declared shape, so it falls back to the ident validator.
func TestParseRPCChain_TableShapeSelectUsesIdentValidator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{}}
	fn := domain.Function{ReturnCategory: "setof", Returns: domain.FuncReturn{Type: "setof table(id int)"}}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/rpc/f?select="+url.QueryEscape("a;b"), nil)
	if _, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1); err == nil {
		t.Error("select=a;b: expected rejection")
	}

	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/rpc/f?select=id", nil)
	if _, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1); err != nil {
		t.Fatalf("select=id: valid column rejected: %v", err)
	}
}

// Regression: "setof record" must use the ident validator, not a zero-value table.
func TestParseRPCChain_SetofRecordSelectUsesIdentValidator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{}}
	fn := domain.Function{ReturnCategory: "setof", Returns: domain.FuncReturn{Type: "setof record"}}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/rpc/f?select=name", nil)
	if _, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1); err != nil {
		t.Fatalf("select=name: safe identifier rejected: %v", err)
	}

	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/rpc/f?select="+url.QueryEscape("a;b"), nil)
	if _, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1); err == nil {
		t.Error("select=a;b: expected rejection")
	}
}

// TestRPCTableShape parses a TABLE(...) return type into a synthetic domain.Table.
func TestRPCTableShape(t *testing.T) {
	tbl, ok := rpcTableShape("TABLE(id int, total numeric(10,2), Label text)")
	if !ok || len(tbl.Fields) != 3 {
		t.Fatalf("got %+v ok=%v", tbl, ok)
	}
	if tbl.Fields[1].Name != "total" || tbl.Fields[1].Type != "numeric(10,2)" {
		t.Errorf("field[1] = %+v", tbl.Fields[1])
	}
	if tbl.Fields[2].Name != "label" {
		t.Errorf("unquoted column must fold to lower case, got %q", tbl.Fields[2].Name)
	}
	for _, raw := range []string{"", "setof users", "int", "table()", "table(id)", "table(id int", "table (a int, (b) int)"} {
		if _, ok := rpcTableShape(raw); ok {
			t.Errorf("rpcTableShape(%q) ok, want false", raw)
		}
	}
}

func TestParseRPCChain_TableShapeValidatesColumns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &CRUDHandler{cfg: &domain.Config{}}
	fn := domain.Function{ReturnCategory: "setof", Returns: domain.FuncReturn{Type: "table(id int, meta jsonb)"}}
	good := "/rpc/f?select=id&id=gt.1&meta->>k=eq.v&order=id.desc&having=" + url.QueryEscape("id.gt.0")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", good, nil)
	if _, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1); err != nil {
		t.Fatalf("declared columns rejected: %v", err)
	}
	reject := []string{
		"/rpc/f?other=eq.1",
		"/rpc/f?select=other",
		"/rpc/f?order=other",
		"/rpc/f?or=" + url.QueryEscape("(other.eq.1)"),
		"/rpc/f?" + url.QueryEscape("other->>k") + "=eq.1",
	}
	for _, target := range reject {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", target, nil)
		if _, _, err := h.parseRPCChain(c, fn, map[string]bool{}, 1); err == nil {
			t.Errorf("%s: undeclared column accepted", target)
		}
	}
}

func TestRenderRPCChain_OrderUsesValidatedUnquotedColumns(t *testing.T) {
	chain := &rpcChainSQL{order: []postgrest.OrderClause{
		{Column: "age", Desc: true, Nulls: "last"},
		{Column: "username"},
	}}
	sql, args := renderRPCChain(chain, 1)
	if sql != " ORDER BY age DESC NULLS LAST, username ASC" {
		t.Errorf("sql = %q, want %q", sql, " ORDER BY age DESC NULLS LAST, username ASC")
	}
	if len(args) != 0 {
		t.Errorf("order must not add args, got %v", args)
	}
}

func TestRenderRPCChain_OrderPreservesJSONBOperators(t *testing.T) {
	chain := &rpcChainSQL{order: []postgrest.OrderClause{
		{Column: "data->>key", Desc: false, Nulls: ""},
	}}
	sql, args := renderRPCChain(chain, 1)
	if sql != " ORDER BY data->>key ASC" {
		t.Errorf("sql = %q, want %q", sql, " ORDER BY data->>key ASC")
	}
	if len(args) != 0 {
		t.Errorf("order must not add args, got %v", args)
	}
}

func TestRenderRPCChain_EmptyOrderNoEmission(t *testing.T) {
	chain := &rpcChainSQL{order: []postgrest.OrderClause{}}
	sql, args := renderRPCChain(chain, 1)
	if sql != "" {
		t.Errorf("empty order should emit nothing, got %q", sql)
	}
	if len(args) != 0 {
		t.Errorf("empty order should have no args, got %v", args)
	}
}

func TestBuildRPCCountedQuery_CallsOnce(t *testing.T) {
	call := `SELECT * FROM public."f"("a" => $1)`
	chain := &rpcChainSQL{
		where:    postgrest.AndLeaves(postgrest.Filter{Column: "age", Operator: "gt", Value: "1"}),
		order:    []postgrest.OrderClause{{Column: "age", Desc: true}},
		hasLimit: true, limit: 2, hasOffset: true, offset: 1,
	}
	sql := buildRPCCountedQuery(call, chain, 2)
	if n := strings.Count(sql, `public."f"(`); n != 1 {
		t.Fatalf("function referenced %d times: %s", n, sql)
	}
	for _, want := range []string{
		`WITH __inz_src AS MATERIALIZED (SELECT * FROM public."f"("a" => $1))`,
		`(SELECT count(*) AS __inz_total FROM __inz_src AS _rpc WHERE age > $2) _t`,
		`row_number() OVER (ORDER BY _rpc.age DESC) AS __inz_rn`,
		`LIMIT $3 OFFSET $4`,
		`) _p ON true ORDER BY _p.__inz_rn`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q in %s", want, sql)
		}
	}
}

func TestBuildRPCCountedQuery_NoChainStillWrapsAndCounts(t *testing.T) {
	sql := buildRPCCountedQuery(`SELECT * FROM public."f"()`, &rpcChainSQL{}, 1)
	for _, want := range []string{
		`(SELECT count(*) AS __inz_total FROM __inz_src AS _rpc) _t`,
		`SELECT _rpc.*, true AS __inz_row, row_number() OVER () AS __inz_rn FROM (SELECT * FROM __inz_src) AS _rpc`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q in %s", want, sql)
		}
	}
}

func TestBuildRPCCountedQuery_AliasOrderFallsBackToPlainWindow(t *testing.T) {
	chain := &rpcChainSQL{order: []postgrest.OrderClause{{Column: "total", IsAlias: true}}}
	sql := buildRPCCountedQuery(`SELECT * FROM public."f"()`, chain, 1)
	if !strings.Contains(sql, "row_number() OVER () AS __inz_rn") {
		t.Errorf("alias order must not reach the window: %s", sql)
	}
}

func TestSplitRPCTotal(t *testing.T) {
	rows := []map[string]any{
		{"id": int64(1), "__inz_row": true, "__inz_rn": int64(1), "__inz_total": int64(7)},
		{"id": int64(2), "__inz_row": true, "__inz_rn": int64(2), "__inz_total": int64(7)},
	}
	got, total := splitRPCTotal(rows)
	if total != 7 || len(got) != 2 || !reflect.DeepEqual(got[0], map[string]any{"id": int64(1)}) {
		t.Fatalf("got %v total %d", got, total)
	}
	got, total = splitRPCTotal([]map[string]any{{"id": nil, "__inz_row": nil, "__inz_rn": nil, "__inz_total": int64(5)}})
	if total != 5 || len(got) != 0 {
		t.Fatalf("empty page: got %v total %d", got, total)
	}
	got, total = splitRPCTotal([]map[string]any{{"id": nil, "__inz_row": nil, "__inz_rn": nil, "__inz_total": int64(0)}})
	if total != 0 || len(got) != 0 {
		t.Fatalf("zero total: got %v total %d", got, total)
	}
	got, total = splitRPCTotal(nil)
	if total != -1 || len(got) != 0 {
		t.Fatalf("nil: got %v total %d", got, total)
	}
}

func TestBuildRPCCountedQuery_AggregateCountsUngroupedRows(t *testing.T) {
	chain := &rpcChainSQL{
		selectItems: []postgrest.SelectItem{{Col: "status"}, {Agg: "count"}},
		groupBy:     []string{"_rpc.status"},
		where:       postgrest.AndLeaves(postgrest.Filter{Column: "age", Operator: "gt", Value: "1"}),
		having:      postgrest.AndLeaves(postgrest.Filter{Column: "count", Operator: "gt", Value: "2"}),
		hasLimit:    true, limit: 5,
	}
	sql := buildRPCCountedQuery(`SELECT * FROM public."f"()`, chain, 1)
	for _, want := range []string{
		`(SELECT count(*) AS __inz_total FROM __inz_src AS _rpc WHERE age > $1) _t`,
		`FROM (SELECT * FROM __inz_src) AS _rpc WHERE age > $1 GROUP BY _rpc.status HAVING count > $2 LIMIT $3`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q in %s", want, sql)
		}
	}
}
