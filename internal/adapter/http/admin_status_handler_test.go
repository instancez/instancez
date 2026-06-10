package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
)

func TestConfigStatusOK(t *testing.T) {
	tracker := app.NewDriftTracker("file://./instancez.yaml")
	tracker.MarkOK("abcd1234", time.Now())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &AdminHandler{
		dashboardMode: DashboardReadwrite,
		driftFn:       func() *app.DriftTracker { return tracker },
	}
	r.GET("/api/_admin/config/status", h.handleConfigStatus)

	req := httptest.NewRequest("GET", "/api/_admin/config/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body %s)", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "ok" {
		t.Fatalf("status field = %v", got["status"])
	}
	running, _ := got["running"].(map[string]any)
	if running["checksum"] != "abcd1234" {
		t.Fatalf("running.checksum = %v", running["checksum"])
	}
	if got["dashboard_mode"] != "readwrite" {
		t.Fatalf("dashboard_mode = %v", got["dashboard_mode"])
	}
}

func TestConfigStatusDrift(t *testing.T) {
	tracker := app.NewDriftTracker("s3://bucket/key")
	tracker.MarkOK("good", time.Now())
	tracker.MarkDrift("bad", `ERROR: column "foo" cannot be cast`, time.Now())

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &AdminHandler{
		dashboardMode: DashboardReadonly,
		driftFn:       func() *app.DriftTracker { return tracker },
	}
	r.GET("/api/_admin/config/status", h.handleConfigStatus)

	req := httptest.NewRequest("GET", "/api/_admin/config/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "drift" {
		t.Fatalf("status = %v", got["status"])
	}
	if got["last_error"] == nil || got["last_error"] == "" {
		t.Fatalf("last_error must be set")
	}
	if got["dashboard_mode"] != "readonly" {
		t.Fatalf("dashboard_mode = %v", got["dashboard_mode"])
	}
}

func TestConfigStatusUnknownWhenTrackerNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &AdminHandler{
		dashboardMode: DashboardDisabled,
		driftFn:       func() *app.DriftTracker { return nil },
	}
	r.GET("/api/_admin/config/status", h.handleConfigStatus)

	req := httptest.NewRequest("GET", "/api/_admin/config/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["status"] != "unknown" {
		t.Fatalf("status = %v", got["status"])
	}
}
