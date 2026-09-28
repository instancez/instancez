package http

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/instancez/instancez/internal/domain"
)

// A failed live-key lookup aborts the tx, so the upsert must stop with that error.
func TestUpsert_LivePrimaryKeyLookupErrorIsReported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, c := range []struct{ method, prefer string }{
		{"POST", "resolution=merge-duplicates"},
		{"POST", "resolution=ignore-duplicates"},
		{"PUT", ""},
	} {
		t.Run(c.method+" "+c.prefer, func(t *testing.T) {
			var wrote []string
			db := &stubDB{beginFn: func(context.Context) (domain.Tx, error) {
				return &stubTx{
					queryRowFn: func(context.Context, string, ...any) (map[string]any, error) {
						return nil, &pgconn.PgError{Code: "42501", Message: "permission denied for table pg_index"}
					},
					queryFn: func(_ context.Context, q string, _ ...any) ([]map[string]any, error) {
						wrote = append(wrote, q)
						return nil, nil
					},
					execFn: func(_ context.Context, q string, _ ...any) (int64, error) {
						wrote = append(wrote, q)
						return 0, nil
					},
				}, nil
			}}
			h := &CRUDHandler{cfg: &domain.Config{}, db: db, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = httptest.NewRequest(c.method, "/rest/v1/todos", strings.NewReader(`{"id":1,"title":"x"}`))
			ctx.Request.Header.Set("Content-Type", "application/json")
			if c.prefer != "" {
				ctx.Request.Header.Set("Prefer", c.prefer)
			}
			ctx.Set(contextKeySession, domain.Session{Role: "service_role", IsAuthenticated: true})
			if c.method == "PUT" {
				h.handleUpsert("todos", testTable())(ctx)
			} else {
				h.handleCreate("todos", testTable())(ctx)
			}
			require.Equal(t, 403, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"code":"42501"`)
			assert.Empty(t, wrote, "no write may run after the lookup failed")
		})
	}
}
