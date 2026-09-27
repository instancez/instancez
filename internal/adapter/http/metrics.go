package http

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

type metricKey struct{ method, path, status string }

// Metrics tracks Prometheus-style metrics; label values are bounded by the route table.
type Metrics struct {
	mu             sync.Mutex
	requests       map[metricKey]int64
	durationSum    map[metricKey]float64
	durationCount  map[metricKey]int64
	activeRequests atomic.Int64
}

func newMetrics() *Metrics {
	return &Metrics{
		requests:      map[metricKey]int64{},
		durationSum:   map[metricKey]float64{},
		durationCount: map[metricKey]int64{},
	}
}

var globalMetrics = newMetrics()

func metricMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return m
	}
	return "OTHER"
}

// metricsMiddleware records request metrics, keyed by route template (not raw path).
func metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		m := globalMetrics
		m.activeRequests.Add(1)
		start := time.Now()
		c.Next()
		duration := time.Since(start).Seconds()
		m.activeRequests.Add(-1)

		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}
		route := metricKey{method: metricMethod(c.Request.Method), path: path}
		hit := route
		hit.status = strconv.Itoa(c.Writer.Status())

		m.mu.Lock()
		m.requests[hit]++
		m.durationSum[route] += duration
		m.durationCount[route]++
		m.mu.Unlock()
	}
}

func sortedKeys[V any](m map[metricKey]V) []metricKey {
	keys := make([]metricKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.path != b.path {
			return a.path < b.path
		}
		if a.method != b.method {
			return a.method < b.method
		}
		return a.status < b.status
	})
	return keys
}

// handleMetrics serves Prometheus text format metrics.
func handleMetrics(c *gin.Context) {
	m := globalMetrics
	var sb strings.Builder
	m.mu.Lock()
	sb.WriteString("# HELP instancez_http_requests_total Total HTTP requests\n# TYPE instancez_http_requests_total counter\n")
	for _, k := range sortedKeys(m.requests) {
		fmt.Fprintf(&sb, "instancez_http_requests_total{method=%q,path=%q,status=%q} %d\n", k.method, k.path, k.status, m.requests[k])
	}
	sb.WriteString("\n# HELP instancez_http_request_duration_seconds HTTP request duration\n# TYPE instancez_http_request_duration_seconds summary\n")
	for _, k := range sortedKeys(m.durationCount) {
		fmt.Fprintf(&sb, "instancez_http_request_duration_seconds_count{method=%q,path=%q} %d\n", k.method, k.path, m.durationCount[k])
		fmt.Fprintf(&sb, "instancez_http_request_duration_seconds_sum{method=%q,path=%q} %g\n", k.method, k.path, m.durationSum[k])
	}
	m.mu.Unlock()
	sb.WriteString("\n# HELP instancez_http_active_requests Current active requests\n# TYPE instancez_http_active_requests gauge\n")
	fmt.Fprintf(&sb, "instancez_http_active_requests %d\n", m.activeRequests.Load())
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.String(200, sb.String())
}
