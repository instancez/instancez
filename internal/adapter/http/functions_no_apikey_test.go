package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

// TestFunctionsReachableWithoutApikey: a keyless POST reaches /functions/v1
// while the same keyless request to /rest/v1 stays guarded by apiKeyGuard.
func TestFunctionsReachableWithoutApikey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg, err := config.ParseBytes([]byte("version: 1\nproject:\n  name: \"test\"\ntables: {}\n"), "test.yaml")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}

	rt := &fakeRuntime{known: map[string]*domain.FunctionResponse{
		"hook": {Status: 200, Body: []byte(`{"ok":true}`)},
	}}
	deps := ServerDeps{
		Config:          cfg,
		DB:              domain.RequestDB{Database: &stubDB{}},
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		DashboardMode:   DashboardDisabled,
		JWTKeys:         stubKeys(t),
		FunctionRuntime: rt,
	}
	handler := NewServer(deps).Handler()

	// Function: keyless POST reaches the runtime and returns its body.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/functions/v1/hook", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("keyless function call: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	if !rt.invokeCalled {
		t.Fatal("runtime must be invoked for a keyless function call")
	}

	// Control: /rest/v1 without apikey is still guarded (rpc route always exists).
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/rest/v1/rpc/anything", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("keyless /rest/v1: want 401 (apiKeyGuard intact), got %d (body: %s)", w.Code, w.Body.String())
	}
}
