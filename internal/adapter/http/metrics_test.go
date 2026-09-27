package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
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

func metricsEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	prev := globalMetrics
	globalMetrics = newMetrics()
	t.Cleanup(func() { globalMetrics = prev })
	r := gin.New()
	r.Use(metricsMiddleware())
	r.GET("/rest/v1/rpc/:name", func(c *gin.Context) { c.Status(200) })
	r.GET("/metrics", handleMetrics)
	return r
}

func scrape(r *gin.Engine) string {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return w.Body.String()
}

func TestMetrics_LabelsAreRouteTemplates(t *testing.T) {
	r := metricsEngine(t)
	for i := 0; i < 500; i++ {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/rest/v1/nope-%d", i), nil))
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, fmt.Sprintf("/rest/v1/rpc/fn-%d", i), nil))
	}
	if n := len(globalMetrics.requests); n != 2 {
		t.Fatalf("request series = %d, want 2 (route template + unmatched)", n)
	}
	out := scrape(r)
	for _, want := range []string{
		`instancez_http_requests_total{method="GET",path="/rest/v1/rpc/:name",status="200"} 500`,
		`instancez_http_requests_total{method="GET",path="unmatched",status="404"} 500`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "nope-1") || strings.Contains(out, "fn-1") {
		t.Error("raw paths leaked into labels")
	}
}

func TestMetrics_UnknownMethodsCollapse(t *testing.T) {
	r := metricsEngine(t)
	for i := 0; i < 50; i++ {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(fmt.Sprintf("M%d", i), "/rest/v1/rpc/x", nil))
	}
	if n := len(globalMetrics.requests); n != 1 {
		t.Fatalf("series = %d, want 1", n)
	}
	if !strings.Contains(scrape(r), `method="OTHER"`) {
		t.Error("custom methods must collapse to OTHER")
	}
}

func TestMetrics_CumulativeUnderConcurrency(t *testing.T) {
	r := metricsEngine(t)
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/rest/v1/rpc/x", nil))
			}
		}()
	}
	wg.Wait()
	out := scrape(r)
	if !strings.Contains(out, `instancez_http_request_duration_seconds_count{method="GET",path="/rest/v1/rpc/:name"} 1500`) {
		t.Errorf("count must be cumulative past the old 1000 window:\n%s", out)
	}
	if strings.Contains(out, "quantile") {
		t.Error("mean must not be published as a quantile")
	}
}
