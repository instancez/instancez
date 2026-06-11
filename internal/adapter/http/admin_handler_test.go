package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/instancez/instancez/internal/app"
	"github.com/instancez/instancez/internal/config"
	"github.com/instancez/instancez/internal/domain"
)

// stubSource is a fake config.Source for admin handler tests. Read returns
// the canned bytes/version; Write either succeeds with "new-version" or
// returns the canned error (e.g. ErrConfigVersionMismatch) without mutation.
type stubSource struct {
	readBytes   []byte
	readVersion string
	writeErr    error
	writeCalls  int
}

func (s *stubSource) Load(ctx context.Context) (*domain.Config, error) {
	return nil, fmt.Errorf("not implemented")
}
func (s *stubSource) Read(ctx context.Context) ([]byte, string, error) {
	return s.readBytes, s.readVersion, nil
}
func (s *stubSource) Write(ctx context.Context, data []byte, expected string) (string, error) {
	s.writeCalls++
	if s.writeErr != nil {
		return "", s.writeErr
	}
	return "new-version", nil
}
func (s *stubSource) Describe() string { return "stub://" }
func (s *stubSource) Watch(ctx context.Context, _ time.Duration) (<-chan config.WatchEvent, error) {
	ch := make(chan config.WatchEvent)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

// newAdminTestRouter wires a minimal gin engine with PUT /api/_admin/config
// directly to the handler. It deliberately skips adminKeyAuth so the tests
// can exercise the gating logic without needing INSTANCEZ_ADMIN_KEY plumbing.
func newAdminTestRouter(h *AdminHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/api/_admin/config", h.handlePutConfig)
	r.GET("/api/_admin/config", h.handleGetConfig)
	return r
}

func TestPutConfigForbidWhenDisabled(t *testing.T) {
	h := &AdminHandler{
		cfg:           &domain.Config{},
		db:            &stubDB{},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		dashboardMode: DashboardDisabled,
	}
	r := newAdminTestRouter(h)

	body := bytes.NewReader([]byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodPut, "/api/_admin/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
	}
	if got["error"] != "dashboard_disabled" {
		t.Fatalf(`expected error="dashboard_disabled", got %v`, got["error"])
	}
}

func TestPutConfigForbidWhenReadonly(t *testing.T) {
	h := &AdminHandler{
		cfg:           &domain.Config{},
		db:            &stubDB{},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		dashboardMode: DashboardReadonly,
	}
	r := newAdminTestRouter(h)

	body := bytes.NewReader([]byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodPut, "/api/_admin/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
	}
	if got["error"] != "dashboard_readonly" {
		t.Fatalf(`expected error="dashboard_readonly", got %v`, got["error"])
	}
}

// TestPutConfigReadwriteWithoutSourceReturns501 covers the readwrite branch
// where no Source was wired (defensive guard). The handler must not panic
// and must surface a 501.
func TestPutConfigReadwriteWithoutSourceReturns501(t *testing.T) {
	h := &AdminHandler{
		cfg:           &domain.Config{},
		db:            &stubDB{},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		dashboardMode: DashboardReadwrite,
		// configSource intentionally nil
	}
	r := newAdminTestRouter(h)

	body := bytes.NewReader([]byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodPut, "/api/_admin/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
	}
}

// TestGetConfigUsesLiveConfig verifies handleGetConfig returns the engine's
// current running config (via configFn) rather than re-reading from a path.
func TestGetConfigUsesLiveConfig(t *testing.T) {
	live := &domain.Config{Version: 7}
	h := &AdminHandler{
		cfg:      &domain.Config{Version: 1}, // boot-time
		configFn: func() *domain.Config { return live },
		db:       &stubDB{},
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r := newAdminTestRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
	}
	// JSON numbers decode as float64.
	if v, _ := got["version"].(float64); v != 7 {
		t.Fatalf("expected live version=7, got %v", got["version"])
	}
	// _checksum should be omitted when no source is wired.
	if _, ok := got["_checksum"]; ok {
		t.Fatalf("did not expect _checksum without a configured source")
	}
}

// TestPutConfigConflictBodyOmitsChecksum asserts the If-Match conflict body
// returns only `current_version` (the source's version token) and does NOT
// leak a sha256 `current_checksum` — clients echo `current_version` back.
func TestPutConfigConflictBodyOmitsChecksum(t *testing.T) {
	src := &stubSource{readVersion: "v1"}
	h := &AdminHandler{
		cfg:           &domain.Config{},
		db:            &stubDB{},
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		dashboardMode: DashboardReadwrite,
		configSource:  src,
	}
	r := newAdminTestRouter(h)

	body := bytes.NewReader([]byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodPut, "/api/_admin/config", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", "stale-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
	}
	if got["error"] != "conflict" {
		t.Fatalf(`expected error="conflict", got %v`, got["error"])
	}
	if got["current_version"] != "v1" {
		t.Fatalf(`expected current_version="v1", got %v`, got["current_version"])
	}
	if _, ok := got["current_checksum"]; ok {
		t.Fatalf("did not expect current_checksum in conflict body, got %v", got["current_checksum"])
	}
}

// TestPutConfigSourceVersionMismatchReturns409 asserts that when the migrator
// commits successfully but configSource.Write returns ErrConfigVersionMismatch
// (a concurrent writer advanced the source between our Read and our Write),
// the handler returns 409 with `error=source_advanced_during_migration` and
// `db_migrated=true`, NOT a generic 500. This is the C1 contract: the
// dashboard needs to distinguish "source advanced concurrently" from
// "source write infrastructure failure" to drive its conflict UI.
func TestPutConfigSourceVersionMismatchReturns409(t *testing.T) {
	// Drive the migrator's Apply to its early-return path: we stub
	// GetLastMigration to return a record whose checksum matches the
	// marshaled config the handler will receive, so Apply returns nil
	// without touching Begin/Exec.
	parsedCfg := &domain.Config{Version: 1}
	cfgJSON, err := json.Marshal(parsedCfg)
	if err != nil {
		t.Fatalf("marshal cfg: %v", err)
	}
	matchingChecksum := fmt.Sprintf("%x", sha256.Sum256(cfgJSON))

	db := &stubDB{}
	// Override GetLastMigration via the (currently empty) hook surface. The
	// shared stubDB doesn't expose a hook for this, so we shadow it with a
	// local type that embeds stubDB and overrides only GetLastMigration.
	wrapped := &lastMigrationStubDB{stubDB: db, checksum: matchingChecksum}

	src := &stubSource{
		readVersion: "v1",
		writeErr:    config.ErrConfigVersionMismatch,
	}
	h := &AdminHandler{
		cfg:           parsedCfg,
		db:            wrapped,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		dashboardMode: DashboardReadwrite,
		configSource:  src,
	}
	r := newAdminTestRouter(h)

	// Body parses to a minimal config whose checksum matches the stubbed
	// last-migration record, so Apply returns nil quickly without exercising
	// the (unstubbed) tx Begin/Exec path.
	body := bytes.NewReader([]byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodPut, "/api/_admin/config", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
	}
	if got["error"] != "source_advanced_during_migration" {
		t.Fatalf(`expected error="source_advanced_during_migration", got %v`, got["error"])
	}
	if got["expected_version"] != "v1" {
		t.Fatalf(`expected expected_version="v1", got %v`, got["expected_version"])
	}
	if got["db_migrated"] != true {
		t.Fatalf(`expected db_migrated=true, got %v`, got["db_migrated"])
	}
	if src.writeCalls != 1 {
		t.Fatalf("expected exactly 1 Write call, got %d", src.writeCalls)
	}
}

// TestGetKeysReturnsStableAnonKey asserts GET /api/_admin/keys returns the
// publishable anon key and that it is identical across requests — the
// dashboard displays it as "the" project anon key, Supabase-style.
func TestGetKeysReturnsStableAnonKey(t *testing.T) {
	km, err := app.NewInMemoryJWTKeyManager("kid1", nil)
	if err != nil {
		t.Fatalf("key manager: %v", err)
	}
	h := &AdminHandler{
		cfg:     &domain.Config{},
		db:      &stubDB{},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		jwtKeys: km,
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/_admin/keys", h.handleKeys)

	fetch := func() string {
		req := httptest.NewRequest(http.MethodGet, "/api/_admin/keys", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
		}
		key, _ := got["anon_key"].(string)
		if key == "" {
			t.Fatalf("missing anon_key in %s", w.Body.String())
		}
		return key
	}

	first := fetch()
	second := fetch()
	if first != second {
		t.Fatalf("anon_key changed between requests:\n%s\n%s", first, second)
	}
}

// TestGetKeysWithoutKeyManagerReturns501 covers the defensive nil-JWTKeys
// branch (test wiring that never set deps.JWTKeys).
func TestGetKeysWithoutKeyManagerReturns501(t *testing.T) {
	h := &AdminHandler{
		cfg:    &domain.Config{},
		db:     &stubDB{},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/_admin/keys", h.handleKeys)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/keys", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
	}
}

// lastMigrationStubDB lets the C1 test stub GetLastMigration without
// touching the shared stubDB (which other suites depend on).
type lastMigrationStubDB struct {
	*stubDB
	checksum string
}

func (l *lastMigrationStubDB) GetLastMigration(ctx context.Context) (*domain.Migration, error) {
	return &domain.Migration{Checksum: l.checksum, ConfigJSON: "{}"}, nil
}

func TestHandleGetEnvVars(t *testing.T) {
	t.Setenv("INSTANCEZ_RESEND_API_KEY", "re_test")

	raw := []byte(`version: 1
project:
  name: test
providers:
  email:
    type: resend
    api_key: ${INSTANCEZ_RESEND_API_KEY}
auth:
  google:
    client_id: ${INSTANCEZ_GOOGLE_CLIENT_ID}
    client_secret: ${INSTANCEZ_GOOGLE_CLIENT_SECRET}
    redirect_url: ${INSTANCEZ_GOOGLE_REDIRECT_URL}
`)
	src := &stubSource{readBytes: raw, readVersion: "v1"}
	h := &AdminHandler{
		configSource:  src,
		dashboardMode: DashboardReadwrite,
		logger:        slog.Default(),
	}

	r := gin.New()
	r.GET("/config/env-vars", h.handleGetEnvVars)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/config/env-vars", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Vars map[string]struct{ Set bool } `json:"vars"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Vars["INSTANCEZ_RESEND_API_KEY"].Set {
		t.Error("expected INSTANCEZ_RESEND_API_KEY to be set")
	}
	if resp.Vars["INSTANCEZ_GOOGLE_CLIENT_ID"].Set {
		t.Error("expected INSTANCEZ_GOOGLE_CLIENT_ID to be unset")
	}
}
