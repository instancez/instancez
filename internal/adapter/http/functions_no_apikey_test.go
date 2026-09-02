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

// TestFunctionsApikeyOptional: /functions/v1 validates the apikey only when
// present. A keyless webhook caller reaches the runtime; a valid key reaches
// it too; a garbage key is rejected 401 before invoke. The /rest/v1 control
// proves apiKeyGuard is untouched elsewhere.
func TestFunctionsApikeyOptional(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", "inz_publishable_fntest")
	t.Setenv("INSTANCEZ_SECRET_KEY", "inz_secret_fntest")

	cfg, err := config.ParseBytes([]byte("version: 1\nproject:\n  name: \"test\"\ntables: {}\n"), "test.yaml")
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}

	newHandler := func(rt *fakeRuntime) http.Handler {
		return NewServer(ServerDeps{
			Config:          cfg,
			DB:              domain.RequestDB{Database: &stubDB{}},
			Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
			DashboardMode:   DashboardDisabled,
			JWTKeys:         stubKeys(t),
			FunctionRuntime: rt,
		}).Handler()
	}

	cases := []struct {
		name        string
		apikey      string
		wantStatus  int
		wantInvoked bool
	}{
		{"keyless webhook reaches runtime", "", 200, true},
		{"publishable key reaches runtime", "inz_publishable_fntest", 200, true},
		{"secret key reaches runtime", "inz_secret_fntest", 200, true},
		{"garbage key rejected before invoke", "not-a-real-key", 401, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &fakeRuntime{known: map[string]*domain.FunctionResponse{
				"hook": {Status: 200, Body: []byte(`{"ok":true}`)},
			}}
			req := httptest.NewRequest(http.MethodPost, "/functions/v1/hook", nil)
			if tc.apikey != "" {
				req.Header.Set("apikey", tc.apikey)
			}
			w := httptest.NewRecorder()
			newHandler(rt).ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", w.Code, tc.wantStatus, w.Body.String())
			}
			if rt.invokeCalled != tc.wantInvoked {
				t.Fatalf("invokeCalled = %v, want %v", rt.invokeCalled, tc.wantInvoked)
			}
		})
	}

	// Control: /rest/v1 without apikey is still hard-required (rpc route exists).
	w := httptest.NewRecorder()
	newHandler(&fakeRuntime{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/rest/v1/rpc/anything", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("keyless /rest/v1: want 401 (apiKeyGuard intact), got %d (body: %s)", w.Code, w.Body.String())
	}
}
