package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func listStatus(t *testing.T, method, rawQuery string, headers map[string]string, n int, total int64) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rows := make([]map[string]any, n)
	for i := range rows {
		rows[i] = map[string]any{"id": int64(i)}
	}
	db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) {
		return &stubTx{
			queryFn: func(context.Context, string, ...any) ([]map[string]any, error) { return rows, nil },
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
	return w
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
