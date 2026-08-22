package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
)

// queryStubDB embeds stubDB (for domain.Database) and additionally implements
// sqlRunner, recording the call it received and returning canned results.
type queryStubDB struct {
	stubDB
	gotSQL      string
	gotReadOnly bool
	gotLimit    int
	cols        []string
	rows        [][]any
	err         error
}

func (s *queryStubDB) RunSQL(ctx context.Context, sql string, readOnly bool, limit int) ([]string, [][]any, error) {
	s.gotSQL = sql
	s.gotReadOnly = readOnly
	s.gotLimit = limit
	return s.cols, s.rows, s.err
}

// newQueryTestHandler builds the minimal AdminHandler the query tests need;
// runner may be nil to exercise the no-RunSQL-support path.
func newQueryTestHandler(runner *queryStubDB, mode DashboardMode) *AdminHandler {
	h := &AdminHandler{
		cfg:           &domain.Config{},
		db:            &stubDB{},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		dashboardMode: mode,
	}
	if runner != nil {
		h.ownerDB = domain.OwnerDB{Database: runner}
	}
	return h
}

func newQueryTestRouter(h *AdminHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/_admin/query", h.handleQuery)
	return r
}

func doQuery(h *AdminHandler, body string) *httptest.ResponseRecorder {
	r := newQueryTestRouter(h)
	req := httptest.NewRequest(http.MethodPost, "/api/_admin/query", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestHandleQuerySuccess(t *testing.T) {
	runner := &queryStubDB{cols: []string{"a", "b"}, rows: [][]any{{"1", "x"}}}
	h := newQueryTestHandler(runner, DashboardReadwrite)
	w := doQuery(h, `{"sql":"SELECT 1 AS a, 'x' AS b"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Columns  []string `json:"columns"`
		Rows     [][]any  `json:"rows"`
		RowCount int      `json:"row_count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
	}
	if len(got.Columns) != 2 || got.Columns[0] != "a" || got.Columns[1] != "b" {
		t.Fatalf("columns = %v", got.Columns)
	}
	if got.RowCount != 1 || len(got.Rows) != 1 {
		t.Fatalf("rows/row_count = %v/%d", got.Rows, got.RowCount)
	}
	if runner.gotReadOnly != false {
		t.Fatalf("gotReadOnly = %v, want false", runner.gotReadOnly)
	}
	if runner.gotLimit != sqlEditorRowLimit {
		t.Fatalf("gotLimit = %d, want %d", runner.gotLimit, sqlEditorRowLimit)
	}
	if runner.gotSQL != "SELECT 1 AS a, 'x' AS b" {
		t.Fatalf("gotSQL = %q", runner.gotSQL)
	}
}

func TestHandleQueryEmptySQL(t *testing.T) {
	for _, body := range []string{`{"sql":""}`, `{"sql":"   "}`, `{}`} {
		runner := &queryStubDB{}
		h := newQueryTestHandler(runner, DashboardReadwrite)
		w := doQuery(h, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %q: expected 400, got %d: %s", body, w.Code, w.Body.String())
		}
	}
}

func TestHandleQueryRunnerError(t *testing.T) {
	runner := &queryStubDB{err: errors.New("syntax error at or near \"SELCT\"")}
	h := newQueryTestHandler(runner, DashboardReadwrite)
	w := doQuery(h, `{"sql":"SELECT 1"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["error"] != "query_failed" {
		t.Fatalf(`expected error="query_failed", got %v`, got["error"])
	}
}

func TestHandleQueryNoRunner(t *testing.T) {
	// ownerDB intentionally unset (nil runner); h.db is a plain stubDB lacking RunSQL.
	h := newQueryTestHandler(nil, DashboardReadwrite)
	w := doQuery(h, `{"sql":"SELECT 1"}`)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleQueryDisabled(t *testing.T) {
	runner := &queryStubDB{}
	h := newQueryTestHandler(runner, DashboardDisabled)
	w := doQuery(h, `{"sql":"SELECT 1"}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got["error"] != "dashboard_disabled" {
		t.Fatalf(`expected error="dashboard_disabled", got %v`, got["error"])
	}
	if runner.gotSQL != "" {
		t.Fatal("runner should not have been called")
	}
}

func TestHandleQueryReadonlyModePassesReadOnlyTrue(t *testing.T) {
	runner := &queryStubDB{}
	h := newQueryTestHandler(runner, DashboardReadonly)
	w := doQuery(h, `{"sql":"SELECT 1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !runner.gotReadOnly {
		t.Fatal("gotReadOnly = false, want true in readonly dashboard mode")
	}
}

func TestHandleQueryAuditLogSuccess(t *testing.T) {
	var buf bytes.Buffer
	runner := &queryStubDB{cols: []string{"a"}, rows: [][]any{{"1"}}}
	h := newQueryTestHandler(runner, DashboardReadwrite)
	h.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	w := doQuery(h, `{"sql":"select secret_column from t"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	log := buf.String()
	if !strings.Contains(log, "sql editor query") {
		t.Fatalf("log missing \"sql editor query\": %s", log)
	}
	if !strings.Contains(log, `"rows":1`) {
		t.Fatalf("log missing rows=1: %s", log)
	}
	if !strings.Contains(log, `"read_only":false`) {
		t.Fatalf("log missing read_only=false: %s", log)
	}
	if strings.Contains(log, "secret_column") {
		t.Fatalf("log must not contain SQL text: %s", log)
	}
}

func TestHandleQueryAuditLogFailure(t *testing.T) {
	var buf bytes.Buffer
	runner := &queryStubDB{err: errors.New("syntax error")}
	h := newQueryTestHandler(runner, DashboardReadwrite)
	h.logger = slog.New(slog.NewJSONHandler(&buf, nil))

	w := doQuery(h, `{"sql":"SELECT 1"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	log := buf.String()
	if !strings.Contains(log, "sql editor query failed") {
		t.Fatalf("log missing \"sql editor query failed\": %s", log)
	}
}
