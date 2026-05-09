package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDashboardDisabledReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	MountDashboard(r, nil, false, DashboardDisabled)

	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when dashboard disabled, got %d", w.Code)
	}
}

func TestDashboardReadonlyServesSPA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	MountDashboard(r, nil, true, DashboardReadonly)

	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 in readonly mode (dev placeholder), got %d", w.Code)
	}
}

func TestDashboardReadwriteServesSPA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	MountDashboard(r, nil, true, DashboardReadwrite)

	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 in readwrite mode (dev placeholder), got %d", w.Code)
	}
}
