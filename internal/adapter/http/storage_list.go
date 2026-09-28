package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// listQuery mirrors storage.search (v1) and storage.search_v2 / list_objects_with_delimiter (v2).
type listQuery struct {
	bucket   string
	match    string // objects whose name starts with this
	fold     bool   // group keys past the next "/" into one folder row
	foldFrom int    // rune offset after which "/" starts a folder
	caseFold bool   // v1 matches, groups and orders case-insensitively
	byTime   bool
	v1       bool
	desc     bool
	limit    int
	offset   int
	after    *listCursor
}

type listCursor struct {
	Name string `json:"n"`
	At   string `json:"t,omitempty"`
}

// ponytail: groups every row under the prefix before LIMIT; add a skip-scan if prefixes get huge.
func (q listQuery) sql() (string, []any) {
	nameExpr, matchExpr := "name", "starts_with(name, $2)"
	if q.caseFold {
		nameExpr, matchExpr = "lower(name)", "starts_with(lower(name), lower($2))"
	}
	p := "0"
	if q.fold {
		p = "strpos(substr(name, $3 + 1), '/')"
	}
	args := []any{q.bucket, q.match, q.foldFrom}
	sql := fmt.Sprintf(`WITH m AS (
  SELECT id, name, uploaded_at, metadata, %s AS p FROM storage.objects WHERE bucket_id = $1 AND %s
), e AS (
  SELECT min(left(name, $3 + p)) AS name, NULL::text AS id, min(uploaded_at) AS uploaded_at, NULL::jsonb AS metadata, true AS folder
  FROM m WHERE p > 0 GROUP BY %s
  UNION ALL
  SELECT name, id::text, uploaded_at, metadata, false FROM m WHERE p = 0
)
SELECT name, id, uploaded_at, metadata, folder FROM e`, p, matchExpr, strings.Replace(nameExpr, "name", "left(name, $3 + p)", 1))

	dir, op := "ASC", ">"
	if q.desc {
		dir, op = "DESC", "<"
	}
	sortName := nameExpr + ` COLLATE "C"`
	ts := `COALESCE(date_trunc('milliseconds', uploaded_at), 'epoch'::timestamptz)`
	switch {
	case q.after != nil && q.byTime:
		at, _ := time.Parse(time.RFC3339Nano, q.after.At)
		args = append(args, at, q.after.Name)
		sql += fmt.Sprintf(` WHERE ROW(%s, name COLLATE "C") %s ROW(date_trunc('milliseconds', $4::timestamptz), $5::text)`, ts, op)
	case q.after != nil:
		args = append(args, q.after.Name)
		sql += fmt.Sprintf(` WHERE name COLLATE "C" %s $4`, op)
	}
	switch {
	case q.byTime && q.v1:
		// v1 lists folders first, then files by time.
		sql += fmt.Sprintf(` ORDER BY folder DESC, CASE WHEN folder THEN %s END %s, uploaded_at %s, %s %s`, sortName, dir, dir, sortName, dir)
	case q.byTime:
		sql += fmt.Sprintf(` ORDER BY %s %s, name COLLATE "C" %s`, ts, dir, dir)
	default:
		sql += fmt.Sprintf(` ORDER BY %s %s`, sortName, dir)
	}
	args = append(args, q.limit, q.offset)
	sql += fmt.Sprintf(` LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	return sql, args
}

func (h *StorageV1Handler) runList(ctx context.Context, q listQuery) ([]map[string]any, error) {
	sql, args := q.sql()
	return h.db.Query(ctx, sql, args...)
}

func listOrderDesc(order string) bool { return strings.EqualFold(order, "desc") }

func (h *StorageV1Handler) listObjects(c *gin.Context) {
	bucketName := c.Param("bucket")
	if _, ok := h.getBucketConfig(bucketName); !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}
	var req struct {
		Prefix string `json:"prefix"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
		Search string `json:"search"`
		SortBy struct {
			Column string `json:"column"`
			Order  string `json:"order"`
		} `json:"sortBy"`
	}
	_ = c.ShouldBindJSON(&req)

	prefix := strings.TrimPrefix(req.Prefix, "/")
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}
	match := prefix + req.Search
	q := listQuery{
		bucket: bucketName, match: match, fold: true, foldFrom: utf8.RuneCountInString(match), caseFold: true, v1: true,
		byTime: req.SortBy.Column == "updated_at" || req.SortBy.Column == "created_at" || req.SortBy.Column == "last_accessed_at",
		desc:   listOrderDesc(req.SortBy.Order), limit: min(req.Limit, 1500), offset: max(req.Offset, 0),
	}
	rows, err := h.runList(h.rlsCtx(c), q)
	if err != nil {
		h.logger.Error("list objects", "error", err)
		storageErr(c, 500, "internal", "Failed to list")
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		name := []rune(asString(row["name"]))
		rel := string(name[min(utf8.RuneCountInString(prefix), len(name)):])
		if row["folder"] == true {
			items = append(items, gin.H{"name": strings.TrimSuffix(rel, "/"), "id": nil, "updated_at": nil, "created_at": nil, "last_accessed_at": nil, "metadata": nil})
			continue
		}
		items = append(items, objectListItem(row, rel))
	}
	c.JSON(200, items)
}

func objectListItem(row map[string]any, name string) gin.H {
	at := isoTime(row["uploaded_at"])
	return gin.H{"name": name, "id": asString(row["id"]), "updated_at": at, "created_at": at, "last_accessed_at": at, "metadata": row["metadata"]}
}

func (h *StorageV1Handler) listObjectsV2(c *gin.Context) {
	bucketName := c.Param("bucket")
	if _, ok := h.getBucketConfig(bucketName); !ok {
		storageErr(c, 404, "not_found", "Bucket not found")
		return
	}
	var req struct {
		Prefix        string `json:"prefix"`
		Limit         int    `json:"limit"`
		Cursor        string `json:"cursor"`
		WithDelimiter bool   `json:"with_delimiter"`
		SortBy        struct {
			Column string `json:"column"`
			Order  string `json:"order"`
		} `json:"sortBy"`
	}
	_ = c.ShouldBindJSON(&req)

	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	q := listQuery{
		bucket: bucketName, match: req.Prefix, fold: req.WithDelimiter, foldFrom: utf8.RuneCountInString(req.Prefix),
		byTime: req.WithDelimiter && (req.SortBy.Column == "updated_at" || req.SortBy.Column == "created_at"),
		desc:   listOrderDesc(req.SortBy.Order), limit: limit + 1,
	}
	if req.Cursor != "" {
		cur, ok := decodeListCursor(req.Cursor)
		if !ok {
			storageErr(c, 400, "invalid_parameter", "Invalid cursor")
			return
		}
		q.after = &cur
	}
	rows, err := h.runList(h.rlsCtx(c), q)
	if err != nil {
		h.logger.Error("list objects v2", "error", err)
		storageErr(c, 500, "internal", "Failed to list")
		return
	}

	hasNext := len(rows) > limit
	if hasNext {
		rows = rows[:limit]
	}
	levels := 1
	if req.Prefix != "" {
		levels = len(strings.Split(req.Prefix, "/"))
	}
	folders, objects := []gin.H{}, []gin.H{}
	for _, row := range rows {
		name := asString(row["name"])
		if row["folder"] == true {
			var at any
			if q.byTime {
				at = isoTime(row["uploaded_at"])
			}
			folders = append(folders, gin.H{"id": nil, "name": name, "bucket_id": bucketName, "updated_at": at, "created_at": at, "last_accessed_at": nil})
			continue
		}
		item := objectListItem(row, name)
		if req.WithDelimiter {
			item["key"] = splitPart(name, levels)
		}
		objects = append(objects, item)
	}
	result := gin.H{"hasNext": hasNext, "folders": folders, "objects": objects}
	if hasNext {
		last := rows[len(rows)-1]
		cur := listCursor{Name: asString(last["name"])}
		if t, ok := last["uploaded_at"].(time.Time); ok && q.byTime {
			cur.At = t.UTC().Format(time.RFC3339Nano)
		}
		result["nextCursor"] = encodeListCursor(cur)
		result["nextCursorKey"] = cur.Name
	}
	c.JSON(200, result)
}

// splitPart is Postgres split_part(s, '/', n), 1-based.
func splitPart(s string, n int) string {
	parts := strings.Split(s, "/")
	if n < 1 || n > len(parts) {
		return ""
	}
	return parts[n-1]
}

func encodeListCursor(c listCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeListCursor(s string) (listCursor, bool) {
	var c listCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || json.Unmarshal(b, &c) != nil || c.Name == "" {
		return listCursor{}, false
	}
	if c.At != "" {
		if _, err := time.Parse(time.RFC3339Nano, c.At); err != nil {
			return listCursor{}, false
		}
	}
	return c, true
}
