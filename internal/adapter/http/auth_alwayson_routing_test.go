package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

// TestNewServer_AuthMountedWithoutAuthBlock proves the loader chokepoint
// (config.ParseBytes -> applyDefaults) feeds NewServer's Config.Auth != nil
// guard: a config with no auth: block must still mount /auth/v1/*.
func TestNewServer_AuthMountedWithoutAuthBlock(t *testing.T) {
	gin.SetMode(gin.TestMode)

	yaml := []byte("version: 1\nproject:\n  name: \"test\"\ntables: {}\n")
	cfg, err := config.ParseBytes(yaml, "test.yaml")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if cfg.Auth == nil {
		t.Fatal("loader must always populate Auth (always-on)")
	}

	deps := ServerDeps{
		Config:        cfg,
		DB:            domain.RequestDB{Database: &stubDB{}},
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		DashboardMode: DashboardDisabled,
	}
	handler := NewServer(deps).Handler()

	req := httptest.NewRequest(http.MethodPost, "/auth/v1/signup", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("POST /auth/v1/signup: got 404 — /auth/v1 not mounted for an auth-less config")
	}
}
