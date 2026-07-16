package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMetricsRequiresAdminKey asserts /metrics — which exposes
// request/latency internals — is gated behind the same admin key as
// /_admin/*, not open to any caller.
func TestMetricsRequiresAdminKey(t *testing.T) {
	t.Setenv("INSTANCEZ_SECRET_KEY", "test-key-metrics")

	handler := newServerForAdminAliasTest(t)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	// No Authorization header — must be rejected.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("/metrics without auth: expected non-200, got %d — admin key not enforced on /metrics", w.Code)
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/metrics without auth: expected 401, got %d", w.Code)
	}
}

// TestMetricsServesWithAdminKey asserts a correctly-authenticated scrape
// still succeeds.
func TestMetricsServesWithAdminKey(t *testing.T) {
	t.Setenv("INSTANCEZ_SECRET_KEY", "test-key-metrics")

	handler := newServerForAdminAliasTest(t)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer test-key-metrics")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("/metrics with valid admin key: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
