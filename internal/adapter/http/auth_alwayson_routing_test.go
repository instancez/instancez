package http

import (
	"encoding/json"
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

func TestErrorBody_ErrorCodeOnlyOnAuthRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg, err := config.ParseBytes([]byte("version: 1\nproject:\n  name: \"test\"\ntables:\n  todos:\n    rls_enabled: false\n    fields:\n      id: { type: uuid, primary_key: true }\n"), "test.yaml")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	handler := NewServer(ServerDeps{Config: cfg, DB: domain.RequestDB{Database: &stubDB{}},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DashboardMode: DashboardDisabled}).Handler()
	for path, wantErrorCode := range map[string]bool{"/auth/v1/user": true, "/auth/v1/factors": true, "/rest/v1/todos?select=nope(": false} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code < 400 {
			t.Fatalf("%s: status %d body %s", path, w.Code, w.Body.String())
		}
		ec, has := body["error_code"]
		if has != wantErrorCode || (has && ec != body["code"]) || body["code"] == "" {
			t.Errorf("%s: error_code=%v (present %v), code=%v; want present=%v and equal to code", path, ec, has, body["code"], wantErrorCode)
		}
		for _, k := range []string{"code", "message", "details", "hint"} {
			if _, ok := body[k]; !ok {
				t.Errorf("%s: missing %s in %s", path, k, w.Body.String())
			}
		}
	}
}
