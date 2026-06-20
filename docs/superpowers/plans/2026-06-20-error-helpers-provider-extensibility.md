# Error-envelope helpers and provider extensibility — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove inline error-envelope duplication in the storage/admin handlers and make storage, email, and OAuth providers pluggable, without changing any wire format.

**Architecture:** Three error shapes each get one constructor (`storageErr`, `adminErr`; PostgREST's `problemJSON` already exists). Storage/email providers move behind factory registries; OAuth moves behind an `OAuthProvider` interface + registry with map-based config (`auth.oauth.<name>`), replacing the scattered `switch provider` sites. The OAuth config change is a clean break and propagates to the sibling `instancez-platform` repo.

**Tech Stack:** Go 1.25, Gin, pgx, cobra; `go test -race`; integration suite via testcontainers + `@supabase/supabase-js` Node harness.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-06-20-error-helpers-provider-extensibility-design.md`.
- Wire-compat with `@supabase/supabase-js` and `@supabase/storage-js` is load-bearing. Storage error shape is exactly `{"statusCode": "<int-as-string>", "error": "<slug>", "message": "<msg>"}`. No status code, slug, or message text changes anywhere in this plan.
- Feedback loop must pass before each commit: `go build ./...`, `go test -race ./...` (unit), and `go test -tags=integration -race ./internal/adapter/http/...` for HTTP/OAuth changes (needs Docker + node).
- TDD: failing test first, watch it fail, minimal implementation, watch it pass.
- Branch policy: stay on `main`, do NOT create branches. The working tree already has unrelated dirty files (dashboard/*); every `git add` must list only the files named in that task. Never `git add -A` / `git add .`. Commit messages have NO co-author trailer.
- `gin.H` is `map[string]any`. `strconv` must be imported where `storageErr` lands (it already is in `storage_v1_handler.go`).

---

### Task 1: `storageErr` helper

**Files:**
- Modify: `internal/adapter/http/storage_v1_handler.go` (add helper near `uploadWriteError:357`; convert 54 inline `{statusCode,error,message}` literals + the 3 branches of `uploadWriteError`)
- Test: `internal/adapter/http/storage_v1_handler_test.go`

**Interfaces:**
- Produces: `func storageErr(c *gin.Context, status int, errSlug, message string)` (unexported, same package). Body: `c.JSON(status, gin.H{"statusCode": strconv.Itoa(status), "error": errSlug, "message": message})`.

- [ ] **Step 1: Write the failing test**

Add to `storage_v1_handler_test.go`:

```go
func TestStorageErrShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	storageErr(c, 404, "not_found", `Bucket "x" not found`)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	// statusCode MUST be the string "404" (storage-js contract), not a number.
	if body["statusCode"] != "404" {
		t.Errorf("statusCode = %#v, want string \"404\"", body["statusCode"])
	}
	if body["error"] != "not_found" {
		t.Errorf("error = %#v", body["error"])
	}
	if body["message"] != `Bucket "x" not found` {
		t.Errorf("message = %#v", body["message"])
	}
}
```

Ensure imports include `encoding/json`, `net/http/httptest`, `github.com/gin-gonic/gin` (most already present in the test file — check before adding).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/http/ -run TestStorageErrShape`
Expected: FAIL — `undefined: storageErr`.

- [ ] **Step 3: Add the helper**

In `storage_v1_handler.go`, immediately above `uploadWriteError`:

```go
// storageErr writes a storage-js compatible error body: {statusCode, error, message}.
// statusCode is the HTTP status rendered as a string, matching @supabase/storage-js.
func storageErr(c *gin.Context, status int, errSlug, message string) {
	c.JSON(status, gin.H{
		"statusCode": strconv.Itoa(status),
		"error":      errSlug,
		"message":    message,
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapter/http/ -run TestStorageErrShape`
Expected: PASS.

- [ ] **Step 5: Convert the inline literals**

Replace every `c.JSON(<status>, gin.H{"statusCode": "<n>", "error": "<slug>", "message": <expr>})` in `storage_v1_handler.go` with `storageErr(c, <status>, "<slug>", <expr>)`. The `<n>` string and `<status>` int are always the same number; keep `<slug>` and `<expr>` byte-identical. Rewrite `uploadWriteError`'s three branches the same way, e.g.:

```go
func (h *StorageV1Handler) uploadWriteError(c *gin.Context, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "duplicate key") || strings.Contains(msg, "23505"):
		storageErr(c, 409, "duplicate", "The resource already exists")
	case strings.Contains(msg, "row-level security") || strings.Contains(msg, "42501") || strings.Contains(msg, "permission denied"):
		storageErr(c, 403, "forbidden", "Not authorized to write this object")
	default:
		h.logger.Error("record object", "error", err)
		storageErr(c, 500, "internal", "Failed to record object")
	}
}
```

Representative call sites to convert (not exhaustive — convert all): `:113, :127, :131, :135, :141, :149, :207, :230, :251, :270, :363, :365, :368, :379`.

- [ ] **Step 6: Verify no inline storage literals remain**

Run: `grep -nE 'gin\.H\{[^}]*"statusCode"' internal/adapter/http/storage_v1_handler.go`
Expected: no matches (all now go through `storageErr`).

- [ ] **Step 7: Run the storage tests + build**

Run: `go build ./... && go test -race ./internal/adapter/http/ -run 'Storage'`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/adapter/http/storage_v1_handler.go internal/adapter/http/storage_v1_handler_test.go
git commit -m "refactor(http): route storage errors through storageErr helper"
```

---

### Task 2: `adminErr` helper

**Files:**
- Modify: `internal/adapter/http/admin_handler.go` (add helper; convert the 18 `{error,message}` literals; leave `{errors:[...]}` and `{...,detail}` variants inline)
- Test: `internal/adapter/http/admin_handler_test.go`

**Interfaces:**
- Produces: `func adminErr(c *gin.Context, status int, errSlug, message string)` (unexported, same package). Body: `c.JSON(status, gin.H{"error": errSlug, "message": message})`.

- [ ] **Step 1: Write the failing test**

Add to `admin_handler_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/http/ -run TestAdminErrShape`
Expected: FAIL — `undefined: adminErr`.

- [ ] **Step 3: Add the helper**

In `admin_handler.go`, near the top (package-level, like `problemJSON` lives in `middleware.go`):

```go
// adminErr writes the internal /_admin error shape: {error, message}.
// This surface is consumed by the dashboard, not by supabase-js.
func adminErr(c *gin.Context, status int, errSlug, message string) {
	c.JSON(status, gin.H{"error": errSlug, "message": message})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/adapter/http/ -run TestAdminErrShape`
Expected: PASS.

- [ ] **Step 5: Convert exact `{error,message}` literals only**

Replace each `c.JSON(<status>, gin.H{"error": "<slug>", "message": <expr>})` with `adminErr(c, <status>, "<slug>", <expr>)`. Representative sites: `:130, :136, :170, :186, :279, :286, :311, :365, :454, :461, :612, :720, :808`. **Do NOT convert** these (different shape — leave verbatim):
- `:333, :494` — `c.JSON(400, gin.H{"errors": errList})`
- `:766, :776` — `c.JSON(500, gin.H{"error": "npm_error", "message": ..., "detail": string(out)})` (has a third key)

Open each candidate and confirm its literal is exactly two keys before converting.

- [ ] **Step 6: Build + admin tests**

Run: `go build ./... && go test -race ./internal/adapter/http/ -run 'Admin'`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/http/admin_handler.go internal/adapter/http/admin_handler_test.go
git commit -m "refactor(http): route admin errors through adminErr helper"
```

---

### Task 3: storage/email provider registry

**Files:**
- Modify: `internal/cli/providers.go` (replace switches with registries + `registerBuiltins()`)
- Test: `internal/cli/providers_test.go`

**Interfaces:**
- Consumes: `domain.ObjectStore`, `domain.EmailSender`, `domain.StorageProvider`, `domain.EmailProvider`; existing `newS3Store(ctx, *domain.StorageProvider)`, `NewLocalStore(path, prefix)`, `resend.New(apiKey)`.
- Produces: `registerStorage(name string, f storageFactory)`, `registerEmail(name string, f emailFactory)`, `registerBuiltins()` (idempotent), and the unchanged exported `initEmailProvider`/`initStorageProvider`/`initProviders` signatures.

- [ ] **Step 1: Write the failing test**

Add to `providers_test.go`:

```go
func TestStorageRegistryBuiltins(t *testing.T) {
	registerBuiltins()
	for _, name := range []string{"s3", "local"} {
		if _, ok := storageRegistry[name]; !ok {
			t.Errorf("storage factory %q not registered", name)
		}
	}
	if _, ok := emailRegistry["resend"]; !ok {
		t.Error("email factory \"resend\" not registered")
	}
}

func TestUnsupportedStorageProviderError(t *testing.T) {
	registerBuiltins()
	cfg := &domain.Config{Providers: domain.Providers{Storage: &domain.StorageProvider{Type: "gdrive"}}}
	_, err := initStorageProvider(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "gdrive") {
		t.Fatalf("err = %v, want mention of gdrive", err)
	}
	if !strings.Contains(err.Error(), "s3") || !strings.Contains(err.Error(), "local") {
		t.Errorf("error should list supported providers, got %q", err.Error())
	}
}
```

Add imports `context`, `strings` if missing.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run 'Registry|UnsupportedStorage'`
Expected: FAIL — `undefined: registerBuiltins` / `storageRegistry`.

- [ ] **Step 3: Implement the registries**

Rewrite `providers.go` keeping all existing helpers (`newS3Store`, `checkStorageHealth`, `initProviders`) intact. Add:

```go
type storageFactory func(ctx context.Context, p *domain.StorageProvider) (domain.ObjectStore, error)
type emailFactory func(p *domain.EmailProvider) (domain.EmailSender, error)

var (
	storageRegistry = map[string]storageFactory{}
	emailRegistry   = map[string]emailFactory{}
)

func registerStorage(name string, f storageFactory) { storageRegistry[name] = f }
func registerEmail(name string, f emailFactory)     { emailRegistry[name] = f }

// registerBuiltins registers the providers shipped in the box. Idempotent so
// tests can call it freely. New built-in providers add one line here; external
// builds can call registerStorage/registerEmail before initProviders.
func registerBuiltins() {
	registerStorage("s3", func(ctx context.Context, p *domain.StorageProvider) (domain.ObjectStore, error) {
		return newS3Store(ctx, p)
	})
	registerStorage("local", func(_ context.Context, p *domain.StorageProvider) (domain.ObjectStore, error) {
		path := p.Path
		if path == "" {
			path = "./uploads"
		}
		return NewLocalStore(path, os.Getenv("INSTANCEZ_STORAGE_KEY_PREFIX"))
	})
	registerEmail("resend", func(p *domain.EmailProvider) (domain.EmailSender, error) {
		if p.APIKey == "" {
			return nil, fmt.Errorf("INSTANCEZ_RESEND_API_KEY not set (required for resend provider)")
		}
		return resend.New(p.APIKey), nil
	})
}

func sortedKeys[T any](m map[string]T) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
```

Rewrite the two init funcs to dispatch via registry:

```go
func initEmailProvider(cfg *domain.Config) (domain.EmailSender, error) {
	if cfg.Providers.Email == nil || cfg.Providers.Email.Type == "" {
		return nil, nil
	}
	registerBuiltins()
	f, ok := emailRegistry[cfg.Providers.Email.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported email provider: %s (supported: %s)", cfg.Providers.Email.Type, sortedKeys(emailRegistry))
	}
	return f(cfg.Providers.Email)
}

func initStorageProvider(ctx context.Context, cfg *domain.Config) (domain.ObjectStore, error) {
	if cfg.Providers.Storage == nil || cfg.Providers.Storage.Type == "" {
		return nil, nil
	}
	registerBuiltins()
	f, ok := storageRegistry[cfg.Providers.Storage.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported storage provider: %s (supported: %s)", cfg.Providers.Storage.Type, sortedKeys(storageRegistry))
	}
	return f(ctx, cfg.Providers.Storage)
}
```

Add `sort` to the import block. `registerBuiltins` is idempotent (map overwrite), so calling it from both init funcs and tests is safe.

- [ ] **Step 4: Run tests + build**

Run: `go build ./... && go test -race ./internal/cli/ -run 'Provider|Registry|Storage|Email'`
Expected: PASS (existing provider tests included).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/providers.go internal/cli/providers_test.go
git commit -m "refactor(cli): select storage/email providers via a registry"
```

---

### Task 4: OAuth provider interface + map config (clean break)

This task is one buildable unit: the config struct change breaks every consumer's compile, so the interface, the impls, and all consumer rewrites land together. The cross-repo (platform) doc/UI updates are Task 5.

**Files:**
- Create: `internal/adapter/auth/oauthprovider.go` (interface + registry + google/github impls)
- Create: `internal/adapter/auth/oauthprovider_test.go`
- Modify: `internal/adapter/auth/oauth.go` (move bodies into impls; delete the provider switch)
- Modify: `internal/domain/schema.go` (`Google`/`GitHub` → `OAuth map[string]*OAuthProvider`)
- Modify: `internal/config/validate.go:314-326` (loop the map)
- Modify: `internal/adapter/http/auth_handler.go` (sites `:116-121, :143-148, :425-436, :1142-1148, :1175-1213, :1254-1279, :1941-1971`)
- Modify: `instancez.yaml` (auth example), `internal/cli/init.go` (scaffolding), `docs/site/src/content/docs/build/auth.md`
- Test: `internal/config/validate_test.go`, `internal/adapter/http/auth_handler_test.go` (adjust config construction to the map)

**Interfaces:**
- Produces (package `auth`):
  - `type OAuthProvider interface { Name() string; AuthorizeURL(cfg *domain.OAuthProvider, state string) string; ExchangeCode(cfg *domain.OAuthProvider, code string) (string, error); FetchUser(accessToken string) (*OAuthUserInfo, error) }`
  - `func RegisterOAuth(p OAuthProvider)`, `func OAuthRegistry(name string) (OAuthProvider, bool)`
  - existing `OAuthUserInfo` stays exported; package-level `google`/`github` instances self-register via `registerOAuthBuiltins()` called from `OAuthRegistry`/`RegisterOAuth` setup (idempotent).
- Produces (package `domain`): `AuthConfig.OAuth map[string]*OAuthProvider`.

- [ ] **Step 1: Write the failing provider test**

Create `internal/adapter/auth/oauthprovider_test.go`:

```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestOAuthRegistryBuiltins(t *testing.T) {
	for _, name := range []string{"google", "github"} {
		if _, ok := OAuthRegistry(name); !ok {
			t.Errorf("provider %q not registered", name)
		}
	}
	if _, ok := OAuthRegistry("nope"); ok {
		t.Error("unexpected provider \"nope\"")
	}
}

func TestGoogleAuthorizeURL(t *testing.T) {
	p, _ := OAuthRegistry("google")
	cfg := &domain.OAuthProvider{ClientID: "cid", RedirectURL: "https://app/cb"}
	got := p.AuthorizeURL(cfg, "st8")
	for _, want := range []string{"accounts.google.com", "client_id=cid", "state=st8", "scope=openid+email+profile"} {
		if !strings.Contains(got, want) {
			t.Errorf("authorize url missing %q: %s", want, got)
		}
	}
}

func TestGithubFetchUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"email":"a@b.co","name":"A","login":"a"}`))
	}))
	defer srv.Close()
	gh := &githubProvider{userAPI: srv.URL}
	u, err := gh.FetchUser("tok")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "a@b.co" || u.ProviderID != "42" {
		t.Errorf("user = %#v", u)
	}
}
```

(The `userAPI` field makes the GitHub endpoint injectable for the test; default it to the real URL.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/auth/ -run OAuth`
Expected: FAIL — `undefined: OAuthRegistry` / `githubProvider`.

- [ ] **Step 3: Implement `oauthprovider.go`**

```go
package auth

import (
	"fmt"
	"net/url"
	"sync"

	"github.com/instancez/instancez/internal/domain"
)

// OAuthProvider is the seam for an external identity provider. A new provider is
// one implementation of this interface plus a RegisterOAuth call — no handler or
// config-struct edits.
type OAuthProvider interface {
	Name() string
	AuthorizeURL(cfg *domain.OAuthProvider, state string) string
	ExchangeCode(cfg *domain.OAuthProvider, code string) (accessToken string, err error)
	FetchUser(accessToken string) (*OAuthUserInfo, error)
}

var (
	oauthOnce     sync.Once
	oauthRegistry = map[string]OAuthProvider{}
)

func registerOAuthBuiltins() {
	oauthOnce.Do(func() {
		RegisterOAuth(&googleProvider{})
		RegisterOAuth(&githubProvider{userAPI: "https://api.github.com/user", emailAPI: "https://api.github.com/user/emails"})
	})
}

// RegisterOAuth adds a provider to the registry, keyed by Name().
func RegisterOAuth(p OAuthProvider) { oauthRegistry[p.Name()] = p }

// OAuthRegistry returns the provider registered under name.
func OAuthRegistry(name string) (OAuthProvider, bool) {
	registerOAuthBuiltins()
	p, ok := oauthRegistry[name]
	return p, ok
}

// ---- google ----

type googleProvider struct{}

func (googleProvider) Name() string { return "google" }

func (googleProvider) AuthorizeURL(cfg *domain.OAuthProvider, state string) string {
	return fmt.Sprintf(
		"https://accounts.google.com/o/oauth2/v2/auth?client_id=%s&redirect_uri=%s&response_type=code&scope=openid+email+profile&state=%s",
		cfg.ClientID, url.QueryEscape(cfg.RedirectURL), state)
}

func (googleProvider) ExchangeCode(cfg *domain.OAuthProvider, code string) (string, error) {
	return exchangeOAuthCode("https://oauth2.googleapis.com/token", cfg, code)
}

func (googleProvider) FetchUser(accessToken string) (*OAuthUserInfo, error) {
	return fetchGoogleUser(accessToken)
}

// ---- github ----

type githubProvider struct {
	userAPI  string
	emailAPI string
}

func (githubProvider) Name() string { return "github" }

func (githubProvider) AuthorizeURL(cfg *domain.OAuthProvider, state string) string {
	return fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=user:email&state=%s",
		cfg.ClientID, url.QueryEscape(cfg.RedirectURL), state)
}

func (githubProvider) ExchangeCode(cfg *domain.OAuthProvider, code string) (string, error) {
	return exchangeOAuthCode("https://github.com/login/oauth/access_token", cfg, code)
}

func (g githubProvider) FetchUser(accessToken string) (*OAuthUserInfo, error) {
	return fetchGitHubUser(g, accessToken)
}
```

- [ ] **Step 4: Move the existing bodies into `oauth.go`**

In `oauth.go`: delete `ExchangeCode`'s `switch provider` and rename the generic body to `exchangeOAuthCode(tokenURL string, cfg *domain.OAuthProvider, code string) (string, error)` (same request/parse logic, no switch). Rename `FetchGoogleUser`→`fetchGoogleUser` (unexported; identical body) and `FetchGitHubUser`→`fetchGitHubUser(g githubProvider, accessToken string)` using `g.userAPI` / `g.emailAPI` instead of the hardcoded URLs; `FetchGitHubPrimaryEmail` becomes a method/helper using `g.emailAPI`. Keep `OAuthUserInfo` exported. Remove now-dead exported symbols only after Step 6 confirms no other caller.

- [ ] **Step 5: Flip the config struct + validation**

`internal/domain/schema.go`: replace the two fields with:

```go
OAuth map[string]*OAuthProvider `yaml:"oauth" json:"oauth"`
```

`internal/config/validate.go` (was `:314-326`):

```go
for name, p := range auth.OAuth {
	if p == nil {
		continue
	}
	if p.ClientID == "" {
		errs = append(errs, ... fmt.Sprintf("auth.oauth.%s.client_id is required", name))
	}
	if p.ClientSecret == "" {
		errs = append(errs, ... fmt.Sprintf("auth.oauth.%s.client_secret is required", name))
	}
}
```

(Match the surrounding error-append style in `validate.go` — read the two existing blocks first and mirror them exactly.)

- [ ] **Step 6: Rewrite the auth_handler consumers**

Apply these edits in `auth_handler.go`:

Route registration (`:116-121`) — loop configured+registered providers:

```go
for name := range h.cfg.Auth.OAuth {
	if _, ok := adapterauth.OAuthRegistry(name); ok {
		auth.GET("/callback/"+name, h.handleOAuthCallback(name))
	}
}
```

Settings (`:1142-1148`):

```go
providers := gin.H{}
for name, p := range h.cfg.Auth.OAuth {
	if p != nil {
		providers[name] = true
	}
}
```

`handleAuthorize` (`:1175-1213`) and `handleLinkIdentity` (`:1941-1971`) — replace both the config switch and the URL switch:

```go
cfg := h.cfg.Auth.OAuth[provider]
prov, ok := adapterauth.OAuthRegistry(provider)
if cfg == nil || !ok {
	problemJSON(c, 400, "bad_request", "Unsupported or unconfigured provider: "+provider)
	return
}
// ... (state creation unchanged) ...
authURL := prov.AuthorizeURL(cfg, state)
```

`handleOAuthCallback` (`:1254-1279`):

```go
providerCfg := h.cfg.Auth.OAuth[provider]
prov, ok := adapterauth.OAuthRegistry(provider)
if providerCfg == nil || !ok {
	problemJSON(c, 400, "bad_request", "Unconfigured provider: "+provider)
	return
}
oauthToken, err := prov.ExchangeCode(providerCfg, code)
// ... unchanged error handling ...
userInfo, err := prov.FetchUser(oauthToken)
```

id-token path (`:425-436`) — keep google-only but read from the map:

```go
g := h.cfg.Auth.OAuth["google"]
if req.Provider != "google" || g == nil {
	problemJSON(c, 400, "bad_request", "Unsupported provider for ID token: "+req.Provider)
	return
}
clientID = g.ClientID
```

`buildExternalProviders`-style spots at `:143-148` (the `if h.cfg.Auth.Google != nil` pairs) likewise become a loop over `h.cfg.Auth.OAuth`.

- [ ] **Step 7: Update examples/scaffolding/docs (instancez repo)**

- `instancez.yaml`: replace `google: null` / `github: null` under `auth:` with:
  ```yaml
      oauth: {}
      # oauth:
      #   google:
      #     client_id: ${GOOGLE_CLIENT_ID}
      #     client_secret: ${GOOGLE_CLIENT_SECRET}
      #     redirect_url: https://myapp.example.com/auth/callback/google
  ```
- `internal/cli/init.go`: same shape wherever it emits `google`/`github`.
- `docs/site/src/content/docs/build/auth.md:30-39`: rewrite the `google:`/`github:` block under `auth.oauth.<name>`.

- [ ] **Step 8: Fix the tests that build AuthConfig**

In `auth_handler_test.go` and `validate_test.go`, change `Auth{Google: ...}` / `Auth{GitHub: ...}` constructions to `Auth{OAuth: map[string]*domain.OAuthProvider{"google": ...}}`. Run `grep -rn 'Auth.Google\|Auth.GitHub\|Google:\|GitHub:' internal --include='*_test.go'` to find them all.

- [ ] **Step 9: Build + unit tests**

Run: `go build ./... && go test -race ./internal/...`
Expected: PASS. Then `grep -n 'switch provider\|Auth.Google\|Auth.GitHub' internal/adapter/http/auth_handler.go internal/adapter/auth/oauth.go` → no matches.

- [ ] **Step 10: Integration / contract gate**

Run: `go test -run TestSupabaseJSCompat -tags=integration -race ./internal/adapter/http/...`
Expected: PASS (needs Docker + node). This proves the OAuth flow + storage errors still satisfy supabase-js.

- [ ] **Step 11: Commit**

```bash
git add internal/adapter/auth/oauthprovider.go internal/adapter/auth/oauthprovider_test.go \
  internal/adapter/auth/oauth.go internal/domain/schema.go internal/config/validate.go \
  internal/config/validate_test.go internal/adapter/http/auth_handler.go \
  internal/adapter/http/auth_handler_test.go instancez.yaml internal/cli/init.go \
  docs/site/src/content/docs/build/auth.md
git commit -m "feat(auth): pluggable OAuth providers via interface + map config"
```

---

### Task 5: propagate the config break to instancez-platform

**Files (in `../instancez-platform/main`):**
- Modify: `ai/prompts/tools/instancez-yaml.md:121-129`
- Modify: `data/pkg/server/mcp_handler.go:803, :977-985`
- Modify: `web/src/components/blocks/ai/ai-code-and-preview/config-tab.tsx:425-426`
- Sweep: `ai/prompts/generate-yaml.txt`, `data/pkg/server/configdiff_test.go`

**Interfaces:** none (docs/prompts/UI). No Go interface coupling to the instancez module.

- [ ] **Step 1: Find every old-shape reference**

Run: `cd ../instancez-platform/main && grep -rn 'auth.google\|auth.github\|^\s*google:\|^\s*github:' ai/ data/ web/src --include='*.md' --include='*.txt' --include='*.go' --include='*.tsx'`
Record the hits; each becomes an edit below.

- [ ] **Step 2: Update the LLM tool description + examples**

In `ai/prompts/tools/instancez-yaml.md` and `data/pkg/server/mcp_handler.go`, rewrite the `auth:` example so providers nest under `oauth:`:

```yaml
auth:
  oauth:
    google:
      client_id: "${GOOGLE_CLIENT_ID}"
      client_secret: "${GOOGLE_CLIENT_SECRET}"
      redirect_url: "https://app.example.com/auth/callback/google"
    github:
      client_id: "${GITHUB_CLIENT_ID}"
      client_secret: "${GITHUB_CLIENT_SECRET}"
      redirect_url: "https://app.example.com/auth/callback/github"
```

Update the prose at `mcp_handler.go:803` from "email/google/github" to "email and oauth providers (auth.oauth.<name>)".

- [ ] **Step 3: Update the config-tab UI**

`config-tab.tsx:425-426`: replace

```ts
if (auth.google) providers.push('google')
if (auth.github) providers.push('github')
```

with

```ts
if (auth.oauth) providers.push(...Object.keys(auth.oauth))
```

Check the `auth` TS type nearby; if it declares `google?`/`github?`, change it to `oauth?: Record<string, unknown>`.

- [ ] **Step 4: Sweep generate-yaml.txt + configdiff_test.go**

Apply the same `auth.oauth.<name>` rename to any old-shape occurrence found in Step 1.

- [ ] **Step 5: Build/test platform**

Run (from `../instancez-platform/main`): the repo's Go build + `go test ./data/...`, and `npm --prefix web run build` (or the repo's documented web check).
Expected: PASS. Confirm `configdiff_test.go` is green with the new shape.

- [ ] **Step 6: Commit (platform repo)**

```bash
cd ../instancez-platform/main
git add ai/prompts/tools/instancez-yaml.md data/pkg/server/mcp_handler.go \
  web/src/components/blocks/ai/ai-code-and-preview/config-tab.tsx \
  ai/prompts/generate-yaml.txt data/pkg/server/configdiff_test.go
git commit -m "chore: move instancez auth config to auth.oauth.<name>"
```

---

## Self-Review

**Spec coverage:** Part 1 error helpers → Tasks 1–2. Part 2 OAuth interface + map config → Task 4. Part 3 storage/email registry → Task 3. Cross-repo → Tasks 4 (instancez) + 5 (platform). Testing section → tests in every task + the integration gate in Task 4 Step 10. All covered.

**Placeholder scan:** No TBD/TODO. The 54/18 mechanical conversions are described as a pattern with representative line numbers (per the many-files rule) plus exact before/after for the non-obvious cases (`uploadWriteError`, the admin variants to skip). New code (registries, OAuth interface) is shown in full.

**Type consistency:** `storageErr`/`adminErr` signatures identical across spec and tasks. `OAuthProvider` interface methods match between `oauthprovider.go`, the test, and the handler call sites (`AuthorizeURL(cfg, state)`, `ExchangeCode(cfg, code)`, `FetchUser(token)`). `AuthConfig.OAuth map[string]*domain.OAuthProvider` used consistently in schema, validate, handler, and tests. Registry accessors `OAuthRegistry`/`RegisterOAuth` and `storageRegistry`/`emailRegistry` named consistently.

## Notes for the executor

- Run the feedback loop between tasks, not just at the end.
- Each `git add` lists explicit files — the tree has unrelated dirty files; never stage them.
- Task 4 is the only contract-critical one; do not skip its Step 10 integration run.
