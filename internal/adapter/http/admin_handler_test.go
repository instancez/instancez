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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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

// TestGetKeysReturnsConfiguredKeys asserts GET /api/_admin/keys returns the
// publishable and secret keys from the environment — the values the dashboard
// presents for copying, Supabase-style.
func TestGetKeysReturnsConfiguredKeys(t *testing.T) {
	t.Setenv("INSTANCEZ_PUBLISHABLE_KEY", "inz_publishable_testpub")
	t.Setenv("INSTANCEZ_SECRET_KEY", "inz_secret_testsecret")
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
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v (raw %s)", err, w.Body.String())
	}
	if got["publishable_key"] != "inz_publishable_testpub" {
		t.Errorf("publishable_key = %v", got["publishable_key"])
	}
	if got["secret_key"] != "inz_secret_testsecret" {
		t.Errorf("secret_key = %v", got["secret_key"])
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
		Vars map[string]struct {
			Set  bool   `json:"set"`
			Tail string `json:"tail"`
		} `json:"vars"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Vars["INSTANCEZ_RESEND_API_KEY"].Set {
		t.Error("expected INSTANCEZ_RESEND_API_KEY to be set")
	}
	// Set vars carry a last-4 tail; the plaintext is never returned.
	if got := resp.Vars["INSTANCEZ_RESEND_API_KEY"].Tail; got != "test" {
		t.Errorf("tail = %q, want %q (last 4 of re_test)", got, "test")
	}
	if resp.Vars["INSTANCEZ_GOOGLE_CLIENT_ID"].Set {
		t.Error("expected INSTANCEZ_GOOGLE_CLIENT_ID to be unset")
	}
	if got := resp.Vars["INSTANCEZ_GOOGLE_CLIENT_ID"].Tail; got != "" {
		t.Errorf("unset var must have no tail, got %q", got)
	}
}

func TestTailOf(t *testing.T) {
	cases := map[string]string{
		"re_test": "test", // longer than 4 → last 4
		"abcd":    "abcd", // exactly 4 → whole
		"ab":      "ab",   // shorter than 4 → whole
		"":        "",
	}
	for in, want := range cases {
		if got := tailOf(in); got != want {
			t.Errorf("tailOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHandlePreviewConfig covers the save-preview endpoint: it must return
// the raw current source and the YAML that PUT would write, without writing
// or migrating anything.
func TestHandlePreviewConfig(t *testing.T) {
	raw := []byte("version: 1\nproject:\n  name: old\n")
	src := &stubSource{readBytes: raw, readVersion: "v1"}
	h := &AdminHandler{
		dashboardMode: DashboardReadwrite,
		configSource:  src,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r := gin.New()
	r.POST("/config/preview", h.handlePreviewConfig)

	body := bytes.NewReader([]byte(`{"version":1,"project":{"name":"new"}}`))
	req := httptest.NewRequest(http.MethodPost, "/config/preview", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Current  string `json:"current"`
		Proposed string `json:"proposed"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Current != string(raw) {
		t.Errorf("current = %q, want raw source %q", resp.Current, raw)
	}
	if !strings.Contains(resp.Proposed, "name: new") {
		t.Errorf("proposed YAML missing updated project name: %q", resp.Proposed)
	}
	if src.writeCalls != 0 {
		t.Errorf("preview must not write; got %d Write calls", src.writeCalls)
	}
}

func TestHandlePreviewConfig_ValidationErrors(t *testing.T) {
	src := &stubSource{readBytes: []byte("version: 1\n"), readVersion: "v1"}
	h := &AdminHandler{
		dashboardMode: DashboardReadwrite,
		configSource:  src,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r := gin.New()
	r.POST("/config/preview", h.handlePreviewConfig)

	body := bytes.NewReader([]byte(`{"version":99}`))
	req := httptest.NewRequest(http.MethodPost, "/config/preview", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "errors") {
		t.Errorf("expected errors list in body, got %s", w.Body.String())
	}
	if src.writeCalls != 0 {
		t.Errorf("preview must not write; got %d Write calls", src.writeCalls)
	}
}

func TestHandlePreviewConfig_ReadonlyForbidden(t *testing.T) {
	h := &AdminHandler{
		dashboardMode: DashboardReadonly,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r := gin.New()
	r.POST("/config/preview", h.handlePreviewConfig)

	body := bytes.NewReader([]byte(`{"version":1}`))
	req := httptest.NewRequest(http.MethodPost, "/config/preview", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
}

// TestHandleFunctionFileExists covers the file-existence probe the dashboard
// uses before concluding a save that renames a code function's file.
func TestHandleFunctionFileExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "functions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "functions", "foo.js"), []byte("// x"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := &AdminHandler{
		configPath: filepath.Join(dir, "instancez.yaml"),
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	r := gin.New()
	r.GET("/functions/file-exists", h.handleFunctionFileExists)

	get := func(file string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/functions/file-exists?file="+url.QueryEscape(file), nil)
		r.ServeHTTP(w, req)
		return w
	}

	if w := get("functions/foo.js"); w.Code != 200 || !strings.Contains(w.Body.String(), `"exists":true`) {
		t.Errorf("existing file: code=%d body=%s", w.Code, w.Body.String())
	}
	if w := get("functions/missing.js"); w.Code != 200 || !strings.Contains(w.Body.String(), `"exists":false`) {
		t.Errorf("missing file: code=%d body=%s", w.Code, w.Body.String())
	}
	if w := get("../../etc/passwd"); w.Code != 400 {
		t.Errorf("path escape should be rejected: code=%d body=%s", w.Code, w.Body.String())
	}
	if w := get(""); w.Code != 400 {
		t.Errorf("empty file should be rejected: code=%d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleGetEnvVars_RequestedNames covers vars the dashboard asks about
// explicitly via ?names=…: a set env var must report set:true even when the
// raw config has no ${VAR} reference for it yet (e.g. a provider toggled on
// in the dashboard but not saved).
func TestHandleGetEnvVars_RequestedNames(t *testing.T) {
	t.Setenv("INSTANCEZ_S3_BUCKET", "my-bucket")

	raw := []byte(`version: 1
project:
  name: test
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
	req := httptest.NewRequest("GET", "/config/env-vars?names=INSTANCEZ_S3_BUCKET,AWS_REGION,not%20a%20valid%24name", nil)
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
	if v, ok := resp.Vars["INSTANCEZ_S3_BUCKET"]; !ok || !v.Set {
		t.Errorf("expected INSTANCEZ_S3_BUCKET to be reported set, got %+v (present=%v)", v, ok)
	}
	if v, ok := resp.Vars["AWS_REGION"]; !ok || v.Set {
		t.Errorf("expected AWS_REGION to be reported unset, got %+v (present=%v)", v, ok)
	}
	if _, ok := resp.Vars["not a valid$name"]; ok {
		t.Error("expected invalid env var name to be ignored")
	}
}

func TestHandlePutDotenv_Disabled(t *testing.T) {
	h := &AdminHandler{dotenvWritable: false, logger: slog.Default()}
	r := gin.New()
	r.PUT("/config/dotenv", h.handlePutDotenv)

	body := `{"INSTANCEZ_RESEND_API_KEY": "re_test"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/config/dotenv", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestHandlePutDotenv_WritesFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, ".env")

	h := &AdminHandler{dotenvWritable: true, dotenvPath: path, logger: slog.Default()}
	r := gin.New()
	r.PUT("/config/dotenv", h.handlePutDotenv)

	body := `{"INSTANCEZ_RESEND_API_KEY": "re_test_key"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/config/dotenv", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dotenv: %v", err)
	}
	if !strings.Contains(string(data), "INSTANCEZ_RESEND_API_KEY=re_test_key") {
		t.Errorf("dotenv file missing expected line, got: %s", string(data))
	}
}

// --- handleListMigrations ---

func TestHandleListMigrations_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
		return nil, nil
	}}
	h := &AdminHandler{db: db, cfg: &domain.Config{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/migrations", h.handleListMigrations)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/migrations", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body []any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("expected empty list, got %v", body)
	}
}

func TestHandleListMigrations_Populated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
		return []map[string]any{
			{"id": 2, "checksum": "abc", "applied_at": "2024-01-02T00:00:00Z"},
			{"id": 1, "checksum": "def", "applied_at": "2024-01-01T00:00:00Z"},
		}, nil
	}}
	h := &AdminHandler{db: db, cfg: &domain.Config{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/migrations", h.handleListMigrations)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/migrations", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 {
		t.Fatalf("expected 2 migrations, got %d: %v", len(body), body)
	}
}

func TestHandleListMigrations_QueryError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
		return nil, fmt.Errorf("connection refused")
	}}
	h := &AdminHandler{db: db, cfg: &domain.Config{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/migrations", h.handleListMigrations)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/migrations", nil)
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

// --- handleListUsers ---

func TestHandleListUsers_Empty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
		return nil, nil
	}}
	h := &AdminHandler{db: db, cfg: &domain.Config{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/users", h.handleListUsers)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/users", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body []any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("expected empty list, got %v", body)
	}
}

func TestHandleListUsers_Populated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{queryFn: func(ctx context.Context, q string, args ...any) ([]map[string]any, error) {
		return []map[string]any{
			{"id": "u1", "email": "a@example.com", "email_verified": true, "created_at": "2024-01-01T00:00:00Z"},
		}, nil
	}}
	h := &AdminHandler{db: db, cfg: &domain.Config{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/users", h.handleListUsers)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/users", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body[0]["email"] != "a@example.com" {
		t.Fatalf("unexpected body: %v", body)
	}
}

// --- handleDisableUser ---

func TestHandleDisableUser_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotUserID string
	db := &stubDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
		if len(args) > 0 {
			gotUserID, _ = args[0].(string)
		}
		return 1, nil
	}}
	h := &AdminHandler{db: db, cfg: &domain.Config{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/api/_admin/users/:id/disable", h.handleDisableUser)

	req := httptest.NewRequest(http.MethodPost, "/api/_admin/users/u1/disable", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotUserID != "u1" {
		t.Errorf("expected refresh tokens revoked for user_id=u1, got %q", gotUserID)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["user_id"] != "u1" {
		t.Errorf("response user_id = %v", body["user_id"])
	}
}

func TestHandleDisableUser_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
		return 0, fmt.Errorf("connection refused")
	}}
	h := &AdminHandler{db: db, cfg: &domain.Config{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/api/_admin/users/:id/disable", h.handleDisableUser)

	req := httptest.NewRequest(http.MethodPost, "/api/_admin/users/u1/disable", nil)
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

// --- handleAdminResetPassword ---

func TestHandleAdminResetPassword_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) { return 1, nil }}
	h := &AdminHandler{db: db, cfg: &domain.Config{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/api/_admin/users/:id/reset-password", h.handleAdminResetPassword)

	req := httptest.NewRequest(http.MethodPost, "/api/_admin/users/u1/reset-password", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if tok, _ := body["token"].(string); tok == "" {
		t.Error("expected a non-empty reset token")
	}
}

func TestHandleAdminResetPassword_DBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := &stubDB{execFn: func(ctx context.Context, q string, args ...any) (int64, error) {
		return 0, fmt.Errorf("connection refused")
	}}
	h := &AdminHandler{db: db, cfg: &domain.Config{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/api/_admin/users/:id/reset-password", h.handleAdminResetPassword)

	req := httptest.NewRequest(http.MethodPost, "/api/_admin/users/u1/reset-password", nil)
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

// --- handleSchema ---

func TestHandleSchema_ReflectsLiveConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "myproj"},
		Tables: map[string]domain.Table{
			"todos": {Fields: []domain.Field{{Name: "id", Type: "bigserial", PrimaryKey: true}}},
		},
		Storage: map[string]domain.Bucket{"avatars": {Public: true}},
	}
	h := &AdminHandler{cfg: cfg}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/schema", h.handleSchema)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/schema", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["version"] != float64(1) {
		t.Errorf("version = %v", body["version"])
	}
	tables, _ := body["tables"].(map[string]any)
	if _, ok := tables["todos"]; !ok {
		t.Errorf("expected 'todos' table in schema, got %v", tables)
	}
	storage, _ := body["storage"].(map[string]any)
	if _, ok := storage["avatars"]; !ok {
		t.Errorf("expected 'avatars' bucket in schema, got %v", storage)
	}
}

// --- handleStats ---

func TestHandleStats_ReturnsTableAndStorageCounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &domain.Config{
		Tables:  map[string]domain.Table{"todos": {}},
		Storage: map[string]domain.Bucket{"avatars": {}},
	}
	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		if strings.Contains(q, "pg_class") {
			return map[string]any{"count": int64(42)}, nil
		}
		return map[string]any{"object_count": 3, "total_bytes": int64(1024)}, nil
	}}
	h := &AdminHandler{db: db, cfg: cfg}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/stats", h.handleStats)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/stats", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	tables, _ := body["tables"].(map[string]any)
	todos, _ := tables["todos"].(map[string]any)
	if todos["row_count"] != float64(42) {
		t.Errorf("todos row_count = %v", todos["row_count"])
	}
	storage, _ := body["storage"].(map[string]any)
	avatars, _ := storage["avatars"].(map[string]any)
	if avatars["object_count"] != float64(3) {
		t.Errorf("avatars object_count = %v", avatars["object_count"])
	}
}

func TestHandleStats_QueryErrorFallsBackToZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &domain.Config{Tables: map[string]domain.Table{"todos": {}}}
	db := &stubDB{queryRowFn: func(ctx context.Context, q string, args ...any) (map[string]any, error) {
		return nil, fmt.Errorf("relation does not exist")
	}}
	h := &AdminHandler{db: db, cfg: cfg}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/stats", h.handleStats)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/stats", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	tables, _ := body["tables"].(map[string]any)
	todos, _ := tables["todos"].(map[string]any)
	if todos["row_count"] != float64(0) {
		t.Errorf("expected row_count=0 fallback on query error, got %v", todos["row_count"])
	}
}

// --- handleConfigDiff ---
//
// handleConfigDiff always plans a from-scratch migration against h.cfg (it
// passes oldCfg=nil to migrator.Plan, not a value read from the live
// database), so the DB is never queried here — the diff is purely a function
// of h.cfg's tables/storage.

func TestHandleConfigDiff_NoTablesIsNotDestructive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AdminHandler{cfg: &domain.Config{}, db: &stubDB{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/config/diff", h.handleConfigDiff)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/config/diff", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// Even a table-less config plans the baseline schema/grants/auth.jwt_keys
	// DDL, so "no tables" doesn't mean "no statements" — the only thing this
	// pins is that a from-scratch plan is never flagged destructive. Regression
	// guard for containsDestructive matching "DELETE " inside the baseline
	// "GRANT ... DELETE ON TABLES ..." privilege grant, which used to make
	// every fresh deploy report is_destructive=true.
	if body["is_destructive"] != false {
		t.Errorf("expected is_destructive=false, got %v", body["is_destructive"])
	}
}

func TestContainsDestructive(t *testing.T) {
	tests := []struct {
		name string
		stmt string
		want bool
	}{
		{"drop table", "DROP TABLE IF EXISTS todos CASCADE;", true},
		{"drop column via alter", "ALTER TABLE todos DROP COLUMN IF EXISTS notes;", true},
		{"truncate", "TRUNCATE todos;", true},
		{"lowercase drop", "drop table todos;", true},
		{"create table", "CREATE TABLE IF NOT EXISTS todos (id bigserial primary key);", false},
		// Regression: "DELETE" as a privilege name in a GRANT statement must
		// not be treated as a destructive DML DELETE — no migration plan ever
		// emits an actual "DELETE FROM" statement.
		{"grant with delete privilege", "GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO anon;", false},
		{"alter default privileges with delete", "ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO anon;", false},
		{"foreign key on delete cascade", "user_id UUID REFERENCES auth.users(id) ON DELETE CASCADE", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsDestructive(tc.stmt); got != tc.want {
				t.Errorf("containsDestructive(%q) = %v, want %v", tc.stmt, got, tc.want)
			}
		})
	}
}

func TestHandleConfigDiff_TableProducesStatements(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &domain.Config{
		Tables: map[string]domain.Table{
			"todos": {
				Fields: []domain.Field{
					{Name: "id", Type: "bigserial", PrimaryKey: true},
					{Name: "title", Type: "text"},
				},
			},
		},
	}
	h := &AdminHandler{cfg: cfg, db: &stubDB{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/config/diff", h.handleConfigDiff)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/config/diff", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	statements, _ := body["statements"].([]any)
	if len(statements) == 0 {
		t.Fatal("expected CREATE TABLE statements for a table-bearing config")
	}
}

// --- handleGetFunctionDeps / handlePostFunctionDeps ---

func TestHandleGetFunctionDeps_NoConfigPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AdminHandler{cfg: &domain.Config{}}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/functions/deps", h.handleGetFunctionDeps)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/functions/deps", nil)
	r.ServeHTTP(w, req)

	if w.Code != 501 {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleGetFunctionDeps_NoPackageJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	h := &AdminHandler{cfg: &domain.Config{}, configPath: filepath.Join(dir, "instancez.yaml"), dashboardMode: DashboardReadonly}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/functions/deps", h.handleGetFunctionDeps)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/functions/deps", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	deps, _ := body["dependencies"].(map[string]any)
	if len(deps) != 0 {
		t.Errorf("expected no dependencies, got %v", deps)
	}
	if body["has_lock"] != false {
		t.Errorf("expected has_lock=false, got %v", body["has_lock"])
	}
	if body["readonly"] != true {
		t.Errorf("expected readonly=true for DashboardReadonly mode, got %v", body["readonly"])
	}
}

func TestHandleGetFunctionDeps_WithPackageJSONAndLock(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	functionsDir := filepath.Join(dir, "functions")
	if err := os.MkdirAll(functionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := `{"name":"functions","dependencies":{"lodash":"^4.17.21"}}`
	if err := os.WriteFile(filepath.Join(functionsDir, "package.json"), []byte(pkg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(functionsDir, "package-lock.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &AdminHandler{cfg: &domain.Config{}, configPath: filepath.Join(dir, "instancez.yaml"), dashboardMode: DashboardReadwrite}

	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/api/_admin/functions/deps", h.handleGetFunctionDeps)

	req := httptest.NewRequest(http.MethodGet, "/api/_admin/functions/deps", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	deps, _ := body["dependencies"].(map[string]any)
	if deps["lodash"] != "^4.17.21" {
		t.Errorf("expected lodash dependency, got %v", deps)
	}
	if body["has_lock"] != true {
		t.Errorf("expected has_lock=true, got %v", body["has_lock"])
	}
	if body["readonly"] != false {
		t.Errorf("expected readonly=false for DashboardReadwrite mode, got %v", body["readonly"])
	}
}

func TestHandlePostFunctionDeps_ForbiddenWhenReadonly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AdminHandler{cfg: &domain.Config{}, dashboardMode: DashboardReadonly}

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/api/_admin/functions/deps", h.handlePostFunctionDeps)

	req := httptest.NewRequest(http.MethodPost, "/api/_admin/functions/deps", strings.NewReader(`{"add":["lodash"]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePostFunctionDeps_EmptyBodyRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	h := &AdminHandler{
		cfg:           &domain.Config{},
		dashboardMode: DashboardReadwrite,
		configPath:    filepath.Join(dir, "instancez.yaml"),
	}

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/api/_admin/functions/deps", h.handlePostFunctionDeps)

	req := httptest.NewRequest(http.MethodPost, "/api/_admin/functions/deps", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePostFunctionDeps_NoConfigPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AdminHandler{cfg: &domain.Config{}, dashboardMode: DashboardReadwrite}

	w := httptest.NewRecorder()
	r := gin.New()
	r.POST("/api/_admin/functions/deps", h.handlePostFunctionDeps)

	req := httptest.NewRequest(http.MethodPost, "/api/_admin/functions/deps", strings.NewReader(`{"add":["lodash"]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 501 {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminErrShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	adminErr(c, 403, "dashboard_readonly", "Requires readwrite dashboard mode.")

	if w.Code != 403 {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "dashboard_readonly" || body["message"] != "Requires readwrite dashboard mode." {
		t.Errorf("body = %#v", body)
	}
	if _, hasStatusCode := body["statusCode"]; hasStatusCode {
		t.Error("admin shape must not include statusCode")
	}
}
