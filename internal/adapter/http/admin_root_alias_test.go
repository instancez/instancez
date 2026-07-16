package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/domain"
)

// newServerForAdminAliasTest builds the minimum ServerDeps needed to exercise
// the real NewServer wiring without a live database. It wires a stubDB so that
// nothing panics when the router is constructed, but skips auth/storage/etc.
func newServerForAdminAliasTest(t *testing.T) http.Handler {
	t.Helper()
	gin.SetMode(gin.TestMode)
	deps := ServerDeps{
		Config: &domain.Config{
			Project: domain.Project{Name: "test"},
		},
		DB:            domain.RequestDB{Database: &stubDB{}},
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		DashboardMode: DashboardDisabled,
	}
	return NewServer(deps).Handler()
}

// TestAdminRootAlias_EnvVarsReachableWithoutApiPrefix verifies that the admin
// API is reachable at /_admin/* (root-level) — i.e. it survives the
// /api-prefix-stripping proxy that Traefik applies on the platform.
//
// The route is gated by adminKeyAuth, so the test sets the admin key and sends
// the correct bearer token. A 200 response from /_admin/config/env-vars
// confirms (a) the route is registered at root and (b) the auth middleware is
// present and passes a valid key.
//
// Before the fix this test returns 404 (route not registered); after the fix
// it returns 200.
func TestAdminRootAlias_EnvVarsReachableWithoutApiPrefix(t *testing.T) {
	t.Setenv("INSTANCEZ_SECRET_KEY", "test-key-alias")

	handler := newServerForAdminAliasTest(t)

	req := httptest.NewRequest(http.MethodGet, "/_admin/config/env-vars", nil)
	req.Header.Set("Authorization", "Bearer test-key-alias")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("/_admin/config/env-vars: expected 200 (root alias registered), got %d — is the root /_admin alias missing from NewServer?", w.Code)
	}
}

// TestAdminRootAlias_AuthMiddlewareEnforcedAtRoot verifies that the auth
// middleware is active on the root /_admin alias — a request without a valid
// key must be rejected (401), not silently served.
func TestAdminRootAlias_AuthMiddlewareEnforcedAtRoot(t *testing.T) {
	t.Setenv("INSTANCEZ_SECRET_KEY", "test-key-alias")

	handler := newServerForAdminAliasTest(t)

	req := httptest.NewRequest(http.MethodGet, "/_admin/config/env-vars", nil)
	// No Authorization header — must be rejected.
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("/_admin/config/env-vars without auth: expected 401, got %d — auth middleware not enforced on root alias", w.Code)
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/_admin/config/env-vars without auth: expected 401, got %d", w.Code)
	}
}
