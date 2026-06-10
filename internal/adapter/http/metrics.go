package http

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

// Metrics tracks Prometheus-style metrics.
type Metrics struct {
	mu               sync.RWMutex
	requestCount     map[string]*atomic.Int64 // method:path:status -> count
	requestDurations map[string][]float64     // method:path -> durations in seconds
	activeRequests   atomic.Int64
}

var globalMetrics = &Metrics{
	requestCount:     make(map[string]*atomic.Int64),
	requestDurations: make(map[string][]float64),
}

// metricsMiddleware records request metrics.
func metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		globalMetrics.activeRequests.Add(1)
		start := time.Now()

		c.Next()

		duration := time.Since(start).Seconds()
		globalMetrics.activeRequests.Add(-1)

		method := c.Request.Method
		path := normalizePath(c.Request.URL.Path)
		status := strconv.Itoa(c.Writer.Status())

		// Request count
		key := method + ":" + path + ":" + status
		globalMetrics.mu.Lock()
		counter, ok := globalMetrics.requestCount[key]
		if !ok {
			counter = &atomic.Int64{}
			globalMetrics.requestCount[key] = counter
		}

		// Duration histogram (simplified — store recent durations)
		durationKey := method + ":" + path
		globalMetrics.requestDurations[durationKey] = append(
			globalMetrics.requestDurations[durationKey], duration)
		// Keep max 1000 recent durations per endpoint
		if len(globalMetrics.requestDurations[durationKey]) > 1000 {
			globalMetrics.requestDurations[durationKey] = globalMetrics.requestDurations[durationKey][500:]
		}
		globalMetrics.mu.Unlock()

		counter.Add(1)
	}
}

// handleMetrics serves Prometheus text format metrics.
func handleMetrics(c *gin.Context) {
	var sb strings.Builder

	sb.WriteString("# HELP instancez_http_requests_total Total HTTP requests\n")
	sb.WriteString("# TYPE instancez_http_requests_total counter\n")

	globalMetrics.mu.RLock()
	defer globalMetrics.mu.RUnlock()

	// Sort keys for stable output
	countKeys := make([]string, 0, len(globalMetrics.requestCount))
	for k := range globalMetrics.requestCount {
		countKeys = append(countKeys, k)
	}
	sort.Strings(countKeys)

	for _, key := range countKeys {
		parts := strings.SplitN(key, ":", 3)
		if len(parts) != 3 {
			continue
		}
		method, path, status := parts[0], parts[1], parts[2]
		count := globalMetrics.requestCount[key].Load()
		sb.WriteString(fmt.Sprintf(
			"instancez_http_requests_total{method=%q,path=%q,status=%q} %d\n",
			method, path, status, count))
	}

	sb.WriteString("\n# HELP instancez_http_request_duration_seconds HTTP request duration\n")
	sb.WriteString("# TYPE instancez_http_request_duration_seconds summary\n")

	durKeys := make([]string, 0, len(globalMetrics.requestDurations))
	for k := range globalMetrics.requestDurations {
		durKeys = append(durKeys, k)
	}
	sort.Strings(durKeys)

	for _, key := range durKeys {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		method, path := parts[0], parts[1]
		durations := globalMetrics.requestDurations[key]
		if len(durations) == 0 {
			continue
		}

		sum := 0.0
		for _, d := range durations {
			sum += d
		}
		avg := sum / float64(len(durations))

		sb.WriteString(fmt.Sprintf(
			"instancez_http_request_duration_seconds{method=%q,path=%q,quantile=\"0.5\"} %g\n",
			method, path, avg))
		sb.WriteString(fmt.Sprintf(
			"instancez_http_request_duration_seconds_count{method=%q,path=%q} %d\n",
			method, path, len(durations)))
		sb.WriteString(fmt.Sprintf(
			"instancez_http_request_duration_seconds_sum{method=%q,path=%q} %g\n",
			method, path, sum))
	}

	sb.WriteString(fmt.Sprintf("\n# HELP instancez_http_active_requests Current active requests\n"))
	sb.WriteString(fmt.Sprintf("# TYPE instancez_http_active_requests gauge\n"))
	sb.WriteString(fmt.Sprintf("instancez_http_active_requests %d\n", globalMetrics.activeRequests.Load()))

	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.String(200, sb.String())
}

func normalizePath(path string) string {
	// Group dynamic segments for aggregation
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if len(p) > 0 && isLikelyID(p) {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

func isLikelyID(s string) bool {
	// Heuristic: pure numeric or UUID-like
	if len(s) > 8 {
		for _, c := range s {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || c == '-') {
				return false
			}
		}
		return true
	}
	return false
}
