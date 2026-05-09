package http

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/saedx1/ultrabase/internal/domain"
)

// newAdminTestRouter wires a minimal gin engine with PUT /api/_admin/config
// directly to the handler. It deliberately skips adminKeyAuth so the tests
// can exercise the gating logic without needing ULTRABASE_ADMIN_KEY plumbing.
func newAdminTestRouter(h *AdminHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/api/_admin/config", h.handlePutConfig)
	r.GET("/api/_admin/config", h.handleGetConfig)
	return r
}

func TestPutConfigForbidWhenDisabled(t *testing.T) {
	h := &AdminHandler{
		cfg:           &domain.Config{},
		db:            &stubDB{},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		dashboardMode: DashboardDisabled,
	}
	r := newAdminTestRouter(h)

	body := bytes.NewReader([]byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodPut, "/api/_admin/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
	}
	if got["error"] != "dashboard_disabled" {
		t.Fatalf(`expected error="dashboard_disabled", got %v`, got["error"])
	}
}

func TestPutConfigForbidWhenReadonly(t *testing.T) {
	h := &AdminHandler{
		cfg:           &domain.Config{},
		db:            &stubDB{},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		dashboardMode: DashboardReadonly,
	}
	r := newAdminTestRouter(h)

	body := bytes.NewReader([]byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodPut, "/api/_admin/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
	}
	if got["error"] != "dashboard_readonly" {
		t.Fatalf(`expected error="dashboard_readonly", got %v`, got["error"])
	}
}

// TestPutConfigReadwriteWithoutSourceReturns501 covers the readwrite branch
// where no Source was wired (defensive guard). The handler must not panic
// and must surface a 501.
func TestPutConfigReadwriteWithoutSourceReturns501(t *testing.T) {
	h := &AdminHandler{
		cfg:           &domain.Config{},
		db:            &stubDB{},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		dashboardMode: DashboardReadwrite,
		// configSource intentionally nil
	}
	r := newAdminTestRouter(h)

	body := bytes.NewReader([]byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodPut, "/api/_admin/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
	}
}

// TestGetConfigUsesLiveConfig verifies handleGetConfig returns the engine's
// current running config (via configFn) rather than re-reading from a path.
func TestGetConfigUsesLiveConfig(t *testing.T) {
	live := &domain.Config{Version: 7}
	h := &AdminHandler{
		cfg:      &domain.Config{Version: 1}, // boot-time
		configFn: func() *domain.Config { return live },
		db:       &stubDB{},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r := newAdminTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
	}
	// JSON numbers decode as float64.
	if v, _ := got["version"].(float64); v != 7 {
		t.Fatalf("expected live version=7, got %v", got["version"])
	}
	// _checksum should be omitted when no source is wired.
	if _, ok := got["_checksum"]; ok {
		t.Fatalf("did not expect _checksum without a configured source")
	}
}
