package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/instancez/instancez/internal/adapter/http/postgrest"
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
	rest.Use(apiKeyGuard(h.jwtKeys))

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
		query, args := postgrest.BuildSelectQueryFull(tableName, qp, table, allTbls)

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
			defer func() { _ = tx.Rollback(ctx) }()
			rows, err := tx.Query(ctx, explainQuery, args...)
			if err != nil {
				handleDBError(c, err)
				return
			}
			if err := tx.Commit(ctx); err != nil {
				problemJSON(c, 500, "internal", "Failed to commit explain transaction")
				return
			}
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
		defer func() { _ = tx.Rollback(ctx) }()

		applySchemaSearchPath(c, ctx, tx)

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

		allowedCols, err := postgrest.ParseColumnsParam(c.Query("columns"), table)
		if err != nil {
			problemJSON(c, 400, "bad_request", err.Error())
			return
		}
		records = postgrest.FilterRecordsByColumns(records, allowedCols)
		if postgrest.RecordsAllEmpty(records) {
			problemJSON(c, 400, "bad_request", "No insertable columns after applying columns= filter")
			return
		}

		fieldMap := table.FieldMap()
		for _, rec := range records {
			if unknowns := postgrest.FindUnknownFields(rec, fieldMap); len(unknowns) > 0 {
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
			customCols, err := postgrest.ParseOnConflictParam(c.Query("on_conflict"), table)
			if err != nil {
				problemJSON(c, 400, "bad_request", err.Error())
				return
			}
			if len(customCols) > 0 {
				pkCols = customCols
			} else {
				pkCols = postgrest.PrimaryKeyColumns(table)
			}
			if len(pkCols) == 0 {
				problemJSON(c, 400, "bad_request", "Cannot upsert: table has no primary key and no on_conflict")
				return
			}
		}

		var query string
		var args []any
		if resolution != "" {
			query, args = postgrest.BuildBulkUpsertQuery(tableName, records, pkCols, resolution, returnMode == "representation")
		} else {
			query, args = postgrest.BuildBulkInsertQuery(tableName, records, returnMode == "representation")
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

		pkCols := postgrest.PrimaryKeyColumns(table)
		if len(pkCols) == 0 {
			problemJSON(c, 400, "bad_request", "Cannot upsert: table has no primary key")
			return
		}

		upsertFieldMap := table.FieldMap()
		for _, rec := range records {
			if unknowns := postgrest.FindUnknownFields(rec, upsertFieldMap); len(unknowns) > 0 {
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

		query, args := postgrest.BuildBulkUpsertQuery(tableName, records, pkCols, "merge", returnMode == "representation")
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

		if unknowns := postgrest.FindUnknownFields(updates, table.FieldMap()); len(unknowns) > 0 {
			problemJSON(c, 400, "bad_request",
				fmt.Sprintf("Unknown fields: %s", strings.Join(unknowns, ", ")))
			return
		}

		where, err := postgrest.ParseWhere(c.Request.URL.Query(), tableName, table)
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

		query, args := postgrest.BuildUpdateQuery(tableName, updates, where, returnMode == "representation")

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

		where, err := postgrest.ParseWhere(c.Request.URL.Query(), tableName, table)
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
		defer func() { _ = tx.Rollback(ctx) }()
		applySchemaSearchPath(c, ctx, tx)

		returnMode := parseReturnPrefer(prefer)
		maxAffected, hasMax := parseMaxAffectedPrefer(prefer)
		query, args := postgrest.BuildDeleteQuery(tableName, where, returnMode == "representation")

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
	whereSQL, args, _ := qp.Where.BuildSQL(1)
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
	whereSQL, args, _ := qp.Where.BuildSQL(1)
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
		"Query result exceeds max-affected preference constraint",
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

// Type aliases so the rest of this package can reference them without the
// postgrest. prefix while we complete the migration.
type Embed = postgrest.Embed
type QueryParams = postgrest.QueryParams
type Filter = postgrest.Filter
type OrderClause = postgrest.OrderClause

func parseQueryParams(c *gin.Context, tableName string, table domain.Table, allTables map[string]domain.Table) (*QueryParams, error) {
	qp := &QueryParams{
		Limit:  20, // default
		Offset: 0,
	}

	// Parse select
	if sel := c.Query("select"); sel != "" {
		qp.Select = postgrest.ParseSelectParam(sel)
		for _, s := range qp.Select {
			if strings.Contains(s, "(") && !postgrest.IsAggSelectEntry(s) {
				continue // embed — validated via resolveEmbeds
			}
			item := postgrest.ParseSelectItem(s)
			if err := postgrest.ValidateSelectItem(table, item); err != nil {
				return nil, fmt.Errorf("invalid select: %w", err)
			}
		}
	}

	// Resolve embed specs now so later parsing can route embed-scoped query
	// parameters (<embed>.col=op.val, <embed>.order, <embed>.limit).
	if allTables != nil {
		var embedParams []string
		for _, s := range qp.Select {
			if strings.Contains(s, "(") && !postgrest.IsAggSelectEntry(s) {
				embedParams = append(embedParams, s)
			}
		}
		if len(embedParams) > 0 {
			resolved, err := postgrest.ResolveEmbeds(tableName, table, embedParams, allTables)
			if err != nil {
				return nil, fmt.Errorf("invalid embed: %w", err)
			}
			qp.Embeds = resolved
		}
	}
	embedByName := map[string]*postgrest.Embed{}
	for i := range qp.Embeds {
		embedByName[qp.Embeds[i].OutputKey()] = &qp.Embeds[i]
	}

	// Parse order. Format per PostgREST: col[.asc|.desc][.nullsfirst|.nullslast].
	// Select-list aliases (including aggregate default keys) are resolved
	// first so `order=count.desc` works against `select=...,id.count()`.
	if order := c.Query("order"); order != "" {
		clauses, err := postgrest.ParseOrderValueWithSelect(order, table, qp.Select)
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
		if err := postgrest.ParseEmbedScopedParams(c.Request.URL.Query(), embedByName, allTables); err != nil {
			return nil, err
		}
	}
	skipPrefixes := make(map[string]bool, len(embedByName))
	for name := range embedByName {
		skipPrefixes[name] = true
	}

	// Parse filters (including or/and/not logic trees)
	where, err := postgrest.ParseWhereSkip(c.Request.URL.Query(), tableName, table, skipPrefixes)
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
		havingNode, err := postgrest.ParseHavingParam(havingRaw, tableName, table, qp.Select)
		if err != nil {
			return nil, fmt.Errorf("invalid having: %w", err)
		}
		qp.Having = havingNode
	}

	return qp, nil
}


// identRe matches a safe SQL identifier (alias or cast type). Casts are
// interpolated directly into SQL, so we refuse anything outside alnum/_.
// Kept here (duplicated from postgrest package) because rpc_handler.go
// references it directly within the same package.
var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseRangeHeader parses a simple "start-end" Range value (as PostgREST
// expects with Range-Unit: items). Both bounds are inclusive and 0-based.
func parseRangeHeader(h string) (start, end int, ok bool) {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "items=")
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
	applySchemaSearchPath(c, ctx, tx)
	return ctx, tx, func(ctx context.Context) { _ = tx.Rollback(ctx) }, nil
}

// applySchemaSearchPath switches the transaction's search_path when the
// request carried a Content-Profile/Accept-Profile header naming a
// non-public schema (see profileHeaderGuard). Both the read path (handleList)
// and every write path (via setupMutationTx) must call this — a request
// against a non-public-schema table only resolves correctly if the schema
// selected by the header actually applies to the transaction doing the work.
func applySchemaSearchPath(c *gin.Context, ctx context.Context, tx domain.Tx) {
	schema, _ := c.Get("_schema")
	if schema == nil {
		return
	}
	if s, ok := schema.(string); ok && s != "" && s != "public" {
		_, _ = tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s, public", s))
	}
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
		records, err = postgrest.CsvReadRecords(body)
		if err != nil {
			problemJSON(c, 400, "bad_request", err.Error())
			return nil, err
		}
		records = postgrest.CsvCoerceRecords(records, table)
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
