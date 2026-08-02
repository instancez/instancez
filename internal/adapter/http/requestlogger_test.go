package http

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

// captureHandler records the context and record handed to each Handle call, so
// tests can assert both what was logged and — the load-bearing part for trace
// correlation — that the request's context was threaded through.
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
	ctxs    []context.Context
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	h.ctxs = append(h.ctxs, ctx)
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

type reqLogCtxKey struct{}

// runRequestLogger drives the middleware once against a request whose context
// carries a sentinel value, and returns the sentinel so callers can assert it
// reached the handler.
func runRequestLogger(logger *slog.Logger, devMode bool, bridge slog.Handler) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest("GET", "/rest/v1/things", nil)
	req = req.WithContext(context.WithValue(req.Context(), reqLogCtxKey{}, "sentinel"))
	c.Request = req
	requestLogger(logger, devMode, bridge)(c)
	return c
}

// In prod, the request record must be emitted with the request's context so the
// slog bridge can stamp trace_id/span_id onto it (trace↔log correlation).
func TestRequestLoggerProdThreadsRequestContext(t *testing.T) {
	cap := &captureHandler{}
	runRequestLogger(slog.New(cap), false, nil)

	if len(cap.records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(cap.records))
	}
	if cap.records[0].Message != "request" {
		t.Fatalf("message = %q, want %q", cap.records[0].Message, "request")
	}
	if got := cap.ctxs[0].Value(reqLogCtxKey{}); got != "sentinel" {
		t.Fatalf("handler received ctx without the request value (got %v) — correlation would be lost", got)
	}
}

// In dev, the pretty stdout line is kept, but when the OTLP bridge is present
// (OTel enabled) the request is also exported through it, carrying the request
// context.
func TestRequestLoggerDevExportsThroughBridge(t *testing.T) {
	bridge := &captureHandler{}
	// Dev's main logger is not used for the request line; discard it.
	runRequestLogger(slog.New(slog.NewTextHandler(io.Discard, nil)), true, bridge)

	if len(bridge.records) != 1 {
		t.Fatalf("expected 1 exported record via bridge, got %d", len(bridge.records))
	}
	if bridge.records[0].Message != "request" {
		t.Fatalf("bridge message = %q, want %q", bridge.records[0].Message, "request")
	}
	if got := bridge.ctxs[0].Value(reqLogCtxKey{}); got != "sentinel" {
		t.Fatalf("bridge received ctx without the request value (got %v)", got)
	}
}

// With no bridge (OTel off) dev emits nothing through slog — only the pretty
// stdout line — so behavior is unchanged from before.
func TestRequestLoggerDevNoBridgeIsSilentToSlog(t *testing.T) {
	cap := &captureHandler{}
	runRequestLogger(slog.New(cap), true, nil)

	if len(cap.records) != 0 {
		t.Fatalf("dev with no bridge should not emit through slog, got %d records", len(cap.records))
	}
}
