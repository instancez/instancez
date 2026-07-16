package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/adapter/http/postgrest"
	"github.com/instancez/instancez/internal/domain"
)

// handleRPC dispatches POST/GET /rest/v1/rpc/:name to a YAML-declared
// Postgres function. It mirrors PostgREST's RPC surface as closely as is
// needed for supabase-js .rpc() to work: JSON body as named args, Prefer:
// params=single-object for whole-body-as-jsonb, Accept:
// application/vnd.pgrst.object+json for singular responses, volatility
// gating for GET, and void → 204 No Content.
//
// Security model: the function name in the URL is matched against
// h.cfg.RPC, so only configured functions are reachable; a typo
// or probe returns PGRST202. Arg names in the request body are matched
// against the function's declared args (unknown keys are rejected, just
// like PostgREST). Arg values are passed as pgx placeholders, never
// concatenated into SQL. This closes the injection surface: URL path,
// JSON body keys, and values are all filtered or parameterized.
func (h *CRUDHandler) handleRPC() gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		fn, ok := h.cfg.RPC[name]
		if !ok {
			pgJSON(c, http.StatusNotFound, "PGRST202",
				fmt.Sprintf("Could not find the function public.%s in the schema cache", name),
				"",
				"")
			return
		}

		// GET and HEAD are only valid for non-volatile functions. PostgREST
		// blocks GET on VOLATILE because the URL may be cached or retried
		// and a volatile function can have side effects. HEAD follows the
		// same rule since it's just GET-without-body. POST always works.
		method := c.Request.Method
		if (method == http.MethodGet || method == http.MethodHead) && strings.EqualFold(fn.Volatility, "volatile") {
			pgJSON(c, http.StatusMethodNotAllowed, "PGRST102",
				"Volatile functions must be called with POST",
				"",
				"")
			return
		}

		if fn.AuthRequired {
			session := getSession(c)
			if !session.IsAuthenticated && !isAdmin(c) {
				problemJSON(c, http.StatusUnauthorized, "unauthorized", "This function requires authentication")
				return
			}
		}

		// Collect the request body (POST) or query params (GET) into a
		// map keyed by arg name, respecting Prefer: params=single-object.
		callArgs, err := h.collectRPCArgs(c, name, fn)
		if err != nil {
			problemJSON(c, http.StatusBadRequest, "bad_request", err.Error())
			return
		}

		// Build SELECT ... FROM public."<name>"("arg" => $1, ...).
		// Arg names were already checked against fn.Args so the quoted
		// identifiers cannot carry anything unexpected, but we double-
		// quote them anyway as defense-in-depth.
		sql, placeholders := buildRPCCall(name, fn, callArgs)

		// For setof RPC, supabase-js may chain .eq/.order/.limit/.offset
		// on top of the call; PostgREST runs these against the function
		// result. Wrap the call in a subquery and append WHERE/ORDER/LIMIT
		// on the outside. Filters are parsed here (not earlier) so the
		// placeholder numbering continues cleanly from the arg slots.
		var rpcChain *rpcChainSQL
		if fn.ReturnCategory == "setof" {
			argNames := make(map[string]bool, len(fn.Args))
			for _, a := range fn.Args {
				argNames[a.Name] = true
			}
			baseIdx := len(placeholders) + 1
			chain, chainArgs, err := h.parseRPCChain(c, fn, argNames, baseIdx)
			if err != nil {
				problemJSON(c, http.StatusBadRequest, "bad_request", err.Error())
				return
			}
			rpcChain = chain
			var embedArgs []any
			sql, embedArgs = wrapRPCCallForChain(sql, chain, baseIdx)
			placeholders = append(placeholders, chainArgs...)
			placeholders = append(placeholders, embedArgs...)
		}

		session := getSession(c)
		ctx, err := h.db.WithRLS(c.Request.Context(), session)
		if err != nil {
			problemJSON(c, http.StatusInternalServerError, "internal", "Failed to set RLS context")
			return
		}
		if isAdmin(c) {
			ctx = c.Request.Context()
		}

		tx, err := h.db.Begin(ctx)
		if err != nil {
			problemJSON(c, http.StatusInternalServerError, "internal", "Failed to start transaction")
			return
		}
		defer func() { _ = tx.Rollback(ctx) }()

		// Defense-in-depth: non-volatile functions are documented as
		// side-effect-free, so pin the transaction to read-only. Postgres
		// will reject INSERT/UPDATE/DELETE (including those issued through
		// dynamic SQL in the function body) with error 25006, blocking
		// misdeclared or malicious functions from mutating data through a
		// GET/.rpc read path. VOLATILE functions are unaffected.
		if !strings.EqualFold(fn.Volatility, "volatile") {
			if _, err := tx.Exec(ctx, "SET LOCAL transaction_read_only = on"); err != nil {
				h.logger.Error("rpc set read_only error", "fn", name, "error", err)
				problemJSON(c, http.StatusInternalServerError, "internal", "Failed to enter read-only mode")
				return
			}
		}

		switch fn.ReturnCategory {
		case "void":
			// Postgres still needs SELECT to invoke the function; we
			// discard the result. PostgREST responds with HTTP 204 No
			// Content and no body for void functions, which supabase-js
			// treats as data === null.
			if _, err := tx.Exec(ctx, sql, placeholders...); err != nil {
				h.logger.Error("rpc void error", "fn", name, "error", err)
				handleDBError(c, err)
				return
			}
			if err := tx.Commit(ctx); err != nil {
				problemJSON(c, http.StatusInternalServerError, "internal", "Transaction commit failed")
				return
			}
			c.Status(http.StatusNoContent)
			return

		case "setof":
			rows, err := tx.Query(ctx, sql, placeholders...)
			if err != nil {
				h.logger.Error("rpc setof error", "fn", name, "error", err)
				handleDBError(c, err)
				return
			}
			if err := tx.Commit(ctx); err != nil {
				problemJSON(c, http.StatusInternalServerError, "internal", "Transaction commit failed")
				return
			}
			if rows == nil {
				rows = []map[string]any{}
			}
			// Content-Range: emit on every setof response to match
			// PostgREST. When Prefer: count=exact is set, run a separate
			// COUNT(*) over the same filtered subquery and put the real
			// total after the slash; otherwise use "*".
			offset := 0
			if rpcChain != nil {
				offset = rpcChain.offset
			}
			end := offset + len(rows) - 1
			if len(rows) == 0 {
				end = offset
			}
			countMode := parseCountPrefer(joinPrefer(c))
			total := -1
			if countMode == "exact" {
				total, err = h.executeRPCCount(c, name, fn, callArgs, rpcChain, session)
				if err != nil {
					h.logger.Error("rpc count error", "fn", name, "error", err)
				}
			}
			if total >= 0 {
				c.Header("Content-Range", fmt.Sprintf("%d-%d/%d", offset, end, total))
			} else {
				c.Header("Content-Range", fmt.Sprintf("%d-%d/*", offset, end))
			}
			if c.GetHeader("Accept") == "application/vnd.pgrst.object+json" {
				if len(rows) == 0 {
					pgJSON(c, http.StatusNotAcceptable, "PGRST116",
						"JSON object requested, multiple (or no) rows returned",
						"The result contains 0 rows", "")
					return
				}
				if len(rows) > 1 {
					pgJSON(c, http.StatusNotAcceptable, "PGRST116",
						"JSON object requested, multiple (or no) rows returned",
						fmt.Sprintf("The result contains %d rows", len(rows)), "")
					return
				}
				c.JSON(http.StatusOK, rows[0])
				return
			}
			c.JSON(http.StatusOK, rows)
			return

		default: // "scalar"
			row, err := tx.QueryRow(ctx, sql, placeholders...)
			if err != nil {
				h.logger.Error("rpc scalar error", "fn", name, "error", err)
				handleDBError(c, err)
				return
			}
			if err := tx.Commit(ctx); err != nil {
				problemJSON(c, http.StatusInternalServerError, "internal", "Transaction commit failed")
				return
			}
			// For scalar functions, PostgREST returns the bare value
			// (number, string, bool, null) — not wrapped in an object.
			// QueryRow returns a map[col]value; our SELECT aliases the
			// call as the function name, so there's exactly one entry.
			if row == nil {
				c.JSON(http.StatusOK, nil)
				return
			}
			if v, ok := row[name]; ok && len(row) == 1 {
				c.JSON(http.StatusOK, v)
				return
			}
			c.JSON(http.StatusOK, row)
			return
		}
	}
}

// collectRPCArgs extracts named arguments from the request. For POST it
// reads a JSON object from the body; for GET it reads query-string
// values. When the Prefer header contains params=single-object the
// entire JSON body is treated as one jsonb argument — PostgREST uses
// this for functions declared with a single json/jsonb parameter, and
// supabase-js exposes it via .rpc('name', body, { params: 'single' }).
func (h *CRUDHandler) collectRPCArgs(c *gin.Context, fnName string, fn domain.Function) (map[string]any, error) {
	prefer := c.GetHeader("Prefer")
	singleObject := strings.Contains(prefer, "params=single-object")

	if singleObject {
		if len(fn.Args) != 1 {
			return nil, fmt.Errorf("params=single-object requires the function to have exactly one argument")
		}
		if c.Request.Method != http.MethodPost {
			return nil, fmt.Errorf("params=single-object requires POST")
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return nil, fmt.Errorf("reading body: %w", err)
		}
		// Pass the raw JSON bytes as a string; pgx encodes it as jsonb
		// when the target column type is jsonb. For json/jsonb args this
		// is the shape PostgREST uses.
		return map[string]any{fn.Args[0].Name: string(body)}, nil
	}

	raw := make(map[string]any)

	if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
		// GET/HEAD: pull each declared arg from the query string. Missing
		// args are treated as absent (handled downstream — either the
		// function has a default or Postgres raises undefined-arg).
		for _, a := range fn.Args {
			if v, ok := c.GetQuery(a.Name); ok {
				raw[a.Name] = v
			}
		}
	} else {
		// POST: body is a JSON object whose keys are arg names. An
		// empty body is valid (function with no args or all defaults).
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return nil, fmt.Errorf("reading body: %w", err)
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &raw); err != nil {
				return nil, fmt.Errorf("invalid JSON body: %w", err)
			}
		}
	}

	// Reject unknown keys — PostgREST does this and it's also our
	// strongest guarantee that nothing unexpected reaches the SQL
	// builder. Without this, a typo like `postId` vs `post_id` would
	// silently drop the arg instead of surfacing the mistake.
	declared := make(map[string]bool, len(fn.Args))
	for _, a := range fn.Args {
		declared[a.Name] = true
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if !declared[k] {
			return nil, fmt.Errorf("unknown argument %q for function %s", k, fnName)
		}
		// json.Unmarshal decodes every number as float64, but Postgres
		// does function resolution by exact type — there's no implicit
		// float8→int cast. Coerce integer-valued floats to int64 so
		// pgx sends them as int8 and calls like add_numbers(int, int)
		// resolve cleanly. This mirrors PostgREST's behavior: JSON
		// numbers without a fractional part flow into integer params.
		if f, ok := v.(float64); ok && f == float64(int64(f)) {
			out[k] = int64(f)
			continue
		}
		out[k] = v
	}

	// Required-arg check. Postgres would raise a helpful error on its
	// own, but catching it here gives a cleaner 400 before we even
	// open a transaction.
	for _, a := range fn.Args {
		if a.Required {
			if _, ok := out[a.Name]; !ok {
				return nil, fmt.Errorf("missing required argument %q", a.Name)
			}
		}
	}
	return out, nil
}

// buildRPCCall assembles the SELECT statement that invokes an RPC Function
// with named arguments. Args present in callArgs are passed as
// "name" => $N placeholders; args absent from callArgs are omitted,
// letting Postgres fall back to the function's DEFAULT values. For void
// functions we still use SELECT (not CALL) because Postgres allows
// invoking any returns-void function from a SELECT list, and this keeps
// the code path uniform with scalar/setof.
//
// The returned placeholders slice is positional — placeholders[0]
// corresponds to $1, placeholders[1] to $2, etc. — and is passed to the
// pgx call directly so values are never interpolated into SQL.
func buildRPCCall(name string, fn domain.Function, callArgs map[string]any) (string, []any) {
	var parts []string
	placeholders := make([]any, 0, len(callArgs))
	idx := 1
	// Iterate in declaration order so the generated SQL and the
	// placeholder slice stay aligned with the YAML schema.
	for _, a := range fn.Args {
		v, ok := callArgs[a.Name]
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf(`"%s" => $%d`, a.Name, idx))
		placeholders = append(placeholders, v)
		idx++
	}
	// Alias the function call to its own name so QueryRow's result map
	// has a predictable key for scalar unwrapping. For setof/table
	// returns the SELECT * bypasses this alias and expands the tuple
	// into real columns the same way PostgREST does.
	var sql string
	if fn.ReturnCategory == "setof" {
		sql = fmt.Sprintf(`SELECT * FROM public."%s"(%s)`, name, strings.Join(parts, ", "))
	} else {
		sql = fmt.Sprintf(`SELECT public."%s"(%s) AS "%s"`, name, strings.Join(parts, ", "), name)
	}
	return sql, placeholders
}

// rpcChainSQL carries the parsed pieces of a chained setof RPC call. We
// keep the WhereNode tree (not just its rendered SQL) so executeRPCCount
// can replay it with a fresh placeholder numbering. orderClauses is kept
// structured for the same reason even though order never carries params.
type rpcChainSQL struct {
	where       *postgrest.WhereNode
	having      *postgrest.WhereNode // HAVING clause for aggregate filtering
	selectItems []postgrest.SelectItem // non-empty → project these instead of SELECT *
	embeds      []postgrest.Embed    // resolved embeds when RPC returns SETOF <known table>
	order       []postgrest.OrderClause
	hasLimit    bool
	limit       int
	hasOffset   bool
	offset      int // echoed into Content-Range as "start-end/total"
}

// parseRPCChain parses the PostgREST-style query parameters layered on top
// of a SETOF RPC call. Columns are validated against the target table when
// fn.Returns names a known table; otherwise an any-column-goes validator is
// used and Postgres rejects mismatches at execute time. Function argument
// keys are skipped so GET calls can carry both function args and filters in
// the same query string without collision.
//
// argIdx is the first free placeholder index; the returned args slice is
// appended to the caller's existing placeholder list.
// resolveRPCTargetTable resolves the table behind a SETOF <table> return type.
func (h *CRUDHandler) resolveRPCTargetTable(fn domain.Function) (domain.Table, bool) {
	target := parseSetofTarget(fn.Returns.Type)
	if target == "" {
		return domain.Table{}, false
	}
	if tbl, ok := h.cfg.Tables[target]; ok {
		return tbl, true
	}
	if tbl, ok := h.allTables()[target]; ok {
		return tbl, true
	}
	return domain.Table{}, false
}

func (h *CRUDHandler) parseRPCChain(c *gin.Context, fn domain.Function, argNames map[string]bool, argIdx int) (*rpcChainSQL, []any, error) {
	chain := &rpcChainSQL{}

	target := parseSetofTarget(fn.Returns.Type)
	targetTable, targetFound := h.resolveRPCTargetTable(fn)

	var validate postgrest.ColValidator
	if targetFound {
		tbl := targetTable
		validate = func(col string) error { return postgrest.ValidateColumn(tbl, col) }
	} else {
		validate = permissiveColValidator
	}

	if sel := c.Query("select"); sel != "" {
		table := targetTable
		tableFound := targetFound
		var embedParams []string
		for _, raw := range postgrest.ParseSelectParam(sel) {
			if raw == "" {
				continue
			}
			if strings.Contains(raw, "(") && !postgrest.IsAggSelectEntry(raw) {
				if !tableFound {
					return nil, nil, fmt.Errorf("embeds are not supported on RPC results returning unknown tables")
				}
				embedParams = append(embedParams, raw)
				continue
			}
			item := postgrest.ParseSelectItem(raw)
			if item.Col == "*" {
				chain.selectItems = nil
				break
			}
			if target != "" {
				if err := postgrest.ValidateSelectItem(table, item); err != nil {
					return nil, nil, fmt.Errorf("invalid select item: %w", err)
				}
			} else {
				// Permissive path: bare count() has no column; only
				// validate Col when it's actually present.
				if item.Col != "" {
					if err := validate(item.Col); err != nil {
						return nil, nil, fmt.Errorf("invalid select item: %w", err)
					}
				} else if item.Agg != "count" {
					return nil, nil, fmt.Errorf("aggregate %q requires a column", item.Agg)
				}
				if item.Alias != "" && !identRe.MatchString(item.Alias) {
					return nil, nil, fmt.Errorf("invalid alias %q", item.Alias)
				}
				if item.Cast != "" && !identRe.MatchString(item.Cast) {
					return nil, nil, fmt.Errorf("invalid cast %q", item.Cast)
				}
			}
			chain.selectItems = append(chain.selectItems, item)
		}
		// Resolve embeds against the target table's FK graph.
		if len(embedParams) > 0 {
			resolved, err := postgrest.ResolveEmbeds(target, table, embedParams, h.allTables())
			if err != nil {
				return nil, nil, fmt.Errorf("invalid embed: %w", err)
			}
			chain.embeds = resolved
		}
	}

	// WHERE tree.
	where, err := postgrest.ParseWhereWith(c.Request.URL.Query(), validate, nil, argNames)
	if err != nil {
		return nil, nil, err
	}
	chain.where = where

	// HAVING. Validate against the target table when known, but qualify
	// emitted column references with the `_rpc` alias — the wrapped query
	// exposes the SETOF rows under that alias, so the original table name
	// is no longer in scope on the outer SELECT.
	if havingRaw := c.Query("having"); havingRaw != "" {
		var selStrings []string
		for _, it := range chain.selectItems {
			if it.Agg != "" {
				raw := it.Col
				if raw != "" {
					raw += "."
				}
				raw += it.Agg + "()"
				if it.Alias != "" {
					raw = it.Alias + ":" + raw
				}
				selStrings = append(selStrings, raw)
			}
		}
		havingNode, err := postgrest.ParseHavingParam(havingRaw, "_rpc", targetTable, selStrings)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid having: %w", err)
		}
		chain.having = havingNode
	}

	// ORDER.
	if ord := c.Query("order"); ord != "" {
		clauses, err := postgrest.ParseOrderValueWith(ord, validate)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid order: %w", err)
		}
		chain.order = clauses
	}

	// LIMIT.
	if l := c.Query("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 {
			return nil, nil, fmt.Errorf("invalid limit: %s", l)
		}
		chain.hasLimit = true
		chain.limit = n
	}

	// OFFSET.
	if o := c.Query("offset"); o != "" {
		n, err := strconv.Atoi(o)
		if err != nil || n < 0 {
			return nil, nil, fmt.Errorf("invalid offset: %s", o)
		}
		chain.hasOffset = true
		chain.offset = n
	}

	// Render the chain with placeholder numbering starting at argIdx.
	// Args returned here are appended to the caller's placeholder list.
	_, outArgs := renderRPCChain(chain, argIdx)
	return chain, outArgs, nil
}

// renderRPCChain serializes a parsed chain into an SQL suffix (WHERE, ORDER,
// LIMIT, OFFSET) using placeholders starting at argIdx, plus a matching
// positional args slice. Returning both from a single helper guarantees the
// data and count queries stay in lockstep with their own argument indexing.
func renderRPCChain(chain *rpcChainSQL, argIdx int) (string, []any) {
	if chain == nil {
		return "", nil
	}
	var b strings.Builder
	var args []any
	if chain.where != nil {
		sql, whereArgs, next := chain.where.BuildSQL(argIdx)
		if sql != "" {
			b.WriteString(" WHERE ")
			b.WriteString(sql)
			args = append(args, whereArgs...)
			argIdx = next
		}
	}
	if chain.having != nil {
		sql, havingArgs, next := chain.having.BuildSQL(argIdx)
		if sql != "" {
			b.WriteString(" HAVING ")
			b.WriteString(sql)
			args = append(args, havingArgs...)
			argIdx = next
		}
	}
	if len(chain.order) > 0 {
		b.WriteString(" ")
		b.WriteString(buildOrderSQL(chain.order))
	}
	if chain.hasLimit {
		fmt.Fprintf(&b, " LIMIT $%d", argIdx)
		args = append(args, chain.limit)
		argIdx++
	}
	if chain.hasOffset {
		fmt.Fprintf(&b, " OFFSET $%d", argIdx)
		args = append(args, chain.offset)
	}
	return b.String(), args
}

// permissiveColValidator accepts any non-empty column name. Used when a RPC
// returns SETOF of an ad-hoc TABLE(...) shape that's not declared as a YAML
// table, so we can't validate column names up front.
func permissiveColValidator(col string) error {
	if col == "" {
		return fmt.Errorf("empty column name")
	}
	return nil
}

// parseSetofTarget extracts the table name from a SETOF return type. Returns
// "" for TABLE(...) and any type the RPC code can't resolve to a named table
// in the YAML schema. Strips an optional "public." schema prefix and any
// surrounding double quotes.
func parseSetofTarget(raw string) string {
	t := strings.TrimSpace(strings.ToLower(raw))
	if !strings.HasPrefix(t, "setof ") {
		return ""
	}
	t = strings.TrimSpace(strings.TrimPrefix(t, "setof "))
	t = strings.TrimPrefix(t, "public.")
	t = strings.Trim(t, `"`)
	if t == "" || strings.ContainsAny(t, "() ,") {
		return ""
	}
	return t
}

// wrapRPCCallForChain wraps a raw "SELECT * FROM fn(...)" call in a subquery
// so PostgREST-style filter/order/pagination can be appended on the outside.
// An empty chain (no filters, no order, no limit/offset, no embeds) passes
// through unchanged so the generated SQL is identical to the pre-chaining path.
// baseArgIdx is the first placeholder slot AFTER the function call's own
// arguments, and the chain is rendered with that numbering here — the
// caller is responsible for appending the matching args to its placeholder
// slice (they come out of parseRPCChain already in order).
func wrapRPCCallForChain(callSQL string, chain *rpcChainSQL, baseArgIdx int) (string, []any) {
	if chain == nil {
		return callSQL, nil
	}
	hasEmbeds := len(chain.embeds) > 0
	// When belongs-to embeds will JOIN onto _rpc below, qualify outer WHERE
	// columns with the _rpc alias so a filter like `id=eq.1` doesn't go
	// ambiguous against the joined table's `id`. Mirrors the same fix in
	// buildSelectQueryFull for direct CRUD queries.
	renderChain := chain
	if hasEmbeds && postgrest.HasBelongsToJoin(chain.embeds) && chain.where != nil {
		clone := *chain
		clone.where = postgrest.AliasWhereColumns(chain.where, "_rpc")
		renderChain = &clone
	}
	suffix, _ := renderRPCChain(renderChain, baseArgIdx)
	projection := "*"
	if len(chain.selectItems) > 0 {
		parts := make([]string, 0, len(chain.selectItems))
		for _, it := range chain.selectItems {
			parts = append(parts, postgrest.RenderSelectItem("_rpc", it))
		}
		projection = strings.Join(parts, ", ")
	}

	if suffix == "" && projection == "*" && !hasEmbeds {
		return callSQL, nil
	}

	// Build embed projections and JOINs.
	var embedArgs []any
	var joinClauses []string
	var embedSelectParts []string
	argIdx := baseArgIdx
	// Advance past the suffix args so embed placeholder numbering doesn't collide.
	if _, suffixArgs := renderRPCChain(renderChain, baseArgIdx); len(suffixArgs) > 0 {
		argIdx = baseArgIdx + len(suffixArgs)
	}

	for _, emb := range chain.embeds {
		alias := "_emb_" + emb.Name
		if emb.IsReverse {
			// Has-many: correlated scalar subselect with json_agg.
			rowExpr, rowArgs, nextIdx := postgrest.BuildEmbedRowExpr(emb, emb.RefTable, nil, argIdx)
			embedArgs = append(embedArgs, rowArgs...)
			argIdx = nextIdx

			refPK := emb.RefColumn
			if refPK == "" {
				refPK = "id"
			}
			sub := fmt.Sprintf("SELECT coalesce(json_agg(%s", rowExpr)
			if len(emb.Order) > 0 {
				sub += " ORDER BY " + postgrest.RenderOrderBy(emb.Order)
			}
			sub += fmt.Sprintf("), '[]'::json) FROM %s WHERE %s.%s = _rpc.%s",
				emb.RefTable, emb.RefTable, emb.FKColumn, refPK)
			if emb.Where != nil {
				clauseSQL, clauseArgs, next := emb.Where.BuildSQL(argIdx)
				if clauseSQL != "" {
					sub += " AND " + clauseSQL
					embedArgs = append(embedArgs, clauseArgs...)
					argIdx = next
				}
			}
			if emb.Limit != nil {
				sub += fmt.Sprintf(" LIMIT %d", *emb.Limit)
			}
			if emb.Offset != nil {
				sub += fmt.Sprintf(" OFFSET %d", *emb.Offset)
			}
			embedSelectParts = append(embedSelectParts, fmt.Sprintf("(%s) AS %s", sub, emb.Name))
		} else {
			// Belongs-to: LEFT/INNER JOIN.
			joinKind := "LEFT JOIN"
			if emb.Inner {
				joinKind = "INNER JOIN"
			}
			joinClauses = append(joinClauses, fmt.Sprintf("%s %s AS %s ON _rpc.%s = %s.%s",
				joinKind, emb.RefTable, alias, emb.FKColumn, alias, emb.RefColumn))

			if len(emb.Columns) == 0 {
				embedSelectParts = append(embedSelectParts,
					fmt.Sprintf("row_to_json(%s.*) AS %s", alias, emb.Name))
			} else {
				var embCols []string
				for _, c := range emb.Columns {
					embCols = append(embCols, fmt.Sprintf("'%s', %s.%s", c, alias, c))
				}
				embedSelectParts = append(embedSelectParts,
					fmt.Sprintf("json_build_object(%s) AS %s", strings.Join(embCols, ", "), emb.Name))
			}
		}
	}

	// Assemble the final projection.
	if len(embedSelectParts) > 0 {
		if projection == "*" {
			projection = "_rpc.*"
		}
		projection += ", " + strings.Join(embedSelectParts, ", ")
	}

	sql := "SELECT " + projection + " FROM (" + callSQL + ") AS _rpc"
	for _, j := range joinClauses {
		sql += " " + j
	}
	sql += suffix

	return sql, embedArgs
}

// buildOrderSQL serializes a list of OrderClauses into a PostgREST-compatible
// ORDER BY fragment. Columns have already been validated at parse time.
func buildOrderSQL(clauses []OrderClause) string {
	var parts []string
	for _, oc := range clauses {
		s := fmt.Sprintf(`"%s"`, oc.Column)
		if oc.Desc {
			s += " DESC"
		} else {
			s += " ASC"
		}
		switch oc.Nulls {
		case "first":
			s += " NULLS FIRST"
		case "last":
			s += " NULLS LAST"
		}
		parts = append(parts, s)
	}
	return "ORDER BY " + strings.Join(parts, ", ")
}

// executeRPCCount runs COUNT(*) over the filtered RPC subquery for clients
// that sent Prefer: count=exact. It rebuilds both the base call and the
// WHERE from scratch so the count statement gets its own $1.. numbering and
// never shares placeholder state with the data query. LIMIT/OFFSET are
// intentionally dropped: the total is the full filtered cardinality, not
// the size of the current page. The gin context is passed through so RLS
// can be bypassed for admin callers the same way the data query does.
func (h *CRUDHandler) executeRPCCount(c *gin.Context, name string, fn domain.Function, callArgs map[string]any, chain *rpcChainSQL, session domain.Session) (int, error) {
	callSQL, args := buildRPCCall(name, fn, callArgs)

	// Only the WHERE portion matters for the total; synthesize a chain
	// that carries the filter tree and nothing else.
	countChain := &rpcChainSQL{}
	if chain != nil {
		countChain.where = chain.where
	}
	suffix, whereArgs := renderRPCChain(countChain, len(args)+1)
	countSQL := fmt.Sprintf("SELECT COUNT(*) AS count FROM (%s) AS _rpc%s", callSQL, suffix)
	args = append(args, whereArgs...)

	dbCtx, err := h.db.WithRLS(c.Request.Context(), session)
	if err != nil {
		return -1, err
	}
	if isAdmin(c) {
		dbCtx = c.Request.Context()
	}
	row, err := h.db.QueryRow(dbCtx, countSQL, args...)
	if err != nil {
		return -1, err
	}
	if v, ok := row["count"]; ok {
		switch n := v.(type) {
		case int64:
			return int(n), nil
		case float64:
			return int(n), nil
		}
	}
	return -1, nil
}
