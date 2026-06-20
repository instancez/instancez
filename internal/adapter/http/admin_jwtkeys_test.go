package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
)

func TestHandleJWTKey_ReturnsPublicOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	km, err := app.NewInMemoryJWTKeyManager("kid-abc", nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &AdminHandler{jwtKeys: km}

	r := gin.New()
	r.GET("/api/_admin/jwt-keys", h.handleJWTKey)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/_admin/jwt-keys", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["kid"] != "kid-abc" || got["algorithm"] != "RS256" {
		t.Fatalf("unexpected body: %v", got)
	}
	// No private material anywhere in the response.
	if strings.Contains(w.Body.String(), "PRIVATE KEY") || strings.Contains(w.Body.String(), `"d"`) {
		t.Fatal("response leaked private key material")
	}
}

func TestHandleJWTKey_NoManager(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AdminHandler{}
	r := gin.New()
	r.GET("/api/_admin/jwt-keys", h.handleJWTKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/_admin/jwt-keys", nil))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", w.Code)
	}
}
