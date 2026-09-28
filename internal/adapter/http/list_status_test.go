package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listStatus(t *testing.T, method, rawQuery string, headers map[string]string, n int, total int64) *httptest.ResponseRecorder {
	w, _ := listStatusSQL(t, method, rawQuery, headers, n, total)
	return w
}

func listStatusSQL(t *testing.T, method, rawQuery string, headers map[string]string, n int, total int64) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var sql string
	gin.SetMode(gin.TestMode)
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"id": int64(i)}
	}
	db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) {
		return &stubTx{
			queryFn: func(_ context.Context, q string, _ ...any) ([]map[string]any, error) {
				sql = q
				return rows, nil
			},
			queryRowFn: func(context.Context, string, ...any) (map[string]any, error) {
				return map[string]any{"count": total}, nil
			},
		}, nil
	}}
	h := &CRUDHandler{cfg: &domain.Config{}, db: db, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/rest/v1/todos?"+rawQuery, nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	c.Set(contextKeySession, domain.Session{Role: "service_role", IsAuthenticated: true})
	h.handleList("todos", testTable())(c)
	return w, sql
}

func TestHandleList_RangeStatus(t *testing.T) {
	exact := map[string]string{"Prefer": "count=exact"}
	for _, c := range []struct {
		name, method, query string
		headers             map[string]string
		n                   int
		total               int64
		status              int
		contentRange        string
	}{
		{"no count", "GET", "", nil, 2, 0, 200, "0-1/*"},
		{"partial page", "GET", "limit=2", exact, 2, 5, 206, "0-1/5"},
		{"full page", "GET", "", exact, 5, 5, 200, "0-4/5"},
		{"offset == total", "GET", "offset=5", exact, 0, 5, 206, "*/5"},
		{"past the end", "GET", "offset=10", exact, 0, 5, 416, "*/5"},
		{"empty table offset 1", "GET", "offset=1", exact, 0, 0, 416, "*/0"},
		{"empty table", "GET", "", exact, 0, 0, 200, "*/0"},
		{"limit=0", "GET", "limit=0", exact, 0, 5, 206, "*/5"},
		{"Range without count", "GET", "", map[string]string{"Range": "0-1"}, 2, 0, 200, "0-1/*"},
		{"Range with count", "GET", "", map[string]string{"Range": "0-1", "Prefer": "count=exact"}, 2, 3, 206, "0-1/3"},
		{"HEAD ignores Range", "HEAD", "", map[string]string{"Range": "1-1"}, 2, 0, 200, "0-1/*"},
		{"csv partial", "GET", "limit=1", map[string]string{"Prefer": "count=exact", "Accept": "text/csv"}, 1, 3, 206, "0-0/3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := listStatus(t, c.method, c.query, c.headers, c.n, c.total)
			require.Equal(t, c.status, w.Code, w.Body.String())
			assert.Equal(t, c.contentRange, w.Header().Get("Content-Range"))
			if c.status == 416 {
				var body map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				assert.Equal(t, "PGRST103", body["code"])
				assert.Equal(t, "Requested range not satisfiable", body["message"])
				assert.Contains(t, body["details"], "only")
			}
		})
	}
	w := listStatus(t, "GET", "offset=10", map[string]string{"Prefer": "count=exact", "Accept": "application/vnd.pgrst.object+json"}, 0, 5)
	require.Equal(t, 406, w.Code, "singular keeps PGRST116 ahead of 416")
	assert.Contains(t, w.Body.String(), "PGRST116")
	w = listStatus(t, "GET", "offset=10", exact, 0, 5)
	assert.JSONEq(t, `{"code":"PGRST103","message":"Requested range not satisfiable","details":"An offset of 10 was requested, but there are only 5 rows.","hint":""}`, w.Body.String())
}

func TestHandleList_SingularRangeStatus(t *testing.T) {
	singular := map[string]string{"Prefer": "count=exact", "Accept": "application/vnd.pgrst.object+json"}
	w := listStatus(t, "GET", "offset=5", singular, 1, 2)
	require.Equal(t, 416, w.Code, "416 replaces the row body, as in PostgREST")
	assert.JSONEq(t, `{"code":"PGRST103","message":"Requested range not satisfiable","details":"An offset of 5 was requested, but there are only 2 rows.","hint":""}`, w.Body.String())
	assert.Equal(t, "5-5/2", w.Header().Get("Content-Range"))

	w = listStatus(t, "GET", "", singular, 1, 3)
	require.Equal(t, 206, w.Code, w.Body.String())
	assert.JSONEq(t, `{"id":0}`, w.Body.String())

	w = listStatus(t, "GET", "", singular, 2, 3)
	require.Equal(t, 406, w.Code)
	assert.Contains(t, w.Body.String(), "PGRST116")
}

func TestHandleList_RangeHeaderIntersectsLimitOffset(t *testing.T) {
	for _, c := range []struct {
		name, method, query, rng, wantSQL string
	}{
		{"header only", "GET", "", "2-5", " LIMIT 4 OFFSET 2"},
		{"limit narrows header", "GET", "limit=2", "0-9", " LIMIT 2 OFFSET 0"},
		{"header narrows limit", "GET", "limit=10", "0-2", " LIMIT 3 OFFSET 0"},
		{"offset inside header", "GET", "offset=4", "2-9", " LIMIT 6 OFFSET 4"},
		{"header inside offset+limit", "GET", "offset=1&limit=10", "5-6", " LIMIT 2 OFFSET 5"},
		{"limit=0 bypasses header", "GET", "limit=0", "0-9", " LIMIT 0 OFFSET 0"},
		{"huge end", "GET", "", "0-9223372036854775807", ""},
		{"huge offset+limit", "GET", "offset=9223372036854775807&limit=9223372036854775807", "0-9223372036854775807", " LIMIT 1 OFFSET 9223372036854775807"},
		{"HEAD ignores header", "HEAD", "limit=5", "0-1", " LIMIT 5 OFFSET 0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, sql := listStatusSQL(t, c.method, c.query, map[string]string{"Range": c.rng}, 0, 0)
			require.Equal(t, 200, w.Code, w.Body.String())
			if c.wantSQL == "" {
				assert.NotContains(t, sql, "LIMIT")
				return
			}
			assert.True(t, strings.HasSuffix(sql, c.wantSQL), sql)
		})
	}
	w := listStatus(t, "GET", "offset=5", map[string]string{"Range": "0-1"}, 0, 0)
	require.Equal(t, 416, w.Code, w.Body.String())
	assert.JSONEq(t, `{"code":"PGRST103","message":"Requested range not satisfiable","details":"Limit should be greater than or equal to zero.","hint":""}`, w.Body.String())
}

func rpcStatus(t *testing.T, method, rawQuery string, headers map[string]string, rows []map[string]any) (*httptest.ResponseRecorder, []any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var args []any
	db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) {
		return &stubTx{queryFn: func(_ context.Context, _ string, a ...any) ([]map[string]any, error) {
			args = a
			return rows, nil
		}}, nil
	}}
	cfg := &domain.Config{RPC: map[string]domain.Function{"list": {
		Language: "sql", Volatility: "stable", ReturnCategory: "setof",
		Returns: domain.FuncReturn{Type: "setof todos"},
	}}}
	h := &CRUDHandler{cfg: cfg, db: db, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, "/rest/v1/rpc/list?"+rawQuery, nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	c.Params = gin.Params{{Key: "name", Value: "list"}}
	c.Set(contextKeySession, domain.Session{Role: "service_role", IsAuthenticated: true})
	h.handleRPC()(c)
	return w, args
}

// countedRows mimics the count=exact wrapper: n data rows, or one marker row when the page is empty.
func countedRows(n int, total int64) []map[string]any {
	if n == 0 {
		return []map[string]any{{"__inz_total": total}}
	}
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"id": int64(i), "__inz_total": total, "__inz_row": true}
	}
	return rows
}

func TestHandleRPC_RangeStatus(t *testing.T) {
	exact := map[string]string{"Prefer": "count=exact"}
	singular := map[string]string{"Prefer": "count=exact", "Accept": "application/vnd.pgrst.object+json"}
	for _, c := range []struct {
		name, query  string
		headers      map[string]string
		n            int
		total        int64
		status       int
		contentRange string
	}{
		{"no count", "", nil, 2, 0, 200, "0-1/*"},
		{"full", "", exact, 3, 3, 200, "0-2/3"},
		{"partial", "limit=1", exact, 1, 3, 206, "0-0/3"},
		{"offset == total", "offset=3", exact, 0, 3, 206, "*/3"},
		{"past the end", "offset=10", exact, 0, 3, 416, "*/3"},
		{"singular partial", "", singular, 1, 3, 206, "0-0/3"},
		{"singular past the end", "offset=5", singular, 1, 2, 416, "5-5/2"},
		{"singular empty keeps 406", "offset=10", singular, 0, 3, 406, "*/3"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, _ := rpcStatus(t, "POST", c.query, c.headers, countedRows(c.n, c.total))
			require.Equal(t, c.status, w.Code, w.Body.String())
			assert.Equal(t, c.contentRange, w.Header().Get("Content-Range"))
			if c.status == 416 {
				assert.Contains(t, w.Body.String(), "PGRST103")
				assert.NotContains(t, w.Body.String(), `"id"`)
			}
		})
	}
}

func TestHandleRPC_RangeHeader(t *testing.T) {
	for _, c := range []struct {
		name, method, query, rng string
		wantArgs                 []any
	}{
		{"GET honors header", "GET", "", "2-5", []any{4, 2}},
		{"GET intersects limit", "GET", "limit=2", "1-9", []any{1, 1}},
		{"GET intersects offset", "GET", "offset=3", "1-9", []any{7, 3}},
		{"POST ignores header", "POST", "", "2-5", nil},
		{"HEAD ignores header", "HEAD", "", "2-5", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			w, args := rpcStatus(t, c.method, c.query, map[string]string{"Range": c.rng}, nil)
			require.Equal(t, 200, w.Code, w.Body.String())
			if c.wantArgs == nil {
				assert.Empty(t, args)
				return
			}
			assert.Equal(t, c.wantArgs, args)
		})
	}
	w, _ := rpcStatus(t, "GET", "offset=5", map[string]string{"Range": "0-1"}, nil)
	require.Equal(t, 416, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "Limit should be greater than or equal to zero.")
	w, _ = rpcStatus(t, "GET", "", map[string]string{"Range": "x"}, nil)
	require.Equal(t, 400, w.Code, w.Body.String())
}
