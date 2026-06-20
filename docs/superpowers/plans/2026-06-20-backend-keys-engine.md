# Backend JWT key endpoints (engine) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add admin endpoints to the per-app engine that read the active JWT signing key (public material only) and rotate it.

**Architecture:** The engine already manages signing keys in `auth.jwt_keys` via `app.JWTKeyManager`, with `retired_at` modelling rotation. This plan adds a `RotateActive` method and a JWKS serializer to `JWTKeyManager`, then two `/api/_admin` handlers that wrap them. The anon key is derived from the active key, so rotation changes it as a side effect; no anon-key code changes.

**Tech Stack:** Go, gin, `github.com/golang-jwt/jwt`, Postgres (`domain.Database`).

**Companion spec:** `instancez-platform/main/docs/superpowers/specs/2026-06-20-backend-keys-expose-rotate-design.md`.

## Global Constraints

- Both endpoints sit on the `/api/_admin` group and are already covered by `adminKeyAuth()` (`internal/adapter/http/middleware.go:293`). Do not add per-route auth.
- The read endpoint MUST NOT return private key material (no PEM, no `d`/`p`/`q`).
- Go module path: `github.com/instancez/instancez`.
- Run tests from `instancez/main`.
- Comments and docstrings: keep the existing terse style; no em dashes.

---

### Task 1: JWKS serialization for a public key

**Files:**
- Modify: `internal/app/jwtkeys.go`
- Test: `internal/app/jwtkeys_jwks_test.go` (create)

**Interfaces:**
- Produces: `func (k *JWTKey) PublicJWK() (map[string]any, error)` returns a single JWK (`kty`, `n`, `e`, `kid`, `alg`, `use:"sig"`) for an RS256 key; error for keys without RSA public material.

- [ ] **Step 1: Write the failing test**

```go
package app

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
)

func TestPublicJWK_RS256(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	k := &JWTKey{KID: "kid1", Algorithm: "RS256", PrivateKey: priv, PublicKey: &priv.PublicKey}

	jwk, err := k.PublicJWK()
	if err != nil {
		t.Fatalf("PublicJWK: %v", err)
	}
	if jwk["kty"] != "RSA" || jwk["alg"] != "RS256" || jwk["use"] != "sig" {
		t.Fatalf("unexpected header fields: %v", jwk)
	}
	if jwk["kid"] != "kid1" {
		t.Fatalf("kid = %v", jwk["kid"])
	}
	if jwk["n"] == "" || jwk["e"] == "" {
		t.Fatalf("missing modulus/exponent: %v", jwk)
	}
	// Never leak private material.
	if _, bad := jwk["d"]; bad {
		t.Fatal("JWK leaked private exponent")
	}
}

func TestPublicJWK_NoPublicKey(t *testing.T) {
	k := &JWTKey{KID: "kid1", Algorithm: "HS256", Secret: []byte("x")}
	if _, err := k.PublicJWK(); err == nil {
		t.Fatal("expected error for key without RSA public material")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestPublicJWK -v`
Expected: FAIL with "k.PublicJWK undefined".

- [ ] **Step 3: Write minimal implementation**

Add to `internal/app/jwtkeys.go` (add `"encoding/base64"` and `"math/big"` to imports):

```go
// PublicJWK returns the public half of an RS256 key as a JWK map. It never
// includes private material, so it is safe to expose. Returns an error for keys
// that carry no RSA public key.
func (k *JWTKey) PublicJWK() (map[string]any, error) {
	if k == nil || k.PublicKey == nil {
		return nil, fmt.Errorf("jwt key: no RSA public key for kid %q", kidOf(k))
	}
	n := base64.RawURLEncoding.EncodeToString(k.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.PublicKey.E)).Bytes())
	return map[string]any{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": k.KID,
		"n":   n,
		"e":   e,
	}, nil
}

func kidOf(k *JWTKey) string {
	if k == nil {
		return ""
	}
	return k.KID
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestPublicJWK -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/jwtkeys.go internal/app/jwtkeys_jwks_test.go
git commit -m "feat(engine): add PublicJWK serializer for signing keys"
```

---

### Task 2: Rotate the active signing key

**Files:**
- Modify: `internal/app/jwtkeys.go`
- Test: `internal/app/jwtkeys_rotate_test.go` (create)

**Interfaces:**
- Consumes: the existing `generateRS256Key()` helper and `domain.Database` `Exec` on the manager's `db`.
- Produces: `func (m *JWTKeyManager) RotateActive(ctx context.Context) (*JWTKey, error)` retires every non-retired key, inserts a fresh RS256 key, makes it the in-memory active key, and returns it. Errors if the manager has no `db`.

- [ ] **Step 1: Write the failing test**

Reuse the `fakeDB` defined in `internal/app/migrate_test.go` (same package). The fake records `Exec` calls; assert that rotation retires then inserts, and that `Active` returns the new key afterward.

```go
package app

import (
	"context"
	"strings"
	"testing"
)

func TestRotateActive_RetiresThenInserts(t *testing.T) {
	db := &fakeDB{}
	m := NewJWTKeyManager(db)

	key, err := m.RotateActive(context.Background())
	if err != nil {
		t.Fatalf("RotateActive: %v", err)
	}
	if key == nil || key.KID == "" || key.PrivateKey == nil {
		t.Fatal("rotate returned no usable key")
	}

	// Two writes: a retire UPDATE, then an INSERT, in that order.
	if len(db.execs) != 2 {
		t.Fatalf("want 2 exec calls, got %d: %v", len(db.execs), db.execs)
	}
	if !strings.Contains(strings.ToUpper(db.execs[0]), "UPDATE") || !strings.Contains(db.execs[0], "retired_at") {
		t.Fatalf("first exec is not a retire UPDATE: %q", db.execs[0])
	}
	if !strings.Contains(strings.ToUpper(db.execs[1]), "INSERT") {
		t.Fatalf("second exec is not an INSERT: %q", db.execs[1])
	}

	// The new key is now active without re-reading the DB.
	got, err := m.Active(context.Background())
	if err != nil {
		t.Fatalf("Active after rotate: %v", err)
	}
	if got.KID != key.KID {
		t.Fatalf("active kid = %q, want %q", got.KID, key.KID)
	}
}

func TestRotateActive_NoDB(t *testing.T) {
	m := &JWTKeyManager{byKID: map[string]*JWTKey{}}
	if _, err := m.RotateActive(context.Background()); err == nil {
		t.Fatal("expected error when manager has no db")
	}
}
```

If `fakeDB` does not already capture `Exec` query strings in an `execs []string` field, add that field and append `query` in its `Exec` method (a one-line change in `migrate_test.go`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/ -run TestRotateActive -v`
Expected: FAIL with "m.RotateActive undefined".

- [ ] **Step 3: Write minimal implementation**

Add to `internal/app/jwtkeys.go`:

```go
// RotateActive generates a fresh RS256 signing key and makes it active. Every
// previously active key is marked retired (retired_at = now()) so tokens it
// signed still verify until they expire, while all new tokens use the new key.
// The derived anon key changes as a result, since it is minted from the active
// key. Requires a db-backed manager.
func (m *JWTKeyManager) RotateActive(ctx context.Context) (*JWTKey, error) {
	if m.db == nil {
		return nil, fmt.Errorf("jwt key: rotate requires a database-backed manager")
	}

	key, err := generateRS256Key()
	if err != nil {
		return nil, fmt.Errorf("jwt key: generate: %w", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key.PrivateKey),
	})

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.db.Exec(ctx,
		`UPDATE auth.jwt_keys SET retired_at = now() WHERE retired_at IS NULL`); err != nil {
		return nil, fmt.Errorf("jwt key: retire current: %w", err)
	}
	if _, err := m.db.Exec(ctx,
		`INSERT INTO auth.jwt_keys (kid, secret, algorithm, created_at) VALUES ($1, $2, $3, $4)`,
		key.KID, privPEM, key.Algorithm, key.CreatedAt); err != nil {
		return nil, fmt.Errorf("jwt key: insert new: %w", err)
	}

	m.active = key
	m.byKID[key.KID] = key
	return key, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/ -run TestRotateActive -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/jwtkeys.go internal/app/jwtkeys_rotate_test.go internal/app/migrate_test.go
git commit -m "feat(engine): add JWTKeyManager.RotateActive"
```

---

### Task 3: GET /api/_admin/jwt-keys handler

**Files:**
- Modify: `internal/adapter/http/admin_handler.go`
- Test: `internal/adapter/http/admin_jwtkeys_test.go` (create)

**Interfaces:**
- Consumes: `h.jwtKeys` (`*app.JWTKeyManager`), `JWTKey.PublicJWK()` from Task 1.
- Produces: `func (h *AdminHandler) handleJWTKey(c *gin.Context)` responding `200 {"kid","algorithm","jwks":{"keys":[<jwk>]}}`.

- [ ] **Step 1: Write the failing test**

Model wiring on the existing `handleKeys` test (`admin_handler_test.go:292`): build an in-memory key manager and route directly to the handler, skipping `adminKeyAuth`.

```go
package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if bytesContains(w.Body.Bytes(), "PRIVATE KEY") || bytesContains(w.Body.Bytes(), `"d"`) {
		t.Fatal("response leaked private key material")
	}
}

func bytesContains(b []byte, sub string) bool {
	return len(sub) == 0 || (len(b) >= len(sub) && stringIndex(string(b), sub) >= 0)
}
func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/http/ -run TestHandleJWTKey -v`
Expected: FAIL with "h.handleJWTKey undefined".

- [ ] **Step 3: Write minimal implementation**

Add to `internal/adapter/http/admin_handler.go`:

```go
// handleJWTKey returns the active signing key's public material (JWKS form) plus
// its kid and algorithm. Private key material is never included. Mirrors
// handleKeys' nil-manager guard.
func (h *AdminHandler) handleJWTKey(c *gin.Context) {
	if h.jwtKeys == nil {
		adminErr(c, 501, "not_implemented", "JWT key manager not configured")
		return
	}
	key, err := h.jwtKeys.Active(c.Request.Context())
	if err != nil {
		h.logger.Error("active jwt key", "error", err)
		adminErr(c, 500, "internal", "failed to load signing key")
		return
	}
	jwk, err := key.PublicJWK()
	if err != nil {
		adminErr(c, 500, "internal", "failed to serialize signing key")
		return
	}
	c.JSON(200, gin.H{
		"kid":       key.KID,
		"algorithm": key.Algorithm,
		"jwks":      gin.H{"keys": []any{jwk}},
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapter/http/ -run TestHandleJWTKey -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/http/admin_handler.go internal/adapter/http/admin_jwtkeys_test.go
git commit -m "feat(engine): add GET /api/_admin/jwt-keys"
```

---

### Task 4: POST /api/_admin/jwt-keys/rotate handler + route mount

**Files:**
- Modify: `internal/adapter/http/admin_handler.go`
- Test: `internal/adapter/http/admin_jwtkeys_test.go`

**Interfaces:**
- Consumes: `JWTKeyManager.RotateActive` (Task 2), `PublicJWK` (Task 1).
- Produces: `func (h *AdminHandler) handleRotateJWTKey(c *gin.Context)` responding `200 {"kid","algorithm","jwks":{...}}` for the new key. Routes mounted in `Mount`.

- [ ] **Step 1: Write the failing test**

A DB-backed manager is needed because rotate writes. Use the same `fakeDB` pattern; it lives in the `app` package's test file, so add a tiny local fake in this test file that satisfies `domain.Database` for the two `Exec` calls (return `(1, nil)` from `Exec`, and the existing `QueryRow` returning nil row so `Active` falls through to generate). Then assert rotate returns a new kid and only public material.

```go
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
	if bytesContains(w.Body.Bytes(), "PRIVATE KEY") {
		t.Fatal("rotate leaked private key material")
	}
}

// rotateFakeDB is a minimal domain.Database: Active() finds no existing key
// (QueryRow -> nil) and rotate's two Exec calls succeed.
type rotateFakeDB struct{}

func (rotateFakeDB) Query(ctx context.Context, q string, a ...any) ([]map[string]any, error) {
	return nil, nil
}
func (rotateFakeDB) QueryRow(ctx context.Context, q string, a ...any) (map[string]any, error) {
	return nil, nil
}
func (rotateFakeDB) Exec(ctx context.Context, q string, a ...any) (int64, error) { return 1, nil }
func (rotateFakeDB) ExecDDL(ctx context.Context, sql string) error               { return nil }
```

Add imports `"context"`, `"log/slog"` to the test file. If `domain.Database` has more methods than these, copy their signatures from `internal/domain/database.go:86` and return zero values.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/http/ -run TestHandleRotateJWTKey -v`
Expected: FAIL with "h.handleRotateJWTKey undefined".

- [ ] **Step 3: Write minimal implementation**

Add the handler to `internal/adapter/http/admin_handler.go`:

```go
// handleRotateJWTKey generates a new active signing key and retires the prior
// one. Tokens signed before rotation still verify until they expire; new tokens
// and the derived anon key use the new key. Returns the new public key.
func (h *AdminHandler) handleRotateJWTKey(c *gin.Context) {
	if h.jwtKeys == nil {
		adminErr(c, 501, "not_implemented", "JWT key manager not configured")
		return
	}
	key, err := h.jwtKeys.RotateActive(c.Request.Context())
	if err != nil {
		h.logger.Error("rotate jwt key", "error", err)
		adminErr(c, 500, "internal", "failed to rotate signing key")
		return
	}
	jwk, err := key.PublicJWK()
	if err != nil {
		adminErr(c, 500, "internal", "failed to serialize signing key")
		return
	}
	c.JSON(200, gin.H{
		"kid":       key.KID,
		"algorithm": key.Algorithm,
		"jwks":      gin.H{"keys": []any{jwk}},
	})
}
```

Mount both routes in `Mount` next to the existing `admin.GET("/keys", h.handleKeys)` (around `admin_handler.go:119`):

```go
	admin.GET("/jwt-keys", h.handleJWTKey)
	admin.POST("/jwt-keys/rotate", h.handleRotateJWTKey)
```

- [ ] **Step 4: Run the full package tests**

Run: `go test ./internal/adapter/http/ ./internal/app/ -v`
Expected: PASS, including the new tests.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/http/admin_handler.go internal/adapter/http/admin_jwtkeys_test.go
git commit -m "feat(engine): add POST /api/_admin/jwt-keys/rotate and mount key routes"
```

---

## Self-Review

- **Spec coverage:** Engine read endpoint (Task 3), engine rotate endpoint (Task 4), retire-not-delete semantics (Task 2), public-only output (Tasks 1, 3, 4). The anon key changing on rotation is a documented consequence, no code change. Covered.
- **Placeholder scan:** none.
- **Type consistency:** `PublicJWK`, `RotateActive`, `handleJWTKey`, `handleRotateJWTKey` are used with identical names across tasks.
