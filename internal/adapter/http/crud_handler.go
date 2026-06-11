package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
)

// CRUDHandler serves PostgREST-compatible table endpoints.
type CRUDHandler struct {
	cfg     *domain.Config
	db      domain.Database
	logger  *slog.Logger
	jwtKeys *app.JWTKeyManager
}

func NewCRUDHandler(deps ServerDeps) *CRUDHandler {
	return &CRUDHandler{
		cfg:     deps.Config,
		db:      deps.DB.Database,
		logger:  deps.Logger,
		jwtKeys: deps.JWTKeys,
	}
}

// allTables returns a map that includes both user-defined tables from cfg.Tables
// and a synthetic entry for the auth "users" table. This allows embed resolution
// to traverse FKs that reference the users table (e.g., comments → users).
func (h *CRUDHandler) allTables() map[string]domain.Table {
	merged := make(map[string]domain.Table, len(h.cfg.Tables)+1)
	for k, v := range h.cfg.Tables {
		merged[k] = v
	}
	// If auth is enabled but tables.users isn't declared, synthesize a
	// minimal users entry so FK resolution can still find id/email.
	if _, ok := merged["users"]; !ok && h.cfg.Auth != nil {
		merged["users"] = domain.Table{Fields: []domain.Field{
			{Name: "id", Type: "uuid", PrimaryKey: true},
			{Name: "email", Type: "text"},
		}}
	}
	return merged
}

// Mount registers PostgREST-compatible routes at /rest/v1/<table> so that
// @supabase/supabase-js can drive instancez without a custom URL prefix.
func (h *CRUDHandler) Mount(root *gin.RouterGroup) {
	rest := root.Group("/rest/v1")
	var schemas []string
	for _, t := range h.cfg.Tables {
		if s := t.EffectiveSchema(); s != "public" {
			schemas = append(schemas, s)
		}
	}
	rest.Use(profileHeaderGuard(schemas...))

	// /rest/v1/rpc/:name — supabase-js .rpc() dispatch. Only rpc-kind
	// functions are reachable; everything else returns
	// PGRST202 so a typo can't silently hit a different endpoint. POST is
	// always allowed; GET is only honored for non-volatile functions,
	// matching PostgREST. Auth is required=false at middleware level so
	// anon calls still parse a token if present; per-function
	// auth_required enforcement happens inside handleRPC.
	rpc := h.handleRPC()
	rest.POST("/rpc/:name", jwtAuth(h.jwtKeys, false), rpc)
	rest.GET("/rpc/:name", jwtAuth(h.jwtKeys, false), rpc)
	// HEAD reuses the same handler so supabase-js .rpc('fn', {}, { head: true })
	// picks up Content-Range without streaming the row body. As with the CRUD
	// list path, net/http strips the body after the status + headers fly.
	rest.HEAD("/rpc/:name", jwtAuth(h.jwtKeys, false), rpc)

	for tableName, table := range h.cfg.Tables {
		name := tableName
		t := table
		group := rest.Group("/" + name)
		// JWT not required at HTTP level — anon falls through with
		// session.Role="anon" and SQL-layer grants + RLS gate access.
		// Matches Supabase's PostgREST behavior.
		group.Use(jwtAuth(h.jwtKeys, false))

		list := h.handleList(name, t)
		group.GET("", list)
		// HEAD reuses the list handler: Gin/net/http will write the status
		// line and headers (Content-Range, Content-Type) but strip the body
		// so clients can fetch counts and pagination metadata cheaply.
		group.HEAD("", list)
		group.POST("", h.handleCreate(name, t))
		group.PUT("", h.handleUpsert(name, t))
		group.PATCH("", h.handleUpdate(name, t))
		group.DELETE("", h.handleDelete(name, t))
	}
}

// handleList handles GET /api/<table> with PostgREST query params.
func (h *CRUDHandler) handleList(tableName string, table domain.Table) gin.HandlerFunc {
	allTbls := h.allTables()
	return func(c *gin.Context) {
		session := getSession(c)

		prefer := joinPrefer(c)
		if !enforceStrictPrefer(c, prefer) {
			return
		}

		// Parse query params
		qp, err := parseQueryParams(c, tableName, table, allTbls)
		if err != nil {
			problemJSON(c, 400, "bad_request", err.Error())
			return
		}

		// Range header pagination. When the client sets Range: 0-9 (with
		// Range-Unit: items) and did not pass limit/offset explicitly, we
		// translate the byte-range-style bounds into limit/offset. A Range
		// header always yields a 206 response when any rows are returned
		// and the partial result does not cover the whole set.
		rangeUsed := false
		if rh := c.GetHeader("Range"); rh != "" && c.Query("limit") == "" && c.Query("offset") == "" {
			start, end, ok := parseRangeHeader(rh)
			if !ok {
				problemJSON(c, 400, "bad_request", "Invalid Range header")
				return
			}
			qp.Offset = start
			qp.Limit = end - start + 1
			rangeUsed = true
		}

		// Build SQL
		query, args := buildSelectQueryFull(tableName, qp, table, allTbls)

		// Set RLS context
		reqCtx := c.Request.Context()
		ctx, cancel := h.withDBTimeout(reqCtx, prefer)
		if cancel != nil {
			defer cancel()
		}
		ctx, err = h.db.WithRLS(ctx, session)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to set RLS context")
			return
		}

		// EXPLAIN plan response
		accept := c.GetHeader("Accept")
		if accept == "application/vnd.pgrst.plan+json" || accept == "application/vnd.pgrst.plan+text" {
			explainOpts := "FORMAT JSON"
			if strings.Contains(accept, "+text") {
				explainOpts = "FORMAT TEXT"
			}
			explainQuery := fmt.Sprintf("EXPLAIN (%s) %s", explainOpts, query)
			tx, err := h.db.Begin(ctx)
			if err != nil {
				problemJSON(c, 500, "internal", "Failed to start transaction")
				return
			}
			defer tx.Rollback(ctx)
			rows, err := tx.Query(ctx, explainQuery, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			tx.Commit(ctx)
			if strings.Contains(accept, "+text") {
				var lines []string
				for _, r := range rows {
					for _, v := range r {
						lines = append(lines, fmt.Sprintf("%v", v))
					}
				}
				c.Data(200, "application/vnd.pgrst.plan+text", []byte(strings.Join(lines, "\n")))
			} else {
				c.JSON(200, rows)
			}
			return
		}

		// Execute in transaction for RLS
		tx, err := h.db.Begin(ctx)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to start transaction")
			return
		}
		defer tx.Rollback(ctx)

		if schema, _ := c.Get("_schema"); schema != nil {
			if s, ok := schema.(string); ok && s != "" && s != "public" {
				tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", s))
			}
		}

		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			h.logger.Error("query error", "table", tableName, "error", err)
			handleDBError(c, err)
			return
		}

		if err := tx.Commit(ctx); err != nil {
			problemJSON(c, 500, "internal", "Transaction commit failed")
			return
		}

		// Count if requested
		countMode := parseCountPrefer(prefer)
		total := -1
		if countMode != "" {
			total, err = h.executeCount(ctx, tableName, qp, countMode)
			if err != nil {
				h.logger.Error("count error", "error", err)
			}
		}

		// Content-Range header
		offset := qp.Offset
		end := offset + len(rows) - 1
		if len(rows) == 0 {
			end = offset
		}
		if total >= 0 {
			c.Header("Content-Range", fmt.Sprintf("%d-%d/%d", offset, end, total))
		} else {
			c.Header("Content-Range", fmt.Sprintf("%d-%d/*", offset, end))
		}

		// GeoJSON response
		if accept == "application/geo+json" {
			geomCol := findGeometryColumn(table)
			if geomCol == "" {
				pgJSON(c, 406, "PGRST118", "No geometry column found for GeoJSON output", "", "")
				return
			}
			features := make([]map[string]any, 0, len(rows))
			for _, r := range rows {
				geom := r[geomCol]
				props := make(map[string]any, len(r)-1)
				for k, v := range r {
					if k != geomCol {
						props[k] = v
					}
				}
				features = append(features, map[string]any{
					"type":       "Feature",
					"geometry":   geom,
					"properties": props,
				})
			}
			c.JSON(200, gin.H{
				"type":     "FeatureCollection",
				"features": features,
			})
			return
		}

		// Check for singular response
		if accept == "application/vnd.pgrst.object+json" {
			if len(rows) != 1 {
				// supabase-js .maybeSingle() distinguishes zero-row from
				// multi-row errors by parsing the details string ("0 rows"
				// → return null, otherwise surface error). Emit PGRST116
				// for both branches so that contract holds.
				pgJSON(c, 406, "PGRST116",
					"JSON object requested, multiple (or no) rows returned",
					fmt.Sprintf("The result contains %d rows", len(rows)), "")
				return
			}
			c.JSON(200, rows[0])
			return
		}

		if rows == nil {
			rows = []map[string]any{}
		}

		// CSV response via Accept: text/csv.
		if acceptsCSV(accept) {
			out, err := csvRenderRows(rows)
			if err != nil {
				problemJSON(c, 500, "internal", "CSV render failed")
				return
			}
			status := 200
			if rangeUsed {
				if total < 0 || total > end+1 {
					status = 206
				}
			}
			c.Data(status, "text/csv; charset=utf-8", out)
			return
		}

		// Return 206 Partial Content when the client used Range and the
		// response is a strict subset of the available rows. We treat an
		// unknown total (no count prefer) as "might be partial" whenever
		// Range was used.
		status := 200
		if rangeUsed {
			if total < 0 || total > end+1 {
				status = 206
			}
		}
		c.JSON(status, rows)
	}
}

// handleCreate handles POST /api/<table>.
func (h *CRUDHandler) handleCreate(tableName string, table domain.Table) gin.HandlerFunc {
	return func(c *gin.Context) {
		session := getSession(c)
		prefer := joinPrefer(c)
		if !enforceStrictPrefer(c, prefer) {
			return
		}

		records, err := parseRequestBody(c, table)
		if err != nil {
			return
		}

		allowedCols, err := parseColumnsParam(c.Query("columns"), table)
		if err != nil {
			problemJSON(c, 400, "bad_request", err.Error())
			return
		}
		records = filterRecordsByColumns(records, allowedCols)
		if recordsAllEmpty(records) {
			problemJSON(c, 400, "bad_request", "No insertable columns after applying columns= filter")
			return
		}

		fieldMap := table.FieldMap()
		for _, rec := range records {
			if unknowns := findUnknownFields(rec, fieldMap); len(unknowns) > 0 {
				problemJSON(c, 400, "bad_request",
					fmt.Sprintf("Unknown fields: %s", strings.Join(unknowns, ", ")))
				return
			}
		}

		ctx, tx, rollback, err := setupMutationTx(c, h.db, session)
		if err != nil {
			return
		}
		defer rollback(ctx)

		returnMode := parseReturnPrefer(prefer)
		resolution := parseResolutionPrefer(prefer)
		var results []map[string]any

		var pkCols []string
		if resolution != "" {
			customCols, err := parseOnConflictParam(c.Query("on_conflict"), table)
			if err != nil {
				problemJSON(c, 400, "bad_request", err.Error())
				return
			}
			if len(customCols) > 0 {
				pkCols = customCols
			} else {
				pkCols = primaryKeyColumns(table)
			}
			if len(pkCols) == 0 {
				problemJSON(c, 400, "bad_request", "Cannot upsert: table has no primary key and no on_conflict")
				return
			}
		}

		var query string
		var args []any
		if resolution != "" {
			query, args = buildBulkUpsertQuery(tableName, records, pkCols, resolution, returnMode == "representation")
		} else {
			query, args = buildBulkInsertQuery(tableName, records, returnMode == "representation")
		}

		if returnMode == "representation" {
			rows, err := tx.Query(ctx, query, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			results = rows
		} else {
			if _, err := tx.Exec(ctx, query, args...); err != nil {
				handleDBError(c, err)
				return
			}
		}

		if !finishTx(c, tx, ctx, parseTxPrefer(prefer)) {
			return
		}
		if parseMissingPrefer(prefer) {
			c.Header("Preference-Applied", "missing=default")
		}
		writeMutationResponse(c, 201, returnMode, results)
	}
}

// handleUpsert handles PUT /api/<table> as an upsert on the primary key.
// Body may be a single object or an array. Always uses merge semantics:
// matching rows are updated, others are inserted.
func (h *CRUDHandler) handleUpsert(tableName string, table domain.Table) gin.HandlerFunc {
	return func(c *gin.Context) {
		session := getSession(c)
		prefer := joinPrefer(c)
		if !enforceStrictPrefer(c, prefer) {
			return
		}

		records, err := parseRequestBody(c, table)
		if err != nil {
			return
		}

		pkCols := primaryKeyColumns(table)
		if len(pkCols) == 0 {
			problemJSON(c, 400, "bad_request", "Cannot upsert: table has no primary key")
			return
		}

		upsertFieldMap := table.FieldMap()
		for _, rec := range records {
			if unknowns := findUnknownFields(rec, upsertFieldMap); len(unknowns) > 0 {
				problemJSON(c, 400, "bad_request",
					fmt.Sprintf("Unknown fields: %s", strings.Join(unknowns, ", ")))
				return
			}
			// PUT semantics: each record must carry all PK columns so the
			// server can locate the target row deterministically.
			for _, pk := range pkCols {
				if _, ok := rec[pk]; !ok {
					problemJSON(c, 400, "bad_request",
						fmt.Sprintf("PUT body missing primary key column %q", pk))
					return
				}
			}
		}

		ctx, tx, rollback, err := setupMutationTx(c, h.db, session)
		if err != nil {
			return
		}
		defer rollback(ctx)

		returnMode := parseReturnPrefer(prefer)
		var results []map[string]any

		query, args := buildBulkUpsertQuery(tableName, records, pkCols, "merge", returnMode == "representation")
		if returnMode == "representation" {
			rows, err := tx.Query(ctx, query, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			results = rows
		} else {
			if _, err := tx.Exec(ctx, query, args...); err != nil {
				handleDBError(c, err)
				return
			}
		}

		if !finishTx(c, tx, ctx, parseTxPrefer(prefer)) {
			return
		}
		if parseMissingPrefer(prefer) {
			c.Header("Preference-Applied", "missing=default")
		}
		writeMutationResponse(c, 200, returnMode, results)
	}
}

// handleUpdate handles PATCH /api/<table>.
func (h *CRUDHandler) handleUpdate(tableName string, table domain.Table) gin.HandlerFunc {
	return func(c *gin.Context) {
		session := getSession(c)

		prefer := joinPrefer(c)
		if !enforceStrictPrefer(c, prefer) {
			return
		}

		var updates map[string]any
		if err := c.ShouldBindJSON(&updates); err != nil {
			problemJSON(c, 400, "bad_request", "Invalid JSON body")
			return
		}

		if len(updates) == 0 {
			problemJSON(c, 400, "bad_request", "Empty update body")
			return
		}

		if unknowns := findUnknownFields(updates, table.FieldMap()); len(unknowns) > 0 {
			problemJSON(c, 400, "bad_request",
				fmt.Sprintf("Unknown fields: %s", strings.Join(unknowns, ", ")))
			return
		}

		where, err := parseWhere(c, tableName, table)
		if err != nil {
			problemJSON(c, 400, "bad_request", err.Error())
			return
		}

		ctx, tx, rollback, err := setupMutationTx(c, h.db, session)
		if err != nil {
			return
		}
		defer rollback(ctx)

		returnMode := parseReturnPrefer(prefer)
		maxAffected, hasMax := parseMaxAffectedPrefer(prefer)

		query, args := buildUpdateQuery(tableName, updates, where, returnMode == "representation")

		if returnMode == "representation" {
			rows, err := tx.Query(ctx, query, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			if hasMax && len(rows) > maxAffected {
				maxAffectedError(c, len(rows), maxAffected)
				return
			}
			if !finishTx(c, tx, ctx, parseTxPrefer(prefer)) {
				return
			}
			c.JSON(200, rows)
		} else {
			affected, err := tx.Exec(ctx, query, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			if hasMax && int(affected) > maxAffected {
				maxAffectedError(c, int(affected), maxAffected)
				return
			}
			if !finishTx(c, tx, ctx, parseTxPrefer(prefer)) {
				return
			}
			if returnMode == "headers-only" {
				c.Header("Preference-Applied", "return=headers-only")
				c.Status(200)
			} else {
				c.Status(204)
			}
		}
	}
}

// handleDelete handles DELETE /api/<table>.
func (h *CRUDHandler) handleDelete(tableName string, table domain.Table) gin.HandlerFunc {
	return func(c *gin.Context) {
		session := getSession(c)

		prefer := joinPrefer(c)
		if !enforceStrictPrefer(c, prefer) {
			return
		}

		where, err := parseWhere(c, tableName, table)
		if err != nil {
			problemJSON(c, 400, "bad_request", err.Error())
			return
		}

		ctx, err := h.db.WithRLS(c.Request.Context(), session)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to set RLS context")
			return
		}

		tx, err := h.db.Begin(ctx)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to start transaction")
			return
		}
		defer tx.Rollback(ctx)

		returnMode := parseReturnPrefer(prefer)
		maxAffected, hasMax := parseMaxAffectedPrefer(prefer)
		query, args := buildDeleteQuery(tableName, where, returnMode == "representation")

		if returnMode == "representation" {
			rows, err := tx.Query(ctx, query, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			if hasMax && len(rows) > maxAffected {
				maxAffectedError(c, len(rows), maxAffected)
				return
			}
			if !finishTx(c, tx, ctx, parseTxPrefer(prefer)) {
				return
			}
			if c.GetHeader("Accept") == "application/vnd.pgrst.object+json" && len(rows) == 1 {
				c.JSON(200, rows[0])
				return
			}
			if rows == nil {
				rows = []map[string]any{}
			}
			c.JSON(200, rows)
			return
		}

		affected, err := tx.Exec(ctx, query, args...)
		if err != nil {
			handleDBError(c, err)
			return
		}
		if hasMax && int(affected) > maxAffected {
			maxAffectedError(c, int(affected), maxAffected)
			return
		}

		if !finishTx(c, tx, ctx, parseTxPrefer(prefer)) {
			return
		}

		if returnMode == "headers-only" {
			c.Header("Preference-Applied", "return=headers-only")
			c.Status(200)
			return
		}
		c.Status(204)
	}
}

func (h *CRUDHandler) executeCount(ctx interface{ Value(any) any }, tableName string, qp *QueryParams, mode string) (int, error) {
	switch mode {
	case "exact":
		return h.executeExactCount(ctx.(context.Context), tableName, qp)
	case "planned":
		return h.executePlannedCount(ctx.(context.Context), tableName, qp)
	case "estimated":
		// Use planned count if filters exist, otherwise use reltuples
		if qp.Where != nil {
			return h.executePlannedCount(ctx.(context.Context), tableName, qp)
		}
		return h.executeEstimateCount(ctx.(context.Context), tableName)
	default:
		return -1, nil
	}
}

func (h *CRUDHandler) executeExactCount(ctx context.Context, tableName string, qp *QueryParams) (int, error) {
	sql := fmt.Sprintf("SELECT COUNT(*) AS count FROM %s", tableName)
	whereSQL, args, _ := qp.Where.buildSQL(1)
	if whereSQL != "" {
		sql += " WHERE " + whereSQL
	}

	row, err := h.db.QueryRow(ctx, sql, args...)
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

func (h *CRUDHandler) executePlannedCount(ctx context.Context, tableName string, qp *QueryParams) (int, error) {
	innerSQL := fmt.Sprintf("SELECT 1 FROM %s", tableName)
	whereSQL, args, _ := qp.Where.buildSQL(1)
	if whereSQL != "" {
		innerSQL += " WHERE " + whereSQL
	}

	explainSQL := "EXPLAIN " + innerSQL
	rows, err := h.db.Query(ctx, explainSQL, args...)
	if err != nil {
		return -1, err
	}

	// Parse the first row's QUERY PLAN for "rows=N"
	if len(rows) > 0 {
		for _, v := range rows[0] {
			if plan, ok := v.(string); ok {
				if idx := strings.Index(plan, "rows="); idx != -1 {
					numStr := plan[idx+5:]
					if spaceIdx := strings.IndexAny(numStr, " )"); spaceIdx != -1 {
						numStr = numStr[:spaceIdx]
					}
					if n, err := strconv.Atoi(numStr); err == nil {
						return n, nil
					}
				}
			}
		}
	}

	return -1, nil
}

func (h *CRUDHandler) executeEstimateCount(ctx context.Context, tableName string) (int, error) {
	row, err := h.db.QueryRow(ctx,
		"SELECT reltuples::bigint AS count FROM pg_class WHERE relname = $1", tableName)
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

// findUnknownFields returns field names not in the provided field map.
func findUnknownFields(record map[string]any, fieldMap map[string]domain.Field) []string {
	var unknowns []string
	for key := range record {
		if _, ok := fieldMap[key]; !ok {
			unknowns = append(unknowns, key)
		}
	}
	sort.Strings(unknowns)
	return unknowns
}

// joinPrefer concatenates all Prefer header values on the request into a
// single comma-separated string. RFC 7240 allows both multi-header form
// ("Prefer: a" + "Prefer: b") and comma-joined form ("Prefer: a, b");
// gin's c.GetHeader only returns the first value, which loses directives
// from clients like postgrest-js/go that emit one header per preference.
func joinPrefer(c *gin.Context) string {
	return strings.Join(c.Request.Header.Values("Prefer"), ",")
}

func findPreferDirective(prefer, key string) (string, bool) {
	prefix := key + "="
	for _, part := range strings.Split(prefer, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, prefix) {
			return strings.TrimPrefix(part, prefix), true
		}
	}
	return "", false
}

func parseReturnPrefer(prefer string) string {
	if v, ok := findPreferDirective(prefer, "return"); ok {
		return v
	}
	return "minimal"
}

// finishTx commits or (on Prefer: tx=rollback) rolls back the transaction.
// Returns true when the caller should proceed to write the normal response
// and false when an error has already been written.
func finishTx(c *gin.Context, tx domain.Tx, ctx context.Context, txMode string) bool {
	if txMode == "rollback" {
		_ = tx.Rollback(ctx)
		return true
	}
	if err := tx.Commit(ctx); err != nil {
		problemJSON(c, 500, "internal", "Transaction commit failed")
		return false
	}
	return true
}

// parseTxPrefer extracts the transaction disposition from a Prefer header.
// Returns "rollback" or "commit" if a recognized tx= directive is present,
// otherwise "". Callers use "rollback" to execute a dry-run: the query is
// issued but its effects are discarded on transaction rollback.
func parseTxPrefer(prefer string) string {
	if v, ok := findPreferDirective(prefer, "tx"); ok && (v == "rollback" || v == "commit") {
		return v
	}
	return ""
}

// parseMaxAffectedPrefer extracts `max-affected=N` from a Prefer header.
// Returns (n, true) on a well-formed positive integer; (0, false) otherwise.
// Callers use this to abort PATCH/DELETE that would affect more than N rows
// (PostgREST 13 guard against accidentally broad mutations).
// maxAffectedError writes the PostgREST error for a mutation that affected
// more rows than `Prefer: max-affected=N` permitted. The surrounding tx is
// rolled back by the deferred tx.Rollback in the caller.
func maxAffectedError(c *gin.Context, affected, limit int) {
	pgJSON(c, 400, "PGRST124",
		fmt.Sprintf("Query result exceeds max-affected preference constraint"),
		fmt.Sprintf("The query affects %d rows, exceeding the max-affected=%d limit", affected, limit),
		"")
}

func parseMaxAffectedPrefer(prefer string) (int, bool) {
	raw, ok := findPreferDirective(prefer, "max-affected")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// parseHandlingPrefer extracts `handling=lenient` or `handling=strict` from
// a Prefer header. Defaults to "lenient" (PostgREST's default). In strict
// mode the server rejects any Prefer directive it does not recognize with
// a PGRST122 error; enforceStrictPrefer performs that check.
func parseHandlingPrefer(prefer string) string {
	if v, ok := findPreferDirective(prefer, "handling"); ok && (v == "strict" || v == "lenient") {
		return v
	}
	return "lenient"
}

// knownPreferDirectives lists every Prefer token Instancez recognizes.
// In handling=strict mode we reject any incoming directive not in this set.
var knownPreferDirectives = map[string]bool{
	"return":       true,
	"count":        true,
	"resolution":   true,
	"tx":           true,
	"missing":      true,
	"max-affected": true,
	"handling":     true,
	"params":       true,
}

// enforceStrictPrefer checks every directive in the Prefer header when
// handling=strict is set and writes a PGRST122 error for the first unknown
// token. Returns false when the request has been aborted.
func enforceStrictPrefer(c *gin.Context, prefer string) bool {
	if parseHandlingPrefer(prefer) != "strict" {
		return true
	}
	for _, part := range strings.Split(prefer, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := part
		if eq := strings.Index(part, "="); eq != -1 {
			key = part[:eq]
		}
		if !knownPreferDirectives[key] {
			pgJSON(c, 400, "PGRST122",
				"Invalid preference",
				fmt.Sprintf("Unknown Prefer directive %q under handling=strict", part),
				"")
			return false
		}
	}
	return true
}

func parseCountPrefer(prefer string) string {
	v, _ := findPreferDirective(prefer, "count")
	return v
}

// parseMissingPrefer extracts `missing=default` from a Prefer header.
// Returns true when the client explicitly asked for the default-substitution
// behavior for omitted columns on bulk insert. Instancez already emits
// DEFAULT for missing keys unconditionally (see renderRowTuples), so this
// is only used to echo Preference-Applied for clients that probe for it.
func parseMissingPrefer(prefer string) bool {
	v, ok := findPreferDirective(prefer, "missing")
	return ok && v == "default"
}

// handleDBError maps Postgres errors to PostgREST-compatible HTTP errors.
// When the underlying error is a *pgconn.PgError we forward the real SQLSTATE
// code plus the detail/hint fields; otherwise we fall back to substring
// matching on the error message for wrapped/non-pgx drivers.
func handleDBError(c *gin.Context, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		status := 500
		switch pgErr.Code {
		case "23505": // unique_violation
			status = 409
		case "23503", // foreign_key_violation
			"23502", // not_null_violation
			"23514", // check_violation
			"22P02": // invalid_text_representation
			status = 422
		case "42501": // insufficient_privilege
			status = 403
		case "42P01", // undefined_table
			"42703": // undefined_column
			status = 404
		case "22001": // string_data_right_truncation
			status = 422
		case "22003": // numeric_value_out_of_range
			status = 422
		case "22007", // invalid_datetime_format
			"22008": // datetime_field_overflow
			status = 422
		case "23000": // integrity_constraint_violation (generic)
			status = 409
		case "25006": // read_only_sql_transaction
			status = 405
		case "42601": // syntax_error
			status = 400
		case "42602": // invalid_name
			status = 400
		case "42883": // undefined_function
			status = 404
		case "P0001": // raise_exception (user-defined)
			// User-defined PL/pgSQL RAISE maps to 400 by default; the
			// hint field often carries application-specific guidance.
			status = 400
		}
		// RLS policy violations surface as various SQLSTATEs depending on
		// Postgres version; catch the common phrases too.
		if strings.Contains(pgErr.Message, "row-level security") ||
			strings.Contains(pgErr.Message, "new row violates") {
			status = 403
		}
		hint := pgErr.Hint
		if hint == "" {
			hint = suggestHintForPgError(pgErr)
		}
		pgJSON(c, status, pgErr.Code, pgErr.Message, pgErr.Detail, hint)
		return
	}

	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "unique") || strings.Contains(errStr, "duplicate key"):
		pgJSON(c, 409, "23505", errStr, "", "")
	case strings.Contains(errStr, "foreign key"):
		pgJSON(c, 422, "23503", errStr, "", "")
	case strings.Contains(errStr, "violates check"):
		pgJSON(c, 422, "23514", errStr, "", "")
	case strings.Contains(errStr, "not-null constraint"):
		pgJSON(c, 422, "23502", errStr, "", "")
	case strings.Contains(errStr, "row-level security") || strings.Contains(errStr, "new row violates"):
		pgJSON(c, 403, "42501", "Access denied by row-level security policy", "", "")
	case strings.Contains(errStr, "permission denied"):
		pgJSON(c, 403, "42501", errStr, "", "")
	case strings.Contains(errStr, "value too long"):
		pgJSON(c, 422, "22001", errStr, "", "")
	case strings.Contains(errStr, "invalid input syntax"):
		pgJSON(c, 422, "22P02", errStr, "", "Check that the value matches the expected column type")
	default:
		pgJSON(c, 500, "XX000", "Database error", errStr, "")
	}
}

// suggestHintForPgError generates a hint string for common Postgres errors
// when the database itself didn't provide one. This helps API consumers
// understand what went wrong without having to look up SQLSTATE codes.
func suggestHintForPgError(pgErr *pgconn.PgError) string {
	switch pgErr.Code {
	case "23505":
		if pgErr.ConstraintName != "" {
			return fmt.Sprintf("A record with the same value already exists (constraint: %s)", pgErr.ConstraintName)
		}
		return "A record with the same value already exists"
	case "23503":
		if pgErr.ConstraintName != "" {
			return fmt.Sprintf("The referenced record does not exist (constraint: %s)", pgErr.ConstraintName)
		}
		return "The referenced record does not exist"
	case "23502":
		if pgErr.ColumnName != "" {
			return fmt.Sprintf("Column %q cannot be null", pgErr.ColumnName)
		}
		return "A required column is missing or null"
	case "23514":
		if pgErr.ConstraintName != "" {
			return fmt.Sprintf("The value violates a check constraint (constraint: %s)", pgErr.ConstraintName)
		}
		return "The value does not satisfy a check constraint"
	case "42703":
		if pgErr.ColumnName != "" {
			return fmt.Sprintf("Column %q does not exist on this table", pgErr.ColumnName)
		}
		return "The specified column does not exist"
	case "42P01":
		return "The specified table does not exist in the schema"
	case "22P02":
		return "Check that the value matches the expected column type"
	case "22001":
		return "The value is too long for this column"
	case "P0001":
		return "A server-side function raised an error"
	}
	return ""
}

// --- SQL Query Building ---

// Embed represents a relation embed in the select clause (e.g., author(id,name)).
type Embed struct {
	Name      string   // relation name (e.g., "author")
	Alias     string   // optional output key from "alias:name" prefix (PostgREST)
	Columns   []string // columns to select from the related table (empty = *)
	FKColumn  string   // the FK column on this table (e.g., "author_id")
	RefTable  string   // the referenced table (e.g., "users")
	RefColumn string   // the referenced column (e.g., "id")
	IsReverse bool     // true for has-many (reverse FK)
	Inner     bool     // true → INNER JOIN hint (!inner)
	Spread    bool     // true → flatten columns into parent row (... prefix)
	Children  []Embed  // nested embeds (e.g., author(*, posts(*)))

	// Nested (PostgREST) scoping applied via "embedname.<...>" query params.
	// Where applies to either side; Order and Limit are only valid for
	// has-many (reverse) embeds where the result is a row set.
	Where  *WhereNode
	Order  []OrderClause
	Limit  *int
	Offset *int
}

// outputKey returns the JSON object key for this embed in the response, and
// the SQL alias seed used internally. Aliased embeds (`category:categories`)
// surface under the alias; otherwise the relation Name is used.
func (e Embed) outputKey() string {
	if e.Alias != "" {
		return e.Alias
	}
	return e.Name
}

// QueryParams holds parsed PostgREST query parameters.
type QueryParams struct {
	Select []string
	Embeds []Embed
	Where  *WhereNode
	Having *WhereNode // HAVING clause filters applied after GROUP BY
	Order  []OrderClause
	Limit  int
	Offset int
}

type Filter struct {
	Column   string
	Operator string
	Value    string
	// Config carries an inline parameter parsed from operator syntax like
	// `fts(english).query`. Currently only used by the FTS family to
	// choose the Postgres text-search configuration at query time.
	Config string
}

type OrderClause struct {
	Column string
	Desc   bool
	Nulls  string // "", "first", or "last"
	// IsAlias marks clauses whose Column refers to a select-list alias
	// (explicit or the default key of an aggregate like "count") rather
	// than a real table column. Such clauses skip column validation at
	// parse time and are emitted as a double-quoted identifier so
	// Postgres resolves them from the output list — matching PostgREST's
	// behaviour where `order=count.desc` sorts by the aggregate selected
	// as `...,id.count()`.
	IsAlias bool
}

func parseQueryParams(c *gin.Context, tableName string, table domain.Table, allTables map[string]domain.Table) (*QueryParams, error) {
	qp := &QueryParams{
		Limit:  20, // default
		Offset: 0,
	}

	// Parse select
	if sel := c.Query("select"); sel != "" {
		qp.Select = parseSelectParam(sel)
		for _, s := range qp.Select {
			if strings.Contains(s, "(") && !isAggSelectEntry(s) {
				continue // embed — validated via resolveEmbeds
			}
			item := parseSelectItem(s)
			if err := validateSelectItem(table, item); err != nil {
				return nil, fmt.Errorf("invalid select: %w", err)
			}
		}
	}

	// Resolve embed specs now so later parsing can route embed-scoped query
	// parameters (<embed>.col=op.val, <embed>.order, <embed>.limit).
	if allTables != nil {
		var embedParams []string
		for _, s := range qp.Select {
			if strings.Contains(s, "(") && !isAggSelectEntry(s) {
				embedParams = append(embedParams, s)
			}
		}
		if len(embedParams) > 0 {
			resolved, err := resolveEmbeds(tableName, table, embedParams, allTables)
			if err != nil {
				return nil, fmt.Errorf("invalid embed: %w", err)
			}
			qp.Embeds = resolved
		}
	}
	embedByName := map[string]*Embed{}
	for i := range qp.Embeds {
		embedByName[qp.Embeds[i].outputKey()] = &qp.Embeds[i]
	}

	// Parse order. Format per PostgREST: col[.asc|.desc][.nullsfirst|.nullslast].
	// Select-list aliases (including aggregate default keys) are resolved
	// first so `order=count.desc` works against `select=...,id.count()`.
	if order := c.Query("order"); order != "" {
		clauses, err := parseOrderValueWithSelect(order, table, qp.Select)
		if err != nil {
			return nil, fmt.Errorf("invalid order: %w", err)
		}
		qp.Order = clauses
	}

	// Parse limit
	if l := c.Query("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid limit: %s", l)
		}
		qp.Limit = n
	}

	// Parse offset
	if o := c.Query("offset"); o != "" {
		n, err := strconv.Atoi(o)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid offset: %s", o)
		}
		qp.Offset = n
	}

	// Parse embed-scoped params (<embed>.col=op.val, <embed>.order, <embed>.limit)
	// before the outer WHERE parse so those keys don't get routed to the
	// outer filter.
	if len(embedByName) > 0 {
		if err := parseEmbedScopedParams(c, embedByName, allTables); err != nil {
			return nil, err
		}
	}
	skipPrefixes := make(map[string]bool, len(embedByName))
	for name := range embedByName {
		skipPrefixes[name] = true
	}

	// Parse filters (including or/and/not logic trees)
	where, err := parseWhereSkip(c, tableName, table, skipPrefixes)
	if err != nil {
		return nil, err
	}
	qp.Where = where

	// Parse HAVING. Format mirrors filter syntax but conditions must reference
	// aggregate aliases or columns that will appear in GROUP BY. The having
	// parameter uses the same col.op.value syntax as filters:
	//   ?having=count.gt.5
	//   ?having=sum.gte.100
	if havingRaw := c.Query("having"); havingRaw != "" {
		havingNode, err := parseHavingParam(havingRaw, tableName, table, qp.Select)
		if err != nil {
			return nil, fmt.Errorf("invalid having: %w", err)
		}
		qp.Having = havingNode
	}

	return qp, nil
}

// parseOrderValue parses a comma-separated PostgREST order list into
// OrderClauses. Each entry is col[.asc|.desc][.nullsfirst|.nullslast];
// modifiers are stripped right-to-left so the remaining prefix is the
// column name (default ASC, server default nulls order).
func parseOrderValue(val string, table domain.Table) ([]OrderClause, error) {
	return parseOrderValueWith(val, func(col string) error { return validateColumn(table, col) })
}

// parseOrderValueWithSelect is parseOrderValue with knowledge of the
// select list, so order tokens that match a select alias (explicit or
// the default key of an aggregate) bypass column validation and are
// emitted as quoted output-list references in the generated SQL. This
// mirrors PostgREST, where `order=count.desc` against a select of
// `...,id.count()` sorts by the aggregate output rather than failing
// with "unknown column".
func parseOrderValueWithSelect(val string, table domain.Table, selectItems []string) ([]OrderClause, error) {
	aliases := collectSelectAliases(selectItems)
	validate := func(col string) error {
		if _, ok := aliases[col]; ok {
			return nil
		}
		return validateColumn(table, col)
	}
	clauses, err := parseOrderValueWith(val, validate)
	if err != nil {
		return nil, err
	}
	for i := range clauses {
		if _, ok := aliases[clauses[i].Column]; ok {
			clauses[i].IsAlias = true
		}
	}
	return clauses, nil
}

// collectSelectAliases returns the set of output-list names produced by
// a parsed select list: explicit aliases (`nick:name`) and the default
// alias key for aggregates without an explicit alias (`id.count()` →
// "count"). Embed entries are skipped.
func collectSelectAliases(selectItems []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, s := range selectItems {
		if strings.Contains(s, "(") && !isAggSelectEntry(s) {
			continue
		}
		item := parseSelectItem(s)
		switch {
		case item.Alias != "":
			out[item.Alias] = struct{}{}
		case item.Agg != "":
			out[item.Agg] = struct{}{}
		}
	}
	return out
}

// parseOrderValueWith is parseOrderValue with a pluggable column validator,
// used by the RPC path when there is no single domain.Table to validate against.
func parseOrderValueWith(val string, validate colValidator) ([]OrderClause, error) {
	var out []OrderClause
	for _, part := range strings.Split(val, ",") {
		part = strings.TrimSpace(part)
		oc := OrderClause{}
		for {
			switch {
			case strings.HasSuffix(part, ".nullsfirst"):
				oc.Nulls = "first"
				part = strings.TrimSuffix(part, ".nullsfirst")
				continue
			case strings.HasSuffix(part, ".nullslast"):
				oc.Nulls = "last"
				part = strings.TrimSuffix(part, ".nullslast")
				continue
			case strings.HasSuffix(part, ".desc"):
				oc.Desc = true
				part = strings.TrimSuffix(part, ".desc")
				continue
			case strings.HasSuffix(part, ".asc"):
				part = strings.TrimSuffix(part, ".asc")
				continue
			}
			break
		}
		oc.Column = part
		if err := validate(oc.Column); err != nil {
			return nil, err
		}
		out = append(out, oc)
	}
	return out, nil
}

// parseEmbedScopedParams routes "<embed>.*" query parameters into the
// corresponding Embed's Where/Order/Limit fields. The embed table is
// used to validate columns. Order and Limit are only allowed for has-many
// (reverse) embeds — belongs-to embeds are joined to a single row, so
// ordering or limiting the join has no meaning.
func parseEmbedScopedParams(c *gin.Context, embedByName map[string]*Embed, allTables map[string]domain.Table) error {
	for key, values := range c.Request.URL.Query() {
		for embName, emb := range embedByName {
			prefix := embName + "."
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			suffix := strings.TrimPrefix(key, prefix)
			embTable, ok := allTables[emb.RefTable]
			if !ok {
				return fmt.Errorf("embed %q references unknown table %q", embName, emb.RefTable)
			}
			// Embed-scoped filters and order may reference select-list
			// aliases (explicit or aggregate defaults) in the embed's own
			// projection, e.g. `posts.count.gt=0` against `posts(id.count())`.
			aliasSet := collectSelectAliases(emb.Columns)
			embValidate := func(col string) error {
				if _, ok := aliasSet[col]; ok {
					return nil
				}
				return validateColumn(embTable, col)
			}
			switch suffix {
			case "or", "and":
				for _, v := range values {
					node, err := parseLogicListWith(suffix, v, embValidate)
					if err != nil {
						return fmt.Errorf("invalid %s for embed %q: %w", suffix, embName, err)
					}
					if emb.Where == nil {
						emb.Where = &WhereNode{Op: "and"}
					}
					emb.Where.Children = append(emb.Where.Children, node)
				}
			case "order":
				if !emb.IsReverse {
					return fmt.Errorf("order not allowed on belongs-to embed %q", embName)
				}
				for _, v := range values {
					clauses, err := parseOrderValueWithSelect(v, embTable, emb.Columns)
					if err != nil {
						return fmt.Errorf("invalid order for embed %q: %w", embName, err)
					}
					emb.Order = append(emb.Order, clauses...)
				}
			case "limit":
				if !emb.IsReverse {
					return fmt.Errorf("limit not allowed on belongs-to embed %q", embName)
				}
				if len(values) == 0 {
					break
				}
				n, err := strconv.Atoi(values[0])
				if err != nil || n < 0 {
					return fmt.Errorf("invalid limit for embed %q: %s", embName, values[0])
				}
				emb.Limit = &n
			case "offset":
				if !emb.IsReverse {
					return fmt.Errorf("offset not allowed on belongs-to embed %q", embName)
				}
				if len(values) == 0 {
					break
				}
				n, err := strconv.Atoi(values[0])
				if err != nil || n < 0 {
					return fmt.Errorf("invalid offset for embed %q: %s", embName, values[0])
				}
				emb.Offset = &n
			case "select":
				// Reserved name inside an embed scope: column projection is
				// controlled by the parentheses syntax (posts(col1,col2))
				// rather than a separate posts.select= param.
				return fmt.Errorf("embed param %q.%s not supported", embName, suffix)
			default:
				if err := validateColumn(embTable, suffix); err != nil {
					return fmt.Errorf("invalid filter on embed %q: %w", embName, err)
				}
				for _, v := range values {
					leaf, err := parseLeafValue(suffix, v)
					if err != nil {
						return fmt.Errorf("invalid filter on embed %q: %w", embName, err)
					}
					if emb.Where == nil {
						emb.Where = &WhereNode{Op: "and"}
					}
					emb.Where.Children = append(emb.Where.Children, leaf)
				}
			}
			break
		}
	}
	return nil
}

// hasBelongsToJoin reports whether any embed will produce a JOIN on the
// outer FROM clause. Reverse (has-many) embeds become correlated subselects
// and don't widen the outer scope; belongs-to embeds do.
func hasBelongsToJoin(embeds []Embed) bool {
	for _, e := range embeds {
		if !e.IsReverse {
			return true
		}
	}
	return false
}

// qualifyOrderColumns prefixes each non-alias ORDER clause column with
// "<table>." so it's unambiguous against joined embed columns. IsAlias
// clauses resolve against the SELECT output list and are left untouched.
func qualifyOrderColumns(clauses []OrderClause, tableName string) []OrderClause {
	if len(clauses) == 0 {
		return clauses
	}
	out := make([]OrderClause, len(clauses))
	for i, oc := range clauses {
		out[i] = oc
		if !oc.IsAlias && !strings.Contains(oc.Column, ".") {
			out[i].Column = tableName + "." + oc.Column
		}
	}
	return out
}

// aliasWhereColumns returns a clone of n with every leaf column prefixed
// with "<alias>.". Used to qualify belongs-to embed filters against the
// join alias when they're emitted into the outer WHERE clause.
func aliasWhereColumns(n *WhereNode, alias string) *WhereNode {
	if n == nil {
		return nil
	}
	if n.Leaf != nil {
		f := *n.Leaf
		f.Column = alias + "." + f.Column
		return &WhereNode{Leaf: &f, Not: n.Not}
	}
	clone := &WhereNode{Op: n.Op, Not: n.Not}
	for _, c := range n.Children {
		clone.Children = append(clone.Children, aliasWhereColumns(c, alias))
	}
	return clone
}

// reservedParams are non-filter query parameters. The or/and keys are not
// listed here because they are handled explicitly in parseWhere's switch.
var reservedParams = map[string]bool{
	"select": true,
	"order":  true,
	"limit":  true,
	"offset": true,
	"having": true,
}

// jsonKeyRe restricts JSONB path keys to safe identifiers. Keys are
// interpolated into SQL as single-quoted literals, so we refuse anything
// that could break out of the quoting.
var jsonKeyRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// validateColumn ensures col references a field declared on the table.
// JSONB access expressions (e.g. "metadata->>theme") are accepted when the
// base column exists and the path key is a safe identifier.
func validateColumn(table domain.Table, col string) error {
	if col == "" {
		return fmt.Errorf("empty column name")
	}
	base, steps := splitJSONBPath(col)
	if _, ok := table.GetField(base); !ok {
		return fmt.Errorf("unknown column %q", base)
	}
	for _, s := range steps {
		if s.key == "" {
			return fmt.Errorf("empty JSONB key in %q", col)
		}
		if !s.isInt && !jsonKeyRe.MatchString(s.key) {
			return fmt.Errorf("invalid JSONB key %q", s.key)
		}
	}
	return nil
}

var validOps = map[string]string{
	"eq":         "=",
	"neq":        "!=",
	"gt":         ">",
	"gte":        ">=",
	"lt":         "<",
	"lte":        "<=",
	"like":       "LIKE",
	"ilike":      "ILIKE",
	"match":      "~",  // POSIX regex match
	"imatch":     "~*", // POSIX regex match, case-insensitive
	"is":         "IS",
	"isdistinct": "IS DISTINCT FROM",
	"in":         "IN",
	"cs":         "@>",
	"cd":         "<@",
	"ov":         "&&",
	"fts":        "@@",
	"plfts":      "@@",
	"phfts":      "@@",
	"wfts":       "@@",
	// Range operators (PostgREST). Values are Postgres range literals like
	// "(1,10)" or "[1,10)"; we pass them as text params and rely on
	// Postgres to infer the range type from the target column.
	"sl":  "<<",  // strictly left of
	"sr":  ">>",  // strictly right of
	"nxl": "&>",  // does not extend to the left of
	"nxr": "&<",  // does not extend to the right of
	"adj": "-|-", // adjacent to
	// like(all)/like(any) families. SQL form is handled in
	// buildFilterCondition; map values here are placeholders only.
	"like(all)":  "LIKE",
	"like(any)":  "LIKE",
	"ilike(all)": "ILIKE",
	"ilike(any)": "ILIKE",
}

// parseFilterValue splits "eq.active" into ("eq", "active", "", nil).
// Operators may carry an inline parameter in parentheses — currently
// used by the FTS family to choose a text-search configuration:
//
//	fts(english).query → op="fts", config="english", value="query"
//
// For JSONB access, the column may contain "->" or "->>" operators,
// e.g. "metadata->>theme=eq.dark" is parsed in parseFilters with column="metadata->>theme".
func parseFilterValue(val string) (op, operand, config string, err error) {
	idx := strings.Index(val, ".")
	if idx == -1 {
		return "", "", "", fmt.Errorf("expected operator.value format, got %q", val)
	}
	op = val[:idx]
	operand = val[idx+1:]

	// Inline config: strip "(…)" from the op token.
	if lp := strings.Index(op, "("); lp != -1 {
		if !strings.HasSuffix(op, ")") {
			return "", "", "", fmt.Errorf("malformed operator %q", op)
		}
		config = op[lp+1 : len(op)-1]
		op = op[:lp]
		if config == "" {
			return "", "", "", fmt.Errorf("empty config in operator %q", val[:idx])
		}
		if !identRe.MatchString(config) {
			return "", "", "", fmt.Errorf("invalid config %q", config)
		}
	}

	if _, ok := validOps[op]; !ok {
		return "", "", "", fmt.Errorf("unknown operator %q", op)
	}

	// The `is` operator is the one comparison whose right-hand side is not
	// emitted as a bind parameter (Postgres `IS` requires a keyword literal,
	// not an expression). Restrict it to the four SQL truth keywords so a
	// caller can never smuggle arbitrary SQL into the WHERE clause via
	// `col=is.<anything>`. PostgREST applies the same restriction.
	if op == "is" {
		switch strings.ToLower(strings.TrimSpace(operand)) {
		case "null", "true", "false", "unknown":
		default:
			return "", "", "", fmt.Errorf(`operator "is" only accepts null, true, false, or unknown`)
		}
	}

	return op, operand, config, nil
}

// parsePatternList parses the value portion of a like(all)/like(any) filter.
// PostgREST accepts curly-brace form ({a,b,c}) and we also accept parenthesized
// form ((a,b,c)) for symmetry with in.(...). An empty list yields [""] so a
// degenerate filter still produces a LIKE comparison rather than an empty
// ARRAY[] (which Postgres cannot type-infer).
func parsePatternList(raw string) []string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 {
		if (s[0] == '{' && s[len(s)-1] == '}') || (s[0] == '(' && s[len(s)-1] == ')') {
			s = s[1 : len(s)-1]
		}
	}
	if s == "" {
		return []string{""}
	}
	parts := strings.Split(s, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// jsonPathStep is one link of a JSONB accessor chain. An integer key
// (isInt = true) is rendered into SQL unquoted so Postgres treats it as
// an array index; a text key is rendered as a single-quoted literal.
type jsonPathStep struct {
	op    string // "->" or "->>"
	key   string
	isInt bool
}

// splitJSONBPath parses a filter/select column like "data->items->0->>name"
// into the base column ("data") and an ordered list of JSONB access steps.
// A column with no path operators returns (col, nil).
func splitJSONBPath(col string) (string, []jsonPathStep) {
	idx := strings.Index(col, "->")
	if idx == -1 {
		return col, nil
	}
	base := col[:idx]
	rest := col[idx:]
	var steps []jsonPathStep
	for len(rest) > 0 {
		if !strings.HasPrefix(rest, "->") {
			break
		}
		op := "->"
		keyStart := 2
		if len(rest) > 2 && rest[2] == '>' {
			op = "->>"
			keyStart = 3
		}
		tail := rest[keyStart:]
		next := strings.Index(tail, "->")
		var key string
		if next == -1 {
			key = tail
			rest = ""
		} else {
			key = tail[:next]
			rest = tail[next:]
		}
		steps = append(steps, jsonPathStep{op: op, key: key, isInt: isAllDigits(key)})
	}
	return base, steps
}

// renderJSONBSuffix serializes a JSONB access chain onto a prefix like
// "tbl.col" or "col", emitting integer keys unquoted and text keys as
// single-quoted literals (jsonKeyRe has already validated text keys).
func renderJSONBSuffix(steps []jsonPathStep) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(s.op)
		if s.isInt {
			b.WriteString(s.key)
		} else {
			b.WriteByte('\'')
			b.WriteString(s.key)
			b.WriteByte('\'')
		}
	}
	return b.String()
}

// lastJSONBOp reports the operator of the final step in a JSONB chain,
// which determines the result type: "->>" yields text, "->" yields jsonb.
func lastJSONBOp(steps []jsonPathStep) string {
	if len(steps) == 0 {
		return ""
	}
	return steps[len(steps)-1].op
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseSelectParam(sel string) []string {
	// Simple parsing — just split on commas at top level (respecting parentheses)
	var result []string
	depth := 0
	start := 0
	for i, ch := range sel {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				result = append(result, strings.TrimSpace(sel[start:i]))
				start = i + 1
			}
		}
	}
	if start < len(sel) {
		result = append(result, strings.TrimSpace(sel[start:]))
	}
	return result
}

// buildEmbedRowExpr returns the JSON expression representing a single row of an
// embed, including nested child subselects. parentTable is the table the embed's
// FK column references from (for correlation). srcAlias is the qualifier used to
// reference the embed's columns (e.g., the table name in a subselect, or an alias
// for a LEFT JOINed belongs-to).
func buildEmbedRowExpr(emb Embed, srcAlias string, allTables map[string]domain.Table, argIdx int) (string, []any, int) {
	var allArgs []any

	// If no explicit columns AND no children → row_to_json(t.*)
	if len(emb.Columns) == 0 && len(emb.Children) == 0 {
		return fmt.Sprintf("row_to_json(%s.*)", srcAlias), nil, argIdx
	}

	// Build json_build_object entries for scalar columns and child embeds.
	var entries []string

	// Scalar columns: if columns are empty but children exist, use all fields
	// from the table (i.e., "*" with children means all scalar cols + children).
	scalarCols := emb.Columns
	if len(scalarCols) == 0 && len(emb.Children) > 0 {
		// Emit all fields from the referenced table as scalar columns.
		if allTables != nil {
			if refTbl, ok := allTables[emb.RefTable]; ok {
				for _, f := range refTbl.Fields {
					scalarCols = append(scalarCols, f.Name)
				}
				sort.Strings(scalarCols)
			}
		}
	}

	for _, c := range scalarCols {
		entries = append(entries, fmt.Sprintf("'%s', %s.%s", c, srcAlias, c))
	}

	// Nested child embeds → scalar subselects.
	for _, child := range emb.Children {
		childExpr, childArgs, nextIdx := buildChildEmbedSubselect(child, srcAlias, allTables, argIdx)
		entries = append(entries, fmt.Sprintf("'%s', %s", child.outputKey(), childExpr))
		allArgs = append(allArgs, childArgs...)
		argIdx = nextIdx
	}

	return fmt.Sprintf("json_build_object(%s)", strings.Join(entries, ", ")), allArgs, argIdx
}

// buildChildEmbedSubselect builds a complete scalar subselect expression for a
// nested child embed, correlated to the parent via parentAlias.
func buildChildEmbedSubselect(child Embed, parentAlias string, allTables map[string]domain.Table, argIdx int) (string, []any, int) {
	var allArgs []any

	if child.IsReverse {
		// Has-many child: (SELECT coalesce(json_agg(rowExpr), '[]'::json) FROM T WHERE T.fk = parent.pk)
		rowExpr, rowArgs, nextIdx := buildEmbedRowExpr(child, child.RefTable, allTables, argIdx)
		allArgs = append(allArgs, rowArgs...)
		argIdx = nextIdx

		refPK := child.RefColumn
		if refPK == "" {
			refPK = "id"
		}
		sub := fmt.Sprintf("SELECT coalesce(json_agg(%s", rowExpr)
		if len(child.Order) > 0 {
			sub += " ORDER BY " + renderOrderBy(child.Order)
		}
		sub += fmt.Sprintf("), '[]'::json) FROM %s WHERE %s.%s = %s.%s",
			child.RefTable, child.RefTable, child.FKColumn, parentAlias, refPK)
		if child.Where != nil {
			clauseSQL, clauseArgs, next := child.Where.buildSQL(argIdx)
			if clauseSQL != "" {
				sub += " AND " + clauseSQL
				allArgs = append(allArgs, clauseArgs...)
				argIdx = next
			}
		}
		if child.Limit != nil {
			sub += fmt.Sprintf(" LIMIT %d", *child.Limit)
		}
		if child.Offset != nil {
			sub += fmt.Sprintf(" OFFSET %d", *child.Offset)
		}
		return fmt.Sprintf("(%s)", sub), allArgs, argIdx
	}

	// Belongs-to child: (SELECT row_to_json/json_build_object FROM T WHERE T.pk = parent.fk LIMIT 1)
	rowExpr, rowArgs, nextIdx := buildEmbedRowExpr(child, child.RefTable, allTables, argIdx)
	allArgs = append(allArgs, rowArgs...)
	argIdx = nextIdx

	sub := fmt.Sprintf("SELECT %s FROM %s WHERE %s.%s = %s.%s LIMIT 1",
		rowExpr, child.RefTable, child.RefTable, child.RefColumn, parentAlias, child.FKColumn)
	return fmt.Sprintf("(%s)", sub), allArgs, argIdx
}

// buildSelectQuery builds a SELECT SQL from QueryParams, including LEFT JOINs for embeds.
func buildSelectQuery(tableName string, qp *QueryParams, table domain.Table) (string, []any) {
	return buildSelectQueryFull(tableName, qp, table, nil)
}

// buildSelectQueryFull is the full version of buildSelectQuery that accepts allTables
// for resolving nested embed schemas.
func buildSelectQueryFull(tableName string, qp *QueryParams, table domain.Table, allTables map[string]domain.Table) (string, []any) {
	// SELECT clause — base table columns
	var selectParts []string
	var groupByExprs []string
	var hasAgg bool
	if len(qp.Select) > 0 {
		var items []SelectItem
		for _, s := range qp.Select {
			if strings.Contains(s, "(") && !isAggSelectEntry(s) {
				continue // embed handled below
			}
			item := parseSelectItem(s)
			items = append(items, item)
			if item.Agg != "" {
				hasAgg = true
			}
		}
		for _, item := range items {
			selectParts = append(selectParts, renderSelectItem(tableName, item))
			if hasAgg && item.Agg == "" {
				if expr := renderSelectItemGroupByExpr(tableName, item); expr != "" {
					groupByExprs = append(groupByExprs, expr)
				}
			}
		}
	}
	if len(selectParts) == 0 {
		if len(table.Searchable) > 0 {
			// Exclude the internal _tsv tsvector column from default SELECT.
			for _, f := range table.Fields {
				selectParts = append(selectParts, tableName+"."+f.Name)
			}
		} else {
			selectParts = append(selectParts, tableName+".*")
		}
	}

	var allArgs []any
	argIdx := 1
	var belongsToWhere []string

	// Add embed columns with aliases. belongs-to embeds reference a single
	// joined row by alias; has-many embeds are emitted as a correlated
	// scalar subselect that aggregates children into a JSON array.
	for _, emb := range qp.Embeds {
		alias := "_emb_" + emb.outputKey()
		hasChildren := len(emb.Children) > 0
		if emb.IsReverse {
			// Has-many embed → correlated scalar subselect with json_agg.
			rowExpr, rowArgs, nextIdx := buildEmbedRowExpr(emb, emb.RefTable, allTables, argIdx)
			allArgs = append(allArgs, rowArgs...)
			argIdx = nextIdx

			refPK := emb.RefColumn
			if refPK == "" {
				refPK = "id"
			}

			needsInnerSubquery := emb.Limit != nil || emb.Offset != nil

			if needsInnerSubquery {
				// When LIMIT/OFFSET is requested, wrap the source rows in a
				// subquery so pagination applies before aggregation.
				inner := fmt.Sprintf("SELECT * FROM %s WHERE %s.%s = %s.%s",
					emb.RefTable, emb.RefTable, emb.FKColumn, tableName, refPK)
				if emb.Where != nil {
					clauseSQL, clauseArgs, next := emb.Where.buildSQL(argIdx)
					if clauseSQL != "" {
						inner += " AND " + clauseSQL
						allArgs = append(allArgs, clauseArgs...)
						argIdx = next
					}
				}
				if len(emb.Order) > 0 {
					inner += " ORDER BY " + renderOrderBy(emb.Order)
				}
				if emb.Limit != nil {
					inner += fmt.Sprintf(" LIMIT %d", *emb.Limit)
				}
				if emb.Offset != nil {
					inner += fmt.Sprintf(" OFFSET %d", *emb.Offset)
				}
				sub := fmt.Sprintf(
					"SELECT coalesce(json_agg(%s), '[]'::json) FROM (%s) %s",
					rowExpr, inner, emb.RefTable)
				selectParts = append(selectParts, fmt.Sprintf("(%s) AS %s", sub, emb.outputKey()))
			} else {
				sub := fmt.Sprintf(
					"SELECT coalesce(json_agg(%s", rowExpr)
				if len(emb.Order) > 0 {
					sub += " ORDER BY " + renderOrderBy(emb.Order)
				}
				sub += fmt.Sprintf("), '[]'::json) FROM %s WHERE %s.%s = %s.%s",
					emb.RefTable, emb.RefTable, emb.FKColumn, tableName, refPK)
				if emb.Where != nil {
					clauseSQL, clauseArgs, next := emb.Where.buildSQL(argIdx)
					if clauseSQL != "" {
						sub += " AND " + clauseSQL
						allArgs = append(allArgs, clauseArgs...)
						argIdx = next
					}
				}
				selectParts = append(selectParts, fmt.Sprintf("(%s) AS %s", sub, emb.outputKey()))
			}
		} else if emb.Spread {
			// Spread belongs-to: inline columns into parent SELECT.
			spreadCols := emb.Columns
			if len(spreadCols) == 0 {
				refTbl := allTables[emb.RefTable]
				for _, f := range refTbl.Fields {
					spreadCols = append(spreadCols, f.Name)
				}
				sort.Strings(spreadCols)
			}
			for _, c := range spreadCols {
				selectParts = append(selectParts, fmt.Sprintf("%s.%s", alias, c))
			}
			// Nested children of a spread embed also become top-level select parts.
			for _, child := range emb.Children {
				childExpr, childArgs, nextIdx := buildChildEmbedSubselect(child, alias, allTables, argIdx)
				selectParts = append(selectParts, fmt.Sprintf("%s AS %s", childExpr, child.outputKey()))
				allArgs = append(allArgs, childArgs...)
				argIdx = nextIdx
			}
		} else if hasChildren {
			// Belongs-to with nested children → json_build_object.
			rowExpr, rowArgs, nextIdx := buildEmbedRowExpr(emb, alias, allTables, argIdx)
			allArgs = append(allArgs, rowArgs...)
			argIdx = nextIdx
			selectParts = append(selectParts, fmt.Sprintf("%s AS %s", rowExpr, emb.outputKey()))
		} else {
			// Simple belongs-to (no children, no spread).
			if len(emb.Columns) == 0 {
				selectParts = append(selectParts,
					fmt.Sprintf("row_to_json(%s.*) AS %s", alias, emb.outputKey()))
			} else {
				var embCols []string
				for _, c := range emb.Columns {
					embCols = append(embCols, fmt.Sprintf("'%s', %s.%s", c, alias, c))
				}
				selectParts = append(selectParts,
					fmt.Sprintf("json_build_object(%s) AS %s", strings.Join(embCols, ", "), emb.outputKey()))
			}
		}
	}

	sql := fmt.Sprintf("SELECT %s FROM %s", strings.Join(selectParts, ", "), tableName)

	// JOINs for belongs-to embeds only. !inner converts LEFT → INNER.
	for _, emb := range qp.Embeds {
		if emb.IsReverse {
			continue
		}
		alias := "_emb_" + emb.outputKey()
		joinKind := "LEFT JOIN"
		if emb.Inner {
			joinKind = "INNER JOIN"
		}
		sql += fmt.Sprintf(" %s %s AS %s ON %s.%s = %s.%s",
			joinKind, emb.RefTable, alias, tableName, emb.FKColumn, alias, emb.RefColumn)
		if emb.Where != nil {
			clauseSQL, clauseArgs, next := aliasWhereColumns(emb.Where, alias).buildSQL(argIdx)
			if clauseSQL != "" {
				belongsToWhere = append(belongsToWhere, clauseSQL)
				allArgs = append(allArgs, clauseArgs...)
				argIdx = next
			}
		}
	}

	// Outer WHERE: combine main filters with belongs-to embed filters.
	// When belongs-to embeds added JOINs above, parent filter columns must be
	// qualified with the base table name; otherwise references like `id` are
	// ambiguous against the joined table's columns.
	parentWhere := qp.Where
	if hasBelongsToJoin(qp.Embeds) {
		parentWhere = aliasWhereColumns(qp.Where, tableName)
	}
	whereSQL, whereArgs, nextArgIdx := parentWhere.buildSQL(argIdx)
	if whereSQL != "" {
		allArgs = append(allArgs, whereArgs...)
	}
	argIdx = nextArgIdx
	var whereParts []string
	if whereSQL != "" {
		whereParts = append(whereParts, whereSQL)
	}
	whereParts = append(whereParts, belongsToWhere...)

	// has-many !inner: filter parent rows to those with at least one
	// matching child via WHERE EXISTS (...). PostgREST treats the !inner
	// hint on a reverse embed as an existence predicate on the parent,
	// independent of the json_agg subselect that still materializes the
	// child array. The embed WHERE is re-rendered here with a fresh
	// argIdx so placeholder numbering continues after the outer WHERE.
	for _, emb := range qp.Embeds {
		if !emb.IsReverse || !emb.Inner {
			continue
		}
		refPK := emb.RefColumn
		if refPK == "" {
			refPK = "id"
		}
		existsSQL := fmt.Sprintf("EXISTS (SELECT 1 FROM %s WHERE %s.%s = %s.%s",
			emb.RefTable, emb.RefTable, emb.FKColumn, tableName, refPK)
		if emb.Where != nil {
			clauseSQL, clauseArgs, next := emb.Where.buildSQL(argIdx)
			if clauseSQL != "" {
				existsSQL += " AND " + clauseSQL
				allArgs = append(allArgs, clauseArgs...)
				argIdx = next
			}
		}
		existsSQL += ")"
		whereParts = append(whereParts, existsSQL)
	}

	if len(whereParts) > 0 {
		sql += " WHERE " + strings.Join(whereParts, " AND ")
	}

	// GROUP BY — added whenever aggregates are mixed with plain columns.
	if len(groupByExprs) > 0 {
		sql += " GROUP BY " + strings.Join(groupByExprs, ", ")
	}

	// HAVING — aggregate filters applied after GROUP BY.
	if qp.Having != nil {
		havingSQL, havingArgs, nextIdx := qp.Having.buildSQL(argIdx)
		if havingSQL != "" {
			sql += " HAVING " + havingSQL
			allArgs = append(allArgs, havingArgs...)
			argIdx = nextIdx
		}
	}

	// ORDER BY. Same ambiguity story as WHERE: when JOINs are present, a
	// non-alias clause like `order=id.asc` must be qualified to the base table
	// for any column that's also present on a joined embed table. Postgres
	// resolves output-list aliases first, so columns explicitly in the SELECT
	// are safe; we qualify the rest. IsAlias clauses are already double-quoted
	// against the output list and never need a table prefix.
	if len(qp.Order) > 0 {
		order := qp.Order
		if hasBelongsToJoin(qp.Embeds) {
			order = qualifyOrderColumns(qp.Order, tableName)
		}
		sql += " ORDER BY " + renderOrderBy(order)
	}

	// LIMIT & OFFSET
	sql += fmt.Sprintf(" LIMIT %d OFFSET %d", qp.Limit, qp.Offset)

	return sql, allArgs
}

// renderOrderBy emits a comma-separated ORDER BY list from OrderClauses.
// Alias-resolved clauses are double-quoted so Postgres resolves them
// from the select output list rather than the underlying table.
func renderOrderBy(clauses []OrderClause) string {
	parts := make([]string, 0, len(clauses))
	for _, o := range clauses {
		dir := "ASC"
		if o.Desc {
			dir = "DESC"
		}
		col := o.Column
		if o.IsAlias {
			col = `"` + strings.ReplaceAll(o.Column, `"`, `""`) + `"`
		}
		c := fmt.Sprintf("%s %s", col, dir)
		switch o.Nulls {
		case "first":
			c += " NULLS FIRST"
		case "last":
			c += " NULLS LAST"
		}
		parts = append(parts, c)
	}
	return strings.Join(parts, ", ")
}

// resolveEmbeds resolves embed names to FK relationships using the table config.
// It recurses to resolve nested embeds (e.g., author(*, posts(*))).
func resolveEmbeds(tableName string, table domain.Table, embedNames []string, allTables map[string]domain.Table) ([]Embed, error) {
	var embeds []Embed

	for _, raw := range embedNames {
		name, alias, cols, nested, spread := parseEmbedParam(raw)
		name, inner, fkHint := parseEmbedHint(name)
		if spread && alias != "" {
			return nil, fmt.Errorf("alias not allowed on spread embed %q", alias+":"+name)
		}

		// Check for belongs-to: this table has a FK column pointing to the embed name
		// Convention: FK column name matches embed name + "_id" or references the embed table
		found := false
		for _, field := range table.Fields {
			if field.ForeignKey == nil {
				continue
			}
			fieldName := field.Name
			ref := field.ForeignKey.References
			parts := strings.SplitN(ref, ".", 2)
			if len(parts) != 2 {
				continue
			}
			refTable, refCol := parts[0], parts[1]

			// When an FK hint was given, the caller is disambiguating between
			// multiple FKs to the same table — only match the column whose name
			// (or column-minus-_id) equals the hint.
			if fkHint != "" {
				if fieldName != fkHint && strings.TrimSuffix(fieldName, "_id") != fkHint {
					continue
				}
				if refTable != name {
					continue
				}
			} else if !(refTable == name || strings.TrimSuffix(fieldName, "_id") == name) {
				continue
			}
			emb := Embed{
				Name:      name,
				Alias:     alias,
				Columns:   cols,
				FKColumn:  fieldName,
				RefTable:  refTable,
				RefColumn: refCol,
				Inner:     inner,
				Spread:    spread,
			}
			if len(nested) > 0 {
				refTbl, ok := allTables[refTable]
				if !ok {
					return nil, fmt.Errorf("embed %q references unknown table %q", name, refTable)
				}
				children, err := resolveEmbeds(refTable, refTbl, nested, allTables)
				if err != nil {
					return nil, fmt.Errorf("nested embed in %q: %w", name, err)
				}
				emb.Children = children
			}
			embeds = append(embeds, emb)
			found = true
			break
		}

		if found {
			continue
		}

		// Check for has-many: another table has a FK pointing to this table
		for otherName, otherTable := range allTables {
			if otherName == tableName {
				continue
			}
			if otherName != name {
				continue
			}
			if spread {
				return nil, fmt.Errorf("spread (...) not allowed on has-many embed %q", name)
			}
			for _, field := range otherTable.Fields {
				if field.ForeignKey == nil {
					continue
				}
				fieldName := field.Name
				ref := field.ForeignKey.References
				parts := strings.SplitN(ref, ".", 2)
				if len(parts) != 2 {
					continue
				}
				if parts[0] == tableName {
					// Respect FK disambiguation hint: when set, only match the
					// reverse FK whose column name equals the hint. This lets
					// callers pick a specific FK when two tables reference the
					// same parent.
					if fkHint != "" && fieldName != fkHint && strings.TrimSuffix(fieldName, "_id") != fkHint {
						continue
					}
					emb := Embed{
						Name:      name,
						Alias:     alias,
						Columns:   cols,
						FKColumn:  fieldName,
						RefTable:  otherName,
						RefColumn: parts[1],
						IsReverse: true,
						Inner:     inner,
					}
					// Recurse for nested embeds
					if len(nested) > 0 {
						children, err := resolveEmbeds(otherName, otherTable, nested, allTables)
						if err != nil {
							return nil, fmt.Errorf("nested embed in %q: %w", name, err)
						}
						emb.Children = children
					}
					embeds = append(embeds, emb)
					found = true
					break
				}
			}
		}

		if !found {
			return nil, fmt.Errorf("could not find a relationship between %q and %q in the schema", tableName, name)
		}
	}

	return embeds, nil
}

// parseEmbedParam parses "author(id,name)" into name, alias, cols, and nested
// embed raw strings. Items containing "(" are returned in nested; others in
// cols. A "..." prefix on the name sets spread=true. A "alias:" prefix
// (PostgREST renaming) is stripped from name and returned in alias.
func parseEmbedParam(s string) (name, alias string, cols []string, nested []string, spread bool) {
	idx := strings.Index(s, "(")
	if idx == -1 {
		name = s
		if strings.HasPrefix(name, "...") {
			spread = true
			name = name[3:]
		}
		alias, name = splitEmbedAlias(name)
		return
	}
	name = s[:idx]
	if strings.HasPrefix(name, "...") {
		spread = true
		name = name[3:]
	}
	alias, name = splitEmbedAlias(name)
	inner := s[idx+1 : len(s)-1] // strip parens
	if inner == "*" || inner == "" {
		return
	}
	items, err := splitTopLevel(inner, ',')
	if err != nil {
		// Fallback: treat entire inner as a single column (shouldn't happen
		// with well-formed input).
		cols = []string{inner}
		return
	}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || item == "*" {
			continue
		}
		if strings.Contains(item, "(") {
			nested = append(nested, item)
		} else {
			cols = append(cols, item)
		}
	}
	return
}

func buildFilterCondition(f Filter, argIdx int) (string, []any, int) {
	// Handle JSONB access operators in column name (e.g. "metadata->>theme",
	// "data->items->0->>name"). splitJSONBPath returns the base column plus
	// an ordered chain of ->/->> steps, which we render back onto the base.
	colExpr := f.Column
	baseCol, steps := splitJSONBPath(f.Column)
	if len(steps) > 0 {
		colExpr = baseCol + renderJSONBSuffix(steps)
	}

	// like(all)/like(any)/ilike(all)/ilike(any) are parsed as op="like" config="any"
	// by parseFilterValue. Reconstruct the composite key for the switch below.
	switchOp := f.Operator
	if f.Config == "all" || f.Config == "any" {
		switchOp = f.Operator + "(" + f.Config + ")"
	}

	switch switchOp {
	case "is":
		// parseFilterValue has already constrained f.Value to one of the
		// four SQL truth keywords, so only the keyword literal is ever
		// interpolated here. The default arm is defensive: should an
		// unvalidated value ever reach this point, compare via a bind
		// parameter instead of interpolating raw input into SQL.
		isVal := strings.ToUpper(strings.TrimSpace(f.Value))
		switch isVal {
		case "NULL", "TRUE", "FALSE", "UNKNOWN":
			return fmt.Sprintf("%s IS %s", colExpr, isVal), nil, argIdx
		default:
			return fmt.Sprintf("%s IS NOT DISTINCT FROM $%d", colExpr, argIdx), []any{f.Value}, argIdx + 1
		}

	case "isdistinct":
		// PostgREST: `col=isdistinct.NULL` / `col=isdistinct.val`.
		val := strings.ToUpper(f.Value)
		if val == "NULL" || val == "TRUE" || val == "FALSE" {
			return fmt.Sprintf("%s IS DISTINCT FROM %s", colExpr, val), nil, argIdx
		}
		return fmt.Sprintf("%s IS DISTINCT FROM $%d", colExpr, argIdx),
			[]any{f.Value}, argIdx + 1

	case "in":
		// in.(val1,val2,val3)
		inner := strings.TrimPrefix(f.Value, "(")
		inner = strings.TrimSuffix(inner, ")")
		vals := strings.Split(inner, ",")
		placeholders := make([]string, len(vals))
		var args []any
		for i, v := range vals {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args = append(args, strings.TrimSpace(v))
			argIdx++
		}
		return fmt.Sprintf("%s IN (%s)", colExpr, strings.Join(placeholders, ", ")), args, argIdx

	case "fts", "plfts", "phfts", "wfts":
		fn := map[string]string{
			"fts":   "to_tsquery",
			"plfts": "plainto_tsquery",
			"phfts": "phraseto_tsquery",
			"wfts":  "websearch_to_tsquery",
		}[f.Operator]
		// `fts(english).query` surfaces as Config="english"; emit it as
		// the first arg to the text-search function, matching
		// PostgREST's fts(<config>).<query> wire format.
		if f.Config != "" {
			return fmt.Sprintf("%s @@ %s('%s', $%d)", colExpr, fn, f.Config, argIdx),
				[]any{f.Value}, argIdx + 1
		}
		return fmt.Sprintf("%s @@ %s($%d)", colExpr, fn, argIdx), []any{f.Value}, argIdx + 1

	case "cs":
		return fmt.Sprintf("%s @> $%d", colExpr, argIdx), []any{f.Value}, argIdx + 1
	case "cd":
		return fmt.Sprintf("%s <@ $%d", colExpr, argIdx), []any{f.Value}, argIdx + 1
	case "ov":
		return fmt.Sprintf("%s && $%d", colExpr, argIdx), []any{f.Value}, argIdx + 1

	case "sl", "sr", "nxl", "nxr", "adj":
		return fmt.Sprintf("%s %s $%d", colExpr, validOps[f.Operator], argIdx),
			[]any{f.Value}, argIdx + 1

	case "like(all)", "like(any)", "ilike(all)", "ilike(any)":
		sqlOp := "LIKE"
		if strings.HasPrefix(switchOp, "ilike") {
			sqlOp = "ILIKE"
		}
		quant := "ALL"
		if strings.HasSuffix(switchOp, "(any)") {
			quant = "ANY"
		}
		patterns := parsePatternList(f.Value)
		placeholders := make([]string, len(patterns))
		args := make([]any, len(patterns))
		for i, p := range patterns {
			placeholders[i] = fmt.Sprintf("$%d", argIdx)
			args[i] = p
			argIdx++
		}
		return fmt.Sprintf("%s %s %s(ARRAY[%s])",
			colExpr, sqlOp, quant, strings.Join(placeholders, ", ")), args, argIdx

	default:
		sqlOp := validOps[f.Operator]
		return fmt.Sprintf("%s %s $%d", colExpr, sqlOp, argIdx), []any{f.Value}, argIdx + 1
	}
}

// buildBulkInsertQuery emits a single INSERT statement covering every record
// in one multi-VALUES clause. Columns are the union of keys across records;
// records missing a column get DEFAULT so tables with server-side defaults
// (sequences, timestamps) still work. When len(records) == 1 the result is
// equivalent to buildInsertQuery with lower overhead.
func buildBulkInsertQuery(tableName string, records []map[string]any, returning bool) (string, []any) {
	cols := unionColumns(records)
	args, rowSQLs := renderRowTuples(records, cols, 1)

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s",
		tableName,
		strings.Join(cols, ", "),
		strings.Join(rowSQLs, ", "))
	if returning {
		sql += " RETURNING *"
	}
	return sql, args
}

// buildBulkUpsertQuery is the bulk counterpart to buildUpsertQuery. It
// emits one multi-VALUES INSERT with an ON CONFLICT clause. resolution is
// "merge" or "ignore".
func buildBulkUpsertQuery(tableName string, records []map[string]any, conflictCols []string, resolution string, returning bool) (string, []any) {
	cols := unionColumns(records)
	args, rowSQLs := renderRowTuples(records, cols, 1)

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s ON CONFLICT (%s) ",
		tableName,
		strings.Join(cols, ", "),
		strings.Join(rowSQLs, ", "),
		strings.Join(conflictCols, ", "))

	if resolution == "ignore" {
		sql += "DO NOTHING"
	} else {
		conflictSet := make(map[string]bool, len(conflictCols))
		for _, c := range conflictCols {
			conflictSet[c] = true
		}
		var setParts []string
		for _, col := range cols {
			if conflictSet[col] {
				continue
			}
			setParts = append(setParts, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
		}
		if len(setParts) == 0 {
			sql += "DO NOTHING"
		} else {
			sql += "DO UPDATE SET " + strings.Join(setParts, ", ")
		}
	}
	if returning {
		sql += " RETURNING *"
	}
	return sql, args
}

// recordsAllEmpty reports whether every record has no keys. This guards
// against emitting "INSERT INTO t () VALUES ()" after columns= stripped
// every field.
func recordsAllEmpty(records []map[string]any) bool {
	for _, r := range records {
		if len(r) > 0 {
			return false
		}
	}
	return true
}

// unionColumns returns the sorted union of keys across all records.
func unionColumns(records []map[string]any) []string {
	set := map[string]bool{}
	for _, r := range records {
		for k := range r {
			set[k] = true
		}
	}
	cols := make([]string, 0, len(set))
	for c := range set {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	return cols
}

// renderRowTuples builds the VALUES tuples for a list of records against a
// fixed column order. Missing values become the literal token DEFAULT so
// the database supplies any configured default. Placeholder numbering starts
// at startArg.
func renderRowTuples(records []map[string]any, cols []string, startArg int) ([]any, []string) {
	var args []any
	argIdx := startArg
	rowSQLs := make([]string, 0, len(records))
	for _, rec := range records {
		parts := make([]string, len(cols))
		for i, col := range cols {
			if v, ok := rec[col]; ok {
				parts[i] = fmt.Sprintf("$%d", argIdx)
				args = append(args, v)
				argIdx++
			} else {
				parts[i] = "DEFAULT"
			}
		}
		rowSQLs = append(rowSQLs, "("+strings.Join(parts, ", ")+")")
	}
	return args, rowSQLs
}

func buildInsertQuery(tableName string, record map[string]any, returning bool) (string, []any) {
	cols := sortedMapKeys(record)
	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))

	for i, col := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = record[col]
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		tableName,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "))

	if returning {
		sql += " RETURNING *"
	}

	return sql, args
}

// parseColumnsParam parses a "columns=a,b" query param into an allow-set
// of column names. PostgREST uses this hint to restrict which keys from
// the request body are inserted; unlisted keys are silently dropped so
// the server-side default (or NULL) is used instead.
func parseColumnsParam(val string, table domain.Table) (map[string]bool, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, nil
	}
	cols := map[string]bool{}
	for _, c := range strings.Split(val, ",") {
		c = strings.TrimSpace(c)
		// PostgREST clients quote column identifiers ("username"). Strip the
		// enclosing quotes so we match against the bare column name.
		if len(c) >= 2 && c[0] == '"' && c[len(c)-1] == '"' {
			c = c[1 : len(c)-1]
		}
		if c == "" {
			return nil, fmt.Errorf("empty column in columns hint")
		}
		if _, ok := table.GetField(c); !ok {
			return nil, fmt.Errorf("unknown column %q in columns hint", c)
		}
		cols[c] = true
	}
	return cols, nil
}

// filterRecordsByColumns returns a copy of records where each record only
// retains keys present in allowed. A nil allow-set is a passthrough.
func filterRecordsByColumns(records []map[string]any, allowed map[string]bool) []map[string]any {
	if allowed == nil {
		return records
	}
	out := make([]map[string]any, len(records))
	for i, rec := range records {
		filtered := make(map[string]any, len(rec))
		for k, v := range rec {
			if allowed[k] {
				filtered[k] = v
			}
		}
		out[i] = filtered
	}
	return out
}

// parseOnConflictParam parses a "on_conflict=a,b" query param into the
// list of conflict-target columns. Each column is validated against the
// table schema. Returns nil (with no error) when val is empty.
func parseOnConflictParam(val string, table domain.Table) ([]string, error) {
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, nil
	}
	var cols []string
	for _, c := range strings.Split(val, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			return nil, fmt.Errorf("empty column in on_conflict")
		}
		if err := validateColumn(table, c); err != nil {
			return nil, fmt.Errorf("invalid on_conflict column: %w", err)
		}
		cols = append(cols, c)
	}
	return cols, nil
}

// primaryKeyColumns returns the PK field names for a table, sorted. An empty
// result means the table has no declared primary key — callers must treat
// this as a configuration error when building upsert SQL.
func primaryKeyColumns(table domain.Table) []string {
	var pks []string
	for _, f := range table.Fields {
		if f.PrimaryKey {
			pks = append(pks, f.Name)
		}
	}
	sort.Strings(pks)
	return pks
}

// buildUpsertQuery emits INSERT ... ON CONFLICT (pk) DO {UPDATE|NOTHING}.
// resolution is "merge" (update existing) or "ignore" (skip existing).
// conflictCols must be non-empty and must all exist as columns in record or
// on the base table; validation is the caller's responsibility.
func buildUpsertQuery(tableName string, record map[string]any, conflictCols []string, resolution string, returning bool) (string, []any) {
	cols := sortedMapKeys(record)
	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))

	for i, col := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = record[col]
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) ",
		tableName,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
		strings.Join(conflictCols, ", "))

	if resolution == "ignore" {
		sql += "DO NOTHING"
	} else {
		// Build SET list excluding conflict columns — we don't overwrite the
		// target key with itself.
		conflictSet := make(map[string]bool, len(conflictCols))
		for _, c := range conflictCols {
			conflictSet[c] = true
		}
		var setParts []string
		for _, col := range cols {
			if conflictSet[col] {
				continue
			}
			setParts = append(setParts, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
		}
		if len(setParts) == 0 {
			// Nothing to update (body was PK-only) — fall back to DO NOTHING.
			sql += "DO NOTHING"
		} else {
			sql += "DO UPDATE SET " + strings.Join(setParts, ", ")
		}
	}

	if returning {
		sql += " RETURNING *"
	}
	return sql, args
}

// parseRangeHeader parses a simple "start-end" Range value (as PostgREST
// expects with Range-Unit: items). Both bounds are inclusive and 0-based.
// A missing end is treated as invalid — we require a closed interval so the
// caller can compute limit without a second round-trip.
func parseRangeHeader(h string) (start, end int, ok bool) {
	h = strings.TrimSpace(h)
	// Accept optional "items=" prefix.
	if strings.HasPrefix(h, "items=") {
		h = strings.TrimPrefix(h, "items=")
	}
	parts := strings.SplitN(h, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, 0, false
	}
	s, err := strconv.Atoi(parts[0])
	if err != nil || s < 0 {
		return 0, 0, false
	}
	e, err := strconv.Atoi(parts[1])
	if err != nil || e < s {
		return 0, 0, false
	}
	return s, e, true
}

// parseResolutionPrefer extracts "merge" or "ignore" from a Prefer header.
// Returns "" when no resolution is specified.
func parseResolutionPrefer(prefer string) string {
	for _, part := range strings.Split(prefer, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "resolution=") {
			v := strings.TrimPrefix(part, "resolution=")
			switch v {
			case "merge-duplicates":
				return "merge"
			case "ignore-duplicates":
				return "ignore"
			}
		}
	}
	return ""
}

func buildUpdateQuery(tableName string, updates map[string]any, where *WhereNode, returning bool) (string, []any) {
	cols := sortedMapKeys(updates)
	var args []any
	argIdx := 1

	setParts := make([]string, len(cols))
	for i, col := range cols {
		setParts[i] = fmt.Sprintf("%s = $%d", col, argIdx)
		args = append(args, updates[col])
		argIdx++
	}

	sql := fmt.Sprintf("UPDATE %s SET %s", tableName, strings.Join(setParts, ", "))

	whereSQL, whereArgs, _ := where.buildSQL(argIdx)
	if whereSQL != "" {
		sql += " WHERE " + whereSQL
		args = append(args, whereArgs...)
	}

	if returning {
		sql += " RETURNING *"
	}

	return sql, args
}

func buildDeleteQuery(tableName string, where *WhereNode, returning bool) (string, []any) {
	sql := fmt.Sprintf("DELETE FROM %s", tableName)
	whereSQL, args, _ := where.buildSQL(1)
	if whereSQL != "" {
		sql += " WHERE " + whereSQL
	}
	if returning {
		sql += " RETURNING *"
	}
	return sql, args
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (c *QueryParams) String() string {
	return fmt.Sprintf("select=%v where=%v order=%v limit=%d offset=%d",
		c.Select, c.Where != nil, c.Order, c.Limit, c.Offset)
}

// handleNotFound returns a handler for /api/<table> endpoints not in the config
func handleNotFound() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, `{"code":"PGRST106","message":"Resource not found","details":"","hint":""}`)
	}
}

// withDBTimeout wraps ctx with the configured db_query timeout. If
// Prefer: statement-timeout=N is present (milliseconds), that overrides
// the config value but is capped at the configured max.
func (h *CRUDHandler) withDBTimeout(ctx context.Context, prefer string) (context.Context, context.CancelFunc) {
	cfgTimeout, _ := time.ParseDuration(h.cfg.Server.Timeouts.DBQuery)

	// Check Prefer header for statement-timeout
	if idx := strings.Index(prefer, "statement-timeout="); idx >= 0 {
		val := prefer[idx+len("statement-timeout="):]
		if end := strings.IndexAny(val, ", "); end > 0 {
			val = val[:end]
		}
		if ms, err := strconv.Atoi(val); err == nil && ms > 0 {
			d := time.Duration(ms) * time.Millisecond
			if cfgTimeout > 0 && d > cfgTimeout {
				d = cfgTimeout
			}
			return context.WithTimeout(ctx, d)
		}
	}

	if cfgTimeout > 0 {
		return context.WithTimeout(ctx, cfgTimeout)
	}
	return ctx, nil
}

func findGeometryColumn(table domain.Table) string {
	for _, f := range table.Fields {
		t := strings.ToLower(f.Type)
		if t == "geometry" || t == "geography" || strings.HasPrefix(t, "geometry(") || strings.HasPrefix(t, "geography(") {
			return f.Name
		}
	}
	return ""
}

// setupMutationTx creates an RLS context and begins a transaction.
// On failure it writes a problemJSON to c and returns non-nil error. Callers must return immediately.
func setupMutationTx(c *gin.Context, db domain.Database, session domain.Session) (context.Context, domain.Tx, func(context.Context), error) {
	noop := func(context.Context) {}
	ctx, err := db.WithRLS(c.Request.Context(), session)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to set RLS context")
		return nil, nil, noop, err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to start transaction")
		return nil, nil, noop, err
	}
	return ctx, tx, func(ctx context.Context) { tx.Rollback(ctx) }, nil
}

// writeMutationResponse writes the HTTP response for a mutation.
// status is 201 for create, 200 for upsert/update.
func writeMutationResponse(c *gin.Context, status int, returnMode string, results []map[string]any) {
	switch returnMode {
	case "representation":
		if c.GetHeader("Accept") == "application/vnd.pgrst.object+json" && len(results) == 1 {
			c.JSON(status, results[0])
		} else {
			if results == nil {
				results = []map[string]any{}
			}
			c.JSON(status, results)
		}
	case "headers-only":
		c.Header("Preference-Applied", "return=headers-only")
		c.Status(status)
	default:
		c.Status(status)
	}
}

// parseRequestBody parses POST/PUT body as records (CSV, JSON array, or single JSON object).
// On failure it writes a problemJSON response and returns nil, err. Callers must return on non-nil err.
func parseRequestBody(c *gin.Context, table domain.Table) ([]map[string]any, error) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		problemJSON(c, 400, "bad_request", "Cannot read request body")
		return nil, err
	}
	var records []map[string]any
	if contentTypeIsCSV(c.GetHeader("Content-Type")) {
		records, err = csvReadRecords(body)
		if err != nil {
			problemJSON(c, 400, "bad_request", err.Error())
			return nil, err
		}
		records = csvCoerceRecords(records, table)
	} else {
		trimmed := strings.TrimSpace(string(body))
		if strings.HasPrefix(trimmed, "[") {
			if err := json.Unmarshal(body, &records); err != nil {
				problemJSON(c, 400, "bad_request", "Invalid JSON array")
				return nil, err
			}
		} else {
			var single map[string]any
			if err := json.Unmarshal(body, &single); err != nil {
				problemJSON(c, 400, "bad_request", "Invalid JSON")
				return nil, err
			}
			records = []map[string]any{single}
		}
	}
	if len(records) == 0 {
		problemJSON(c, 400, "bad_request", "Empty request body")
		return nil, fmt.Errorf("empty body")
	}
	return records, nil
}
