package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/domain"
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
	body := w.Body.String()
	if strings.Contains(body, "PRIVATE KEY") || strings.Contains(body, `"d":`) {
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

func TestHandleRotateJWTKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	km := app.NewJWTKeyManager(&rotateFakeDB{})
	h := &AdminHandler{jwtKeys: km, logger: slog.Default()}

	r := gin.New()
	r.POST("/api/_admin/jwt-keys/rotate", h.handleRotateJWTKey)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/_admin/jwt-keys/rotate", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["kid"] == "" || got["algorithm"] != "RS256" {
		t.Fatalf("unexpected body: %v", got)
	}
	body := w.Body.String()
	if strings.Contains(body, "PRIVATE KEY") {
		t.Fatal("rotate leaked private key material")
	}
}

// rotateFakeDB is a minimal domain.Database: Active() finds no existing key
// (QueryRow -> nil) and rotate's two Exec calls succeed.
type rotateFakeDB struct{}

func (rotateFakeDB) Close() error                                           { return nil }
func (rotateFakeDB) Ping(ctx context.Context) error                         { return nil }
func (rotateFakeDB) EnsureMigrationsTable(ctx context.Context) error         { return nil }
func (rotateFakeDB) GetLastMigration(ctx context.Context) (*domain.Migration, error) {
	return nil, nil
}
func (rotateFakeDB) RecordMigration(ctx context.Context, checksum, sql, configJSON string) error {
	return nil
}
func (rotateFakeDB) Query(ctx context.Context, q string, a ...any) ([]map[string]any, error) {
	return nil, nil
}
func (rotateFakeDB) QueryRow(ctx context.Context, q string, a ...any) (map[string]any, error) {
	return nil, nil
}
func (rotateFakeDB) Exec(ctx context.Context, q string, a ...any) (int64, error) { return 1, nil }
func (rotateFakeDB) ExecDDL(ctx context.Context, sql string) error               { return nil }
func (rotateFakeDB) WithRLS(ctx context.Context, session domain.Session) (context.Context, error) {
	return ctx, nil
}
func (rotateFakeDB) Begin(ctx context.Context) (domain.Tx, error) {
	return nil, nil
}
