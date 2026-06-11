# Provider Secrets Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make provider and auth credentials flow exclusively through environment variables — the YAML stores `${VAR}` refs, `GET /config` never returns secret values, and the dashboard shows status badges with optional `.env` file writing in dev mode.

**Architecture:** Secrets stay in the env; the YAML stores explicit `${VAR}` references that the config loader resolves at runtime. A new `ParseBytesRaw` function lets the dashboard API return the unresolved YAML form. Two new admin endpoints (`GET /config/env-vars` for status, `PUT /config/dotenv` for writing `.development.env`) complete the loop. The dashboard replaces OAuth text inputs and provider env-var badges with live-status var widgets.

**Tech Stack:** Go 1.22+, Gin, pflag, gopkg.in/yaml.v3, React + TypeScript, Vitest

---

## File Map

| File | Action | What changes |
|------|--------|-------------|
| `internal/domain/schema.go` | Modify | `EmailProvider` + `StorageProvider` struct fields |
| `dashboard/src/lib/types.ts` | Modify | `EmailProviderConfig`, `StorageProviderConfig` interfaces |
| `internal/config/loader.go` | Modify | Add `ParseBytesRaw` |
| `internal/config/loader_test.go` | Modify | Tests for `ParseBytesRaw` |
| `internal/adapter/http/admin_handler.go` | Modify | `handleGetConfig` (raw), `handlePutConfig` (re-resolve), new `handleGetEnvVars`, `handlePutDotenv`, `dotenvWritable`/`dotenvPath` fields, mount new routes |
| `internal/adapter/http/server.go` | Modify | `ServerDeps.DotenvWritable`, `ServerDeps.DotenvPath` |
| `internal/cli/flags.go` | Modify | `serveOptions`/`serveFlagSet`/`devFlagSet` + `--dashboard-write-dotenv` / `--dotenv-path` flags |
| `internal/cli/serve.go` | Modify | Pass new flags to `ServerDeps` |
| `internal/cli/providers.go` | Modify | Read credentials from struct fields instead of `os.Getenv` |
| `internal/cli/providers_test.go` | Modify | Tests for new struct-driven init |
| `internal/config/validate.go` | Modify | Validate `api_key` for email, `bucket` for S3 |
| `internal/config/validate_test.go` | Modify | Tests for new validation rules |
| `dashboard/src/api/client.ts` | Modify | Add `getEnvVars`, `putDotenv` |
| `dashboard/src/pages/Providers.tsx` | Modify | Status badges, `default_from_email` field, S3 explicit-creds toggle |
| `dashboard/src/pages/Providers.test.tsx` | Modify | Tests for new UI |
| `dashboard/src/pages/Auth.tsx` | Modify | Replace OAuth text inputs with var-status badges |
| `dashboard/src/pages/Auth.test.tsx` | Modify | Tests for new UI |

---

## Task 1: Domain struct + TypeScript types

**Files:**
- Modify: `internal/domain/schema.go`
- Modify: `dashboard/src/lib/types.ts`

- [ ] **Step 1.1: Write the failing Go build check**

Run: `cd /path/to/main && go build ./...`  
Expected: succeeds (baseline before changes).

- [ ] **Step 1.2: Update `EmailProvider` in `internal/domain/schema.go`**

Find the existing `EmailProvider` struct (currently lines ~104-107) and replace it:

```go
type EmailProvider struct {
	Type             string `yaml:"type" json:"type"`
	APIKey           string `yaml:"api_key" json:"api_key"`
	DefaultFromEmail string `yaml:"default_from_email" json:"default_from_email"`
}
```

- [ ] **Step 1.3: Update `StorageProvider` in `internal/domain/schema.go`**

Find the existing `StorageProvider` struct (currently lines ~109-111) and replace it:

```go
type StorageProvider struct {
	Type            string `yaml:"type" json:"type"`
	Bucket          string `yaml:"bucket" json:"bucket"`
	Region          string `yaml:"region" json:"region"`
	AccessKeyID     string `yaml:"access_key_id" json:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key" json:"secret_access_key"`
	Endpoint        string `yaml:"endpoint" json:"endpoint"` // MinIO endpoint
	Credentials     string `yaml:"credentials" json:"credentials"` // GCS credentials
	Path            string `yaml:"path" json:"path"` // local storage directory
}
```

- [ ] **Step 1.4: Verify build still passes**

Run: `go build ./...`  
Expected: PASS (no references to old struct shape elsewhere).

- [ ] **Step 1.5: Update TypeScript `Providers` and related types in `dashboard/src/lib/types.ts`**

Replace the existing `Providers` interface and add two new interfaces:

```typescript
export interface EmailProviderConfig {
  type: string;
  api_key: string;
  default_from_email: string;
}

export interface StorageProviderConfig {
  type: string;
  bucket: string;
  region: string;
  access_key_id: string;
  secret_access_key: string;
  endpoint: string;
  credentials: string;
  path: string;
}

export interface Providers {
  email: EmailProviderConfig | null;
  storage: StorageProviderConfig | null;
}
```

- [ ] **Step 1.6: Run dashboard type check**

Run: `cd dashboard && npm test -- --run`  
Expected: PASS (tests that reference `Providers` compile with the new shape).

- [ ] **Step 1.7: Commit**

```bash
git add internal/domain/schema.go dashboard/src/lib/types.ts
git commit -m "feat: expand EmailProvider and StorageProvider struct fields"
```

---

## Task 2: ParseBytesRaw + raw GET /config + PUT /config re-resolve fix

**Files:**
- Modify: `internal/config/loader.go`
- Modify: `internal/config/loader_test.go`
- Modify: `internal/adapter/http/admin_handler.go`

- [ ] **Step 2.1: Write failing test for `ParseBytesRaw` in `internal/config/loader_test.go`**

```go
func TestParseBytesRaw_PreservesEnvRefs(t *testing.T) {
	t.Setenv("TEST_API_KEY", "actual_secret")

	yaml := `
version: 1
project:
  name: test
providers:
  email:
    type: resend
    api_key: ${TEST_API_KEY}
`
	cfg, err := ParseBytesRaw([]byte(yaml), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Providers.Email == nil {
		t.Fatal("expected email provider, got nil")
	}
	if cfg.Providers.Email.APIKey != "${TEST_API_KEY}" {
		t.Errorf("api_key = %q, want %q", cfg.Providers.Email.APIKey, "${TEST_API_KEY}")
	}
}

func TestParseBytesRaw_PreservesDefaultSyntax(t *testing.T) {
	yaml := `
version: 1
project:
  name: test
providers:
  email:
    type: resend
    api_key: ${TEST_KEY:-fallback}
`
	cfg, err := ParseBytesRaw([]byte(yaml), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Providers.Email.APIKey != "${TEST_KEY:-fallback}" {
		t.Errorf("api_key = %q, want literal ref string", cfg.Providers.Email.APIKey)
	}
}
```

- [ ] **Step 2.2: Run to confirm failure**

Run: `go test ./internal/config/... -run TestParseBytesRaw`  
Expected: FAIL — `ParseBytesRaw undefined`.

- [ ] **Step 2.3: Add `ParseBytesRaw` to `internal/config/loader.go`**

Add after the existing `ParseBytesLenient` function:

```go
// ParseBytesRaw parses YAML without any env var interpolation — ${VAR} and
// ${VAR:-default} references are preserved as-is. Used by GET /config so
// secret values never transit the dashboard API layer.
func ParseBytesRaw(data []byte, origin string) (*domain.Config, error) {
	var cfg domain.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, &domain.ConfigError{Path: origin, Message: "invalid YAML", Err: err}
	}
	applyDefaults(&cfg)
	return &cfg, nil
}
```

- [ ] **Step 2.4: Run tests to confirm pass**

Run: `go test ./internal/config/... -run TestParseBytesRaw`  
Expected: PASS.

- [ ] **Step 2.5: Update `handleGetConfig` in `internal/adapter/http/admin_handler.go`**

Replace the body of `handleGetConfig` (currently ~lines 204-226) with:

```go
func (h *AdminHandler) handleGetConfig(c *gin.Context) {
	// Read raw bytes from source so ${VAR} refs are preserved — secret values
	// must never transit the dashboard API.
	var result map[string]any
	if h.configSource != nil {
		raw, ver, err := h.configSource.Read(c.Request.Context())
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to read config source: "+err.Error())
			return
		}
		cfg, err := config.ParseBytesRaw(raw, h.sourceDescribe())
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to parse config: "+err.Error())
			return
		}
		jsonData, err := json.Marshal(cfg)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to serialize config")
			return
		}
		if err := json.Unmarshal(jsonData, &result); err != nil {
			problemJSON(c, 500, "internal", "Failed to round-trip config")
			return
		}
		result["_checksum"] = ver
	} else {
		// Test path: no source wired, fall back to live config (already resolved).
		cfg := h.liveConfig()
		jsonData, err := json.Marshal(cfg)
		if err != nil {
			problemJSON(c, 500, "internal", "Failed to serialize config")
			return
		}
		if err := json.Unmarshal(jsonData, &result); err != nil {
			problemJSON(c, 500, "internal", "Failed to round-trip config")
			return
		}
	}
	c.JSON(200, result)
}
```

- [ ] **Step 2.6: Fix `handlePutConfig` — re-resolve after write for `updateConfigFn`**

Find the block near the end of `handlePutConfig` that calls `h.updateConfigFn(&newCfg)` (currently ~line 350-354) and replace it:

```go
// Re-parse the written YAML with env var resolution so the live runtime
// config reflects the change immediately. ParseBytesLenient is used so that
// schema changes take effect even when provider env vars aren't set yet;
// unresolved vars become "placeholder" (harmless for schema-only fields).
if h.updateConfigFn != nil {
	if resolved, err := config.ParseBytesLenient(yamlData, h.sourceDescribe()); err == nil {
		h.updateConfigFn(resolved)
	}
}
```

- [ ] **Step 2.7: Run Go unit tests**

Run: `go test -race ./internal/config/... ./internal/adapter/http/...`  
Expected: PASS.

- [ ] **Step 2.8: Commit**

```bash
git add internal/config/loader.go internal/config/loader_test.go internal/adapter/http/admin_handler.go
git commit -m "feat: GET /config returns raw unresolved config; ParseBytesRaw"
```

---

## Task 3: GET /config/env-vars endpoint

**Files:**
- Modify: `internal/adapter/http/admin_handler.go`

- [ ] **Step 3.1: Write failing test**

In `internal/adapter/http/admin_handler_test.go` (or create it if it doesn't exist — check with `ls internal/adapter/http/*test*`), add:

```go
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
	src := &fakeSource{data: raw, ver: "v1"}
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
```

> Note: look at the existing test file to find the `fakeSource` helper — if it doesn't exist, define it as:
> ```go
> type fakeSource struct{ data []byte; ver string }
> func (f *fakeSource) Read(context.Context) ([]byte, string, error) { return f.data, f.ver, nil }
> func (f *fakeSource) Write(_ context.Context, d []byte, _ string) (string, error) { f.data = d; return f.ver, nil }
> func (f *fakeSource) Describe() string { return "fake" }
> ```

- [ ] **Step 3.2: Run to confirm failure**

Run: `go test ./internal/adapter/http/... -run TestHandleGetEnvVars`  
Expected: FAIL — `handleGetEnvVars undefined`.

- [ ] **Step 3.3: Add `handleGetEnvVars` to `admin_handler.go`**

```go
// handleGetEnvVars returns which ${VAR} references in the current raw config
// source are set vs missing in the server process. Values are never returned.
func (h *AdminHandler) handleGetEnvVars(c *gin.Context) {
	if h.configSource == nil {
		c.JSON(200, gin.H{"vars": gin.H{}})
		return
	}
	raw, _, err := h.configSource.Read(c.Request.Context())
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to read config source: "+err.Error())
		return
	}
	names := config.EnvRefs(raw)
	vars := make(map[string]any, len(names))
	for _, name := range names {
		_, set := os.LookupEnv(name)
		vars[name] = gin.H{"set": set}
	}
	c.JSON(200, gin.H{"vars": vars})
}
```

- [ ] **Step 3.4: Mount the new route in `AdminHandler.Mount`**

In the `Mount` method, after `admin.GET("/config/diff", h.handleConfigDiff)`, add:

```go
admin.GET("/config/env-vars", h.handleGetEnvVars)
```

- [ ] **Step 3.5: Run tests**

Run: `go test -race ./internal/adapter/http/... -run TestHandleGetEnvVars`  
Expected: PASS.

- [ ] **Step 3.6: Commit**

```bash
git add internal/adapter/http/admin_handler.go
git commit -m "feat: GET /config/env-vars endpoint for provider credential status"
```

---

## Task 4: `--dashboard-write-dotenv` flag + `PUT /config/dotenv` endpoint

**Files:**
- Modify: `internal/cli/flags.go`
- Modify: `internal/cli/serve.go`
- Modify: `internal/adapter/http/server.go`
- Modify: `internal/adapter/http/admin_handler.go`

- [ ] **Step 4.1: Write failing flag test in `internal/cli/serve_test.go`**

```go
func TestParseDotenvFlag_ServeDefault(t *testing.T) {
	opts, err := parseServeFlags([]string{}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.dotenvWritable {
		t.Error("expected dotenvWritable=false by default for serve")
	}
}

func TestParseDotenvFlag_ServeExplicit(t *testing.T) {
	opts, err := parseServeFlags(
		[]string{"--dashboard-write-dotenv", "--dotenv-path", "/etc/app.env"},
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.dotenvWritable {
		t.Error("expected dotenvWritable=true")
	}
	if opts.dotenvPath != "/etc/app.env" {
		t.Errorf("dotenvPath = %q, want /etc/app.env", opts.dotenvPath)
	}
}

func TestParseDotenvFlag_DevDefault(t *testing.T) {
	opts, err := parseDevFlags([]string{}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.dotenvWritable {
		t.Error("expected dotenvWritable=true by default for dev")
	}
	if opts.dotenvPath != ".development.env" {
		t.Errorf("dotenvPath = %q, want .development.env", opts.dotenvPath)
	}
}
```

- [ ] **Step 4.2: Run to confirm failure**

Run: `go test ./internal/cli/... -run TestParseDotenvFlag`  
Expected: FAIL — `dotenvWritable` undefined on `serveOptions`.

- [ ] **Step 4.3: Add fields to `serveOptions` and flag sets in `internal/cli/flags.go`**

Add to `serveOptions` struct:
```go
dotenvWritable bool
dotenvPath     string
```

Add to `serveFlagSet` struct:
```go
dotenvWritable bool
dotenvPath     string
```

In `newServeFlagSet()`, add:
```go
fs.flags.BoolVar(&fs.dotenvWritable, "dashboard-write-dotenv", false, "allow dashboard to write secrets to a .env file (env: INSTANCEZ_DASHBOARD_WRITE_DOTENV)")
fs.flags.StringVar(&fs.dotenvPath, "dotenv-path", "", "path to .env file when --dashboard-write-dotenv is set (env: INSTANCEZ_DOTENV_PATH)")
```

In `resolveServeFlags()`, add after dashboard mode resolution:
```go
if fs.dotenvWritable && fs.dotenvPath == "" {
	return serveOptions{}, fmt.Errorf("--dotenv-path is required when --dashboard-write-dotenv is set")
}
```

Add to `serveOptions` return value:
```go
dotenvWritable: fs.dotenvWritable,
dotenvPath:     fs.dotenvPath,
```

Add to `devFlagSet` struct:
```go
dotenvWritable bool
dotenvPath     string
```

In `newDevFlagSet()`, add:
```go
fs.flags.BoolVar(&fs.dotenvWritable, "dashboard-write-dotenv", true, "allow dashboard to write secrets to .development.env")
fs.flags.StringVar(&fs.dotenvPath, "dotenv-path", ".development.env", "path to .env file for dashboard secret writing")
```

Add to `devOptions` return value in `resolveDevFlags`:
```go
serveOptions: serveOptions{
    // ... existing fields ...
    dotenvWritable: fs.dotenvWritable,
    dotenvPath:     fs.dotenvPath,
},
```

- [ ] **Step 4.4: Run flag tests**

Run: `go test ./internal/cli/... -run TestParseDotenvFlag`  
Expected: PASS.

- [ ] **Step 4.5: Add `DotenvWritable` + `DotenvPath` to `ServerDeps` in `internal/adapter/http/server.go`**

In the `ServerDeps` struct, add:
```go
DotenvWritable bool   // true → PUT /config/dotenv is enabled
DotenvPath     string // path to the .env file to write
```

- [ ] **Step 4.6: Add fields and mount route to `AdminHandler` in `admin_handler.go`**

Add to `AdminHandler` struct:
```go
dotenvWritable bool
dotenvPath     string
```

In `NewAdminHandler`:
```go
dotenvWritable: deps.DotenvWritable,
dotenvPath:     deps.DotenvPath,
```

In `Mount`, after the env-vars route:
```go
admin.PUT("/config/dotenv", h.handlePutDotenv)
```

Update `handleConfigStatus` to include `dotenv_writable`:
```go
"dotenv_writable": h.dotenvWritable,
```
(add to all three `c.JSON(200, ...)` calls in that function)

- [ ] **Step 4.7: Implement `handlePutDotenv` in `admin_handler.go`**

```go
// handlePutDotenv writes a map of var-name → value pairs to the dotenv file.
// Only available when --dashboard-write-dotenv is active.
func (h *AdminHandler) handlePutDotenv(c *gin.Context) {
	if !h.dotenvWritable {
		c.JSON(403, gin.H{
			"error":   "dotenv_writes_disabled",
			"message": "Secret writing is disabled. Pass --dashboard-write-dotenv and --dotenv-path to enable.",
		})
		return
	}
	var vars map[string]string
	if err := c.ShouldBindJSON(&vars); err != nil {
		problemJSON(c, 400, "invalid_body", "Expected a JSON object of VAR_NAME: value pairs")
		return
	}
	if err := writeDotenvVars(h.dotenvPath, vars); err != nil {
		problemJSON(c, 500, "internal", "Failed to write .env file: "+err.Error())
		return
	}
	c.JSON(200, gin.H{"message": "Secrets written to " + h.dotenvPath})
}

// writeDotenvVars upserts key=value pairs in a dotenv file.
// Existing entries for a key are updated in-place; new keys are appended.
func writeDotenvVars(path string, vars map[string]string) error {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	lines := strings.Split(string(existing), "\n")
	updated := make(map[string]bool)

	for i, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if val, ok := vars[key]; ok {
			lines[i] = key + "=" + val
			updated[key] = true
		}
	}

	for key, val := range vars {
		if !updated[key] {
			lines = append(lines, key+"="+val)
		}
	}

	result := strings.Join(lines, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return os.WriteFile(path, []byte(result), 0o600)
}
```

Add `"strings"` to the import block if not already present.

- [ ] **Step 4.8: Wire new fields in `internal/cli/serve.go`**

In the `instancezhttp.NewServer(instancezhttp.ServerDeps{...})` call, add:
```go
DotenvWritable: opts.dotenvWritable,
DotenvPath:     opts.dotenvPath,
```

(Do this in both `runServe` and `runDev` paths if they call `NewServer` separately — check serve.go for both.)

- [ ] **Step 4.9: Run all Go unit tests**

Run: `go test -race ./internal/cli/... ./internal/adapter/http/...`  
Expected: PASS.

- [ ] **Step 4.10: Commit**

```bash
git add internal/cli/flags.go internal/cli/serve.go internal/adapter/http/server.go internal/adapter/http/admin_handler.go
git commit -m "feat: --dashboard-write-dotenv flag and PUT /config/dotenv endpoint"
```

---

## Task 5: Update `providers.go` to read from struct fields

**Files:**
- Modify: `internal/cli/providers.go`
- Modify: `internal/cli/providers_test.go`

- [ ] **Step 5.1: Write failing tests in `internal/cli/providers_test.go`**

```go
func TestInitEmailProvider_Resend_UsesStructField(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Email: &domain.EmailProvider{
				Type:   "resend",
				APIKey: "re_test_key",
			},
		},
	}
	sender, err := initEmailProvider(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sender == nil {
		t.Fatal("expected non-nil sender")
	}
}

func TestInitEmailProvider_Resend_MissingKey(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: "resend", APIKey: ""},
		},
	}
	_, err := initEmailProvider(cfg)
	if err == nil {
		t.Fatal("expected error for missing api_key")
	}
}

func TestInitStorageProvider_S3_UsesStructFields(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Storage: &domain.StorageProvider{
				Type:   "s3",
				Bucket: "my-bucket",
				Region: "us-east-1",
			},
		},
	}
	// s3.New makes a real AWS SDK call; just verify no error from our wiring.
	// A missing bucket is caught before that point.
	store, err := initStorageProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestInitStorageProvider_S3_MissingBucket(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Storage: &domain.StorageProvider{Type: "s3", Bucket: ""},
		},
	}
	_, err := initStorageProvider(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for missing bucket")
	}
}

func TestInitStorageProvider_Local_UsesStructPath(t *testing.T) {
	cfg := &domain.Config{
		Providers: domain.Providers{
			Storage: &domain.StorageProvider{
				Type: "local",
				Path: t.TempDir(),
			},
		},
	}
	store, err := initStorageProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}
```

- [ ] **Step 5.2: Run to confirm failure**

Run: `go test ./internal/cli/... -run TestInitEmailProvider|TestInitStorageProvider`  
Expected: FAIL — current code reads from `os.Getenv`, not struct fields.

- [ ] **Step 5.3: Rewrite `initEmailProvider` in `internal/cli/providers.go`**

```go
func initEmailProvider(cfg *domain.Config) (domain.EmailSender, error) {
	if cfg.Providers.Email == nil {
		return nil, nil
	}
	switch cfg.Providers.Email.Type {
	case "resend":
		if cfg.Providers.Email.APIKey == "" {
			return nil, fmt.Errorf("INSTANCEZ_RESEND_API_KEY not set (required for resend provider)")
		}
		return resend.New(cfg.Providers.Email.APIKey), nil
	case "sendgrid":
		if cfg.Providers.Email.APIKey == "" {
			return nil, fmt.Errorf("INSTANCEZ_SENDGRID_API_KEY not set (required for sendgrid provider)")
		}
		return sendgrid.New(cfg.Providers.Email.APIKey), nil
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported email provider: %s (supported: resend, sendgrid)", cfg.Providers.Email.Type)
	}
}
```

- [ ] **Step 5.4: Rewrite `newS3Store` and `initStorageProvider` in `internal/cli/providers.go`**

```go
func initStorageProvider(ctx context.Context, cfg *domain.Config) (domain.ObjectStore, error) {
	if cfg.Providers.Storage == nil {
		return nil, nil
	}
	switch cfg.Providers.Storage.Type {
	case "s3":
		return newS3Store(ctx, cfg.Providers.Storage)
	case "local":
		path := cfg.Providers.Storage.Path
		if path == "" {
			path = "./uploads"
		}
		return NewLocalStore(path, os.Getenv("INSTANCEZ_STORAGE_KEY_PREFIX"))
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported storage provider: %s (supported: s3, local)", cfg.Providers.Storage.Type)
	}
}

func newS3Store(ctx context.Context, p *domain.StorageProvider) (*s3.Store, error) {
	if p.Bucket == "" {
		return nil, fmt.Errorf("INSTANCEZ_S3_BUCKET not set (required for s3 provider)")
	}
	s3Cfg := s3.Config{
		Bucket:          p.Bucket,
		Region:          p.Region,
		Endpoint:        p.Endpoint,
		AccessKeyID:     p.AccessKeyID,
		SecretAccessKey: p.SecretAccessKey,
		KeyPrefix:       os.Getenv("INSTANCEZ_STORAGE_KEY_PREFIX"),
	}
	return s3.New(ctx, s3Cfg)
}
```

- [ ] **Step 5.5: Run tests**

Run: `go test ./internal/cli/... -run TestInitEmailProvider|TestInitStorageProvider`  
Expected: PASS.

- [ ] **Step 5.6: Commit**

```bash
git add internal/cli/providers.go internal/cli/providers_test.go
git commit -m "feat: providers.go reads credentials from config struct fields"
```

---

## Task 6: Validation updates for new provider fields

**Files:**
- Modify: `internal/config/validate.go`
- Modify: `internal/config/validate_test.go`

- [ ] **Step 6.1: Write failing tests**

```go
func TestValidateProviders_ResendRequiresAPIKey(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "test"},
		Providers: domain.Providers{
			Email: &domain.EmailProvider{Type: "resend"},
		},
	}
	errs := Validate(cfg)
	if len(errs) == 0 {
		t.Fatal("expected validation error for missing api_key")
	}
	found := false
	for _, e := range errs {
		if e.Path == "providers.email.api_key" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected providers.email.api_key error, got: %v", errs)
	}
}

func TestValidateProviders_S3RequiresBucket(t *testing.T) {
	cfg := &domain.Config{
		Version: 1,
		Project: domain.Project{Name: "test"},
		Providers: domain.Providers{
			Storage: &domain.StorageProvider{Type: "s3"},
		},
	}
	errs := Validate(cfg)
	if len(errs) == 0 {
		t.Fatal("expected validation error for missing bucket")
	}
	found := false
	for _, e := range errs {
		if e.Path == "providers.storage.bucket" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected providers.storage.bucket error, got: %v", errs)
	}
}
```

- [ ] **Step 6.2: Run to confirm failure**

Run: `go test ./internal/config/... -run TestValidateProviders`  
Expected: FAIL — current `validateProviders` only checks the type string.

- [ ] **Step 6.3: Update `validateProviders` in `internal/config/validate.go`**

Find `validateProviders` (currently ~line 190) and expand it:

```go
func validateProviders(p *domain.Providers) domain.ValidationErrors {
	var errs domain.ValidationErrors

	if p.Email != nil {
		if !validEmailProviders[p.Email.Type] {
			errs = append(errs, &domain.ValidationError{
				Path:    "providers.email.type",
				Message: fmt.Sprintf("unknown email provider type %q", p.Email.Type),
			})
		} else if p.Email.APIKey == "" {
			errs = append(errs, &domain.ValidationError{
				Path:       "providers.email.api_key",
				Message:    "required",
				Suggestion: fmt.Sprintf("Set the env var referenced here or add it to your .env file"),
			})
		}
	}

	if p.Storage != nil {
		if !validStorageProviders[p.Storage.Type] {
			errs = append(errs, &domain.ValidationError{
				Path:    "providers.storage.type",
				Message: fmt.Sprintf("unknown storage provider type %q", p.Storage.Type),
			})
		} else if p.Storage.Type == "s3" && p.Storage.Bucket == "" {
			errs = append(errs, &domain.ValidationError{
				Path:       "providers.storage.bucket",
				Message:    "required for s3 provider",
				Suggestion: "Set INSTANCEZ_S3_BUCKET in your environment or .env file",
			})
		} else if p.Storage.Type == "local" && p.Storage.Path == "" {
			// default path is ./uploads — not an error, just warn via suggestion
		}
	}

	return errs
}
```

- [ ] **Step 6.4: Run tests**

Run: `go test ./internal/config/... -run TestValidateProviders`  
Expected: PASS.

- [ ] **Step 6.5: Run full Go unit test suite**

Run: `go build ./... && go test -race ./...`  
Expected: PASS.

- [ ] **Step 6.6: Commit**

```bash
git add internal/config/validate.go internal/config/validate_test.go
git commit -m "feat: validate api_key and bucket in provider config"
```

---

## Task 7: Dashboard API client additions

**Files:**
- Modify: `dashboard/src/api/client.ts`
- Modify: `dashboard/src/api/client.test.ts`

- [ ] **Step 7.1: Write failing tests in `dashboard/src/api/client.test.ts`**

Look at the existing test file to understand the mock pattern, then add:

```typescript
describe("getEnvVars", () => {
  it("fetches env var status from /config/env-vars", async () => {
    const mockVars = {
      vars: {
        INSTANCEZ_RESEND_API_KEY: { set: true },
        INSTANCEZ_GOOGLE_CLIENT_ID: { set: false },
      },
    };
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => mockVars,
    } as Response);

    const result = await getEnvVars();
    expect(result).toEqual(mockVars);
    expect(globalThis.fetch).toHaveBeenCalledWith(
      "/api/_admin/config/env-vars",
      expect.objectContaining({ method: undefined })
    );
  });
});

describe("putDotenv", () => {
  it("posts to /config/dotenv with var map", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ message: "ok" }),
    } as Response);

    await putDotenv({ INSTANCEZ_RESEND_API_KEY: "re_test" });
    expect(globalThis.fetch).toHaveBeenCalledWith(
      "/api/_admin/config/dotenv",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ INSTANCEZ_RESEND_API_KEY: "re_test" }),
      })
    );
  });
});
```

- [ ] **Step 7.2: Run to confirm failure**

Run: `cd dashboard && npm test -- --run client`  
Expected: FAIL — `getEnvVars` and `putDotenv` not exported from `client.ts`.

- [ ] **Step 7.3: Add `getEnvVars` and `putDotenv` to `dashboard/src/api/client.ts`**

Add after `getConfigStatus`:

```typescript
export interface EnvVarsResponse {
  vars: Record<string, { set: boolean }>;
}

export async function getEnvVars(): Promise<EnvVarsResponse> {
  return request<EnvVarsResponse>("/config/env-vars");
}

export async function putDotenv(
  vars: Record<string, string>
): Promise<{ message: string }> {
  return request("/config/dotenv", {
    method: "PUT",
    body: JSON.stringify(vars),
  });
}
```

Also add `EnvVarsResponse` to the import from `../lib/types` if you move the type there, or keep it inline in `client.ts` as shown.

- [ ] **Step 7.4: Run dashboard tests**

Run: `npm test -- --run`  
Expected: PASS.

- [ ] **Step 7.5: Commit**

```bash
git add dashboard/src/api/client.ts dashboard/src/api/client.test.ts
git commit -m "feat: add getEnvVars and putDotenv API client functions"
```

---

## Task 8: Providers page redesign

**Files:**
- Modify: `dashboard/src/pages/Providers.tsx`
- Modify: `dashboard/src/pages/Providers.test.tsx`

- [ ] **Step 8.1: Write failing tests in `dashboard/src/pages/Providers.test.tsx`**

Read the existing test file first to understand mock patterns, then add/replace tests:

```typescript
import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { ProvidersPage } from "./Providers";
import { ConfigContext } from "../hooks/useConfig";
import type { Config } from "../lib/types";

// Mock client
vi.mock("../api/client", () => ({
  getEnvVars: vi.fn().mockResolvedValue({
    vars: {
      INSTANCEZ_RESEND_API_KEY: { set: true },
      INSTANCEZ_SENDGRID_API_KEY: { set: false },
    },
  }),
  putDotenv: vi.fn().mockResolvedValue({ message: "ok" }),
}));

const baseConfig: Config = {
  version: 1,
  project: { name: "test", description: "" },
  extensions: [],
  server: {} as any,
  providers: { email: null, storage: null },
  auth: null,
  tables: {},
  storage: {},
  rpc: {},
  functions: {},
  seeds: {},
};

function renderProviders(config: Config, dotenvWritable = false) {
  return render(
    <ConfigContext.Provider
      value={{
        config,
        loading: false,
        error: null,
        checksum: "v1",
        saving: false,
        saveErrors: [],
        refresh: vi.fn(),
        save: vi.fn().mockResolvedValue(true),
        updateConfig: vi.fn(),
        dotenvWritable,
      }}
    >
      <ProvidersPage />
    </ConfigContext.Provider>
  );
}

describe("ProvidersPage", () => {
  it("shows env var name badge when resend selected", async () => {
    const cfg = {
      ...baseConfig,
      providers: {
        email: { type: "resend", api_key: "${INSTANCEZ_RESEND_API_KEY}", default_from_email: "" },
        storage: null,
      },
    };
    renderProviders(cfg);
    expect(await screen.findByText("INSTANCEZ_RESEND_API_KEY")).toBeInTheDocument();
  });

  it("shows set status badge when var is set", async () => {
    const cfg = {
      ...baseConfig,
      providers: {
        email: { type: "resend", api_key: "${INSTANCEZ_RESEND_API_KEY}", default_from_email: "" },
        storage: null,
      },
    };
    renderProviders(cfg);
    expect(await screen.findByTitle("INSTANCEZ_RESEND_API_KEY is set")).toBeInTheDocument();
  });

  it("renders default_from_email as editable input", async () => {
    const cfg = {
      ...baseConfig,
      providers: {
        email: { type: "resend", api_key: "${INSTANCEZ_RESEND_API_KEY}", default_from_email: "hello@acme.com" },
        storage: null,
      },
    };
    renderProviders(cfg);
    expect(await screen.findByDisplayValue("hello@acme.com")).toBeInTheDocument();
  });

  it("shows editable input for var when dotenvWritable", async () => {
    const cfg = {
      ...baseConfig,
      providers: {
        email: { type: "resend", api_key: "${INSTANCEZ_RESEND_API_KEY}", default_from_email: "" },
        storage: null,
      },
    };
    renderProviders(cfg, true);
    const input = await screen.findByPlaceholderText("INSTANCEZ_RESEND_API_KEY value");
    expect(input).toBeInTheDocument();
  });
});
```

- [ ] **Step 8.2: Run to confirm failure**

Run: `npm test -- --run Providers`  
Expected: FAIL — component doesn't yet use `getEnvVars` or show var status badges.

- [ ] **Step 8.3: Rewrite `dashboard/src/pages/Providers.tsx`**

```tsx
import { useState, useEffect, useCallback } from "react";
import { useConfig } from "../hooks/useConfig";
import { getEnvVars, putDotenv } from "../api/client";
import type { EnvVarsResponse } from "../api/client";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import { CheckCard, Panel, Section, Field, Input } from "../components/ui";
import { Toggle } from "../components/Toggle";
import type { Config, EmailProviderConfig, StorageProviderConfig } from "../lib/types";
import { Mail, HardDrive } from "lucide-react";

const EMAIL_PROVIDERS = [
  { value: "resend",   label: "Resend",   description: "Modern email API for developers" },
  { value: "sendgrid", label: "SendGrid", description: "Twilio email delivery service" },
] as const;

const STORAGE_PROVIDERS = [
  { value: "s3",    label: "AWS S3",                description: "Amazon Simple Storage Service" },
  { value: "gcs",   label: "Google Cloud Storage", description: "Google Cloud object storage" },
  { value: "minio", label: "MinIO",                description: "S3-compatible object storage" },
  { value: "local", label: "Local Filesystem",     description: "Store files on the local disk" },
] as const;

const EMAIL_VARS: Record<string, string[]> = {
  resend:   ["INSTANCEZ_RESEND_API_KEY"],
  sendgrid: ["INSTANCEZ_SENDGRID_API_KEY"],
};

const STORAGE_VARS: Record<string, string[]> = {
  s3:    ["INSTANCEZ_S3_BUCKET", "AWS_REGION"],
  gcs:   ["INSTANCEZ_GCS_BUCKET", "INSTANCEZ_GCS_CREDENTIALS"],
  minio: ["INSTANCEZ_MINIO_ENDPOINT", "INSTANCEZ_MINIO_ACCESS_KEY", "INSTANCEZ_MINIO_SECRET_KEY", "INSTANCEZ_MINIO_BUCKET"],
  local: ["INSTANCEZ_LOCAL_STORAGE_PATH"],
};

const S3_EXPLICIT_CRED_VARS = ["AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"];

/** Renders a single env var as a status badge, with optional editable input. */
function VarRow({
  name,
  envStatus,
  writable,
  pendingValues,
  onValueChange,
}: {
  name: string;
  envStatus: Record<string, { set: boolean }>;
  writable: boolean;
  pendingValues: Record<string, string>;
  onValueChange: (name: string, value: string) => void;
}) {
  const isSet = envStatus[name]?.set ?? false;
  return (
    <div className="flex items-center gap-3">
      <span
        title={`${name} is ${isSet ? "set" : "not set"}`}
        className={`text-xs font-mono px-2 py-0.5 rounded-md border ${
          isSet
            ? "bg-green-50 border-green-200 text-green-700 dark:bg-green-950 dark:border-green-800 dark:text-green-400"
            : "bg-red-50 border-red-200 text-red-700 dark:bg-red-950 dark:border-red-800 dark:text-red-400"
        }`}
      >
        {name} {isSet ? "✓" : "✗"}
      </span>
      {writable && (
        <Input
          mono
          type="password"
          className="flex-1 text-xs"
          placeholder={`${name} value`}
          value={pendingValues[name] ?? ""}
          onChange={(e) => onValueChange(name, e.target.value)}
        />
      )}
    </div>
  );
}

function buildEmailProvider(type: string): EmailProviderConfig {
  return {
    type,
    api_key: `\${INSTANCEZ_${type.toUpperCase()}_API_KEY}`,
    default_from_email: "",
  };
}

function buildStorageProvider(type: string, explicitCreds: boolean): StorageProviderConfig {
  const base: StorageProviderConfig = {
    type, bucket: "", region: "", access_key_id: "", secret_access_key: "",
    endpoint: "", credentials: "", path: "",
  };
  if (type === "s3") {
    base.bucket = "${INSTANCEZ_S3_BUCKET}";
    base.region = "${AWS_REGION}";
    if (explicitCreds) {
      base.access_key_id = "${AWS_ACCESS_KEY_ID}";
      base.secret_access_key = "${AWS_SECRET_ACCESS_KEY}";
    }
  } else if (type === "gcs") {
    base.bucket = "${INSTANCEZ_GCS_BUCKET}";
    base.credentials = "${INSTANCEZ_GCS_CREDENTIALS}";
  } else if (type === "minio") {
    base.bucket = "${INSTANCEZ_MINIO_BUCKET}";
    base.endpoint = "${INSTANCEZ_MINIO_ENDPOINT}";
    base.access_key_id = "${INSTANCEZ_MINIO_ACCESS_KEY}";
    base.secret_access_key = "${INSTANCEZ_MINIO_SECRET_KEY}";
  } else if (type === "local") {
    base.path = "${INSTANCEZ_LOCAL_STORAGE_PATH}";
  }
  return base;
}

export function ProvidersPage() {
  const { config, save, saving, saveErrors, dotenvWritable } = useConfig();
  const [local, setLocal] = useState<Config | null>(null);
  const [dirty, setDirty] = useState(false);
  const [envStatus, setEnvStatus] = useState<Record<string, { set: boolean }>>({});
  const [pendingDotenv, setPendingDotenv] = useState<Record<string, string>>({});
  const [s3ExplicitCreds, setS3ExplicitCreds] = useState(false);

  useEffect(() => {
    if (config) {
      setLocal(structuredClone(config));
      setDirty(false);
      // Detect if existing S3 config has explicit creds
      const st = config.providers.storage;
      if (st?.type === "s3" && st.access_key_id) setS3ExplicitCreds(true);
    }
  }, [config]);

  const refreshEnvVars = useCallback(() => {
    getEnvVars().then((r) => setEnvStatus(r.vars)).catch(() => {});
  }, []);

  useEffect(() => { refreshEnvVars(); }, [refreshEnvVars]);

  function update(updater: (prev: Config) => Config) {
    setLocal((prev) => {
      if (!prev) return prev;
      setDirty(true);
      return updater(prev);
    });
  }

  function selectEmailProvider(type: string | null) {
    update((c) => ({
      ...c,
      providers: {
        ...c.providers,
        email: type ? buildEmailProvider(type) : null,
      },
    }));
  }

  function selectStorageProvider(type: string | null) {
    update((c) => ({
      ...c,
      providers: {
        ...c.providers,
        storage: type ? buildStorageProvider(type, s3ExplicitCreds) : null,
      },
    }));
  }

  function toggleS3ExplicitCreds(val: boolean) {
    setS3ExplicitCreds(val);
    update((c) => {
      if (c.providers.storage?.type !== "s3") return c;
      return {
        ...c,
        providers: {
          ...c.providers,
          storage: buildStorageProvider("s3", val),
        },
      };
    });
  }

  async function handleSave() {
    if (!local) return;
    const ok = await save(local);
    if (ok && Object.keys(pendingDotenv).length > 0) {
      await putDotenv(pendingDotenv).catch(() => {});
      setPendingDotenv({});
      refreshEnvVars();
    }
    setDirty(false);
  }

  if (!local) return null;

  const selectedEmail = local.providers.email?.type ?? null;
  const selectedStorage = local.providers.storage?.type ?? null;
  const emailVars = selectedEmail ? EMAIL_VARS[selectedEmail] ?? [] : [];
  const storageVars = selectedStorage
    ? [
        ...(STORAGE_VARS[selectedStorage] ?? []),
        ...(selectedStorage === "s3" && s3ExplicitCreds ? S3_EXPLICIT_CRED_VARS : []),
      ]
    : [];

  return (
    <div className="pb-20">
      <PageHeader
        title="Providers"
        description="Configure email and storage providers for your project"
      />

      <div className="px-8 pb-8 space-y-6 max-w-3xl">
        <Section title="Email Provider" description="Used for sending verification emails, password resets, and notifications" icon={Mail}>
          <div className="grid grid-cols-2 gap-3">
            {EMAIL_PROVIDERS.map((p) => (
              <CheckCard
                key={p.value}
                selected={selectedEmail === p.value}
                onClick={() => selectEmailProvider(selectedEmail === p.value ? null : p.value)}
                title={p.label}
                description={p.description}
              />
            ))}
          </div>

          {selectedEmail && (
            <Panel className="px-4 py-3 space-y-3">
              {local.providers.email && (
                <Field label="Default From Email">
                  <Input
                    value={local.providers.email.default_from_email}
                    onChange={(e) =>
                      update((c) => ({
                        ...c,
                        providers: {
                          ...c.providers,
                          email: { ...c.providers.email!, default_from_email: e.target.value },
                        },
                      }))
                    }
                    placeholder="Acme <noreply@acme.com>"
                  />
                </Field>
              )}
              <div className="space-y-1.5">
                <p className="text-xs font-medium text-foreground">Required environment variables</p>
                {emailVars.map((v) => (
                  <VarRow
                    key={v}
                    name={v}
                    envStatus={envStatus}
                    writable={!!dotenvWritable}
                    pendingValues={pendingDotenv}
                    onValueChange={(n, val) => setPendingDotenv((p) => ({ ...p, [n]: val }))}
                  />
                ))}
                <p className="text-xs text-muted-foreground/70 italic mt-1">
                  Set these in your <code className="font-mono">.development.env</code> file or deployment environment.
                </p>
              </div>
            </Panel>
          )}
        </Section>

        <Section title="Storage Provider" description="Used for file uploads and object storage" icon={HardDrive}>
          <div className="grid grid-cols-2 gap-3">
            {STORAGE_PROVIDERS.map((p) => (
              <CheckCard
                key={p.value}
                selected={selectedStorage === p.value}
                onClick={() => selectStorageProvider(selectedStorage === p.value ? null : p.value)}
                title={p.label}
                description={p.description}
              />
            ))}
          </div>

          {selectedStorage && (
            <Panel className="px-4 py-3 space-y-3">
              {selectedStorage === "s3" && (
                <Toggle
                  checked={s3ExplicitCreds}
                  onChange={toggleS3ExplicitCreds}
                  label="Explicit credentials (disable for IAM/ECS role-based auth)"
                />
              )}
              <div className="space-y-1.5">
                <p className="text-xs font-medium text-foreground">Required environment variables</p>
                {storageVars.map((v) => (
                  <VarRow
                    key={v}
                    name={v}
                    envStatus={envStatus}
                    writable={!!dotenvWritable}
                    pendingValues={pendingDotenv}
                    onValueChange={(n, val) => setPendingDotenv((p) => ({ ...p, [n]: val }))}
                  />
                ))}
                <p className="text-xs text-muted-foreground/70 italic mt-1">
                  Set these in your <code className="font-mono">.development.env</code> file or deployment environment.
                </p>
              </div>
            </Panel>
          )}
        </Section>
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
```

- [ ] **Step 8.4: Expose `dotenvWritable` from `useConfig`**

Open `dashboard/src/hooks/useConfig.ts` and add `dotenvWritable: boolean` to the `ConfigState` interface and `useConfigState` return value. Read it from the config status:

In `useConfigState`, add state:
```typescript
const [dotenvWritable, setDotenvWritable] = useState(false);
```

In the `refresh` callback, after `setConfig(cfg)`, fetch status to check `dotenv_writable`:
```typescript
const status = await getConfigStatus().catch(() => null);
if (status) setDotenvWritable(status.dotenv_writable ?? false);
```

Add `dotenvWritable` to the returned object and to the `ConfigState` interface.

Also update `dashboard/src/lib/types.ts` — add `dotenv_writable?: boolean` to the `ConfigStatus` type.

- [ ] **Step 8.5: Run tests**

Run: `npm test -- --run Providers`  
Expected: PASS.

- [ ] **Step 8.6: Commit**

```bash
git add dashboard/src/pages/Providers.tsx dashboard/src/pages/Providers.test.tsx dashboard/src/hooks/useConfig.ts dashboard/src/lib/types.ts
git commit -m "feat: Providers page with env var status badges and dotenv write support"
```

---

## Task 9: Auth page — replace OAuth text inputs with var-status badges

**Files:**
- Modify: `dashboard/src/pages/Auth.tsx`
- Modify: `dashboard/src/pages/Auth.test.tsx`

- [ ] **Step 9.1: Write failing tests in `dashboard/src/pages/Auth.test.tsx`**

Read the existing test file to understand the mock setup, then add:

```typescript
it("shows INSTANCEZ_GOOGLE_CLIENT_ID badge instead of text input", async () => {
  const cfg = {
    ...baseConfig,
    auth: {
      jwt_expiry: "15m",
      refresh_tokens: true,
      refresh_token_expiry: "7d",
      email: null,
      google: {
        client_id: "${INSTANCEZ_GOOGLE_CLIENT_ID}",
        client_secret: "${INSTANCEZ_GOOGLE_CLIENT_SECRET}",
        redirect_url: "${INSTANCEZ_GOOGLE_REDIRECT_URL}",
      },
      github: null,
    },
  };
  renderAuth(cfg);
  expect(await screen.findByText(/INSTANCEZ_GOOGLE_CLIENT_ID/)).toBeInTheDocument();
  expect(screen.queryByPlaceholderText(/client.id/i)).toBeNull();
});
```

- [ ] **Step 9.2: Run to confirm failure**

Run: `npm test -- --run Auth`  
Expected: FAIL — current Auth page still renders text inputs for OAuth fields.

- [ ] **Step 9.3: Update the OAuth provider section in `dashboard/src/pages/Auth.tsx`**

Add imports at the top:
```typescript
import { getEnvVars, putDotenv } from "../api/client";
import type { EnvVarsResponse } from "../api/client";
```

Add state to `AuthPage`:
```typescript
const [envStatus, setEnvStatus] = useState<Record<string, { set: boolean }>>({});
const [pendingDotenv, setPendingDotenv] = useState<Record<string, string>>({});
const dotenvWritable = config ? (config as any)._dotenvWritable : false;
// Note: use useConfig().dotenvWritable instead:
const { dotenvWritable: canWriteDotenv } = useConfig();
```

Actually simpler — destructure `dotenvWritable` from `useConfig()` which now exposes it.

Add effect to load env vars when auth config exists:
```typescript
useEffect(() => {
  getEnvVars().then((r) => setEnvStatus(r.vars)).catch(() => {});
}, []);
```

Replace the OAuth provider `{isEnabled && providerConfig && (...)}` block (currently lines 242-265) with:

```tsx
{isEnabled && (
  <div className="space-y-2">
    {(["client_id", "client_secret", "redirect_url"] as const).map((field) => {
      const varName = `INSTANCEZ_${provider.toUpperCase()}_${field.toUpperCase()}`;
      const isSet = envStatus[varName]?.set ?? false;
      return (
        <div key={field} className="flex items-center gap-3">
          <span
            title={`${varName} is ${isSet ? "set" : "not set"}`}
            className={`text-xs font-mono px-2 py-0.5 rounded-md border flex-shrink-0 ${
              isSet
                ? "bg-green-50 border-green-200 text-green-700 dark:bg-green-950 dark:border-green-800 dark:text-green-400"
                : "bg-red-50 border-red-200 text-red-700 dark:bg-red-950 dark:border-red-800 dark:text-red-400"
            }`}
          >
            {varName} {isSet ? "✓" : "✗"}
          </span>
          {canWriteDotenv && (
            <Input
              mono
              type={field === "client_secret" ? "password" : "text"}
              className="flex-1 text-xs"
              placeholder={`${varName} value`}
              value={pendingDotenv[varName] ?? ""}
              onChange={(e) =>
                setPendingDotenv((p) => ({ ...p, [varName]: e.target.value }))
              }
            />
          )}
        </div>
      );
    })}
    <p className="text-xs text-muted-foreground/70 italic">
      Set these in your <code className="font-mono">.development.env</code> file or deployment environment.
    </p>
  </div>
)}
```

Update `handleSave` to write dotenv vars when present:
```typescript
async function handleSave() {
  if (!config) return;
  const updated = { ...config, auth: enabled ? auth : null };
  const ok = await save(updated);
  if (ok && Object.keys(pendingDotenv).length > 0) {
    await putDotenv(pendingDotenv).catch(() => {});
    setPendingDotenv({});
    getEnvVars().then((r) => setEnvStatus(r.vars)).catch(() => {});
  }
  setDirty(false);
}
```

When toggling an OAuth provider on, write `${VAR}` refs into the auth config instead of empty strings:

Find the `onChange` for the OAuth provider toggle (line ~232-238) and replace the default value:
```typescript
onChange={() =>
  updateAuth((a) => ({
    ...a,
    [provider]: isEnabled
      ? null
      : {
          client_id: `\${INSTANCEZ_${provider.toUpperCase()}_CLIENT_ID}`,
          client_secret: `\${INSTANCEZ_${provider.toUpperCase()}_CLIENT_SECRET}`,
          redirect_url: `\${INSTANCEZ_${provider.toUpperCase()}_REDIRECT_URL}`,
        },
  }))
}
```

- [ ] **Step 9.4: Run dashboard tests**

Run: `npm test -- --run`  
Expected: PASS.

- [ ] **Step 9.5: Run full test suite**

```bash
go build ./...
go test -race ./...
cd dashboard && npm test -- --run
```
Expected: all PASS.

- [ ] **Step 9.6: Final commit**

```bash
git add dashboard/src/pages/Auth.tsx dashboard/src/pages/Auth.test.tsx
git commit -m "feat: Auth page OAuth section uses env var status badges"
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|-----------------|------|
| `EmailProvider.APIKey` + `DefaultFromEmail` fields | Task 1 |
| `StorageProvider` credential fields | Task 1 |
| `ParseBytesRaw` — no env var interpolation | Task 2 |
| `GET /config` returns unresolved config | Task 2 |
| `PUT /config` re-resolves before `updateConfigFn` | Task 2 |
| `GET /config/env-vars` endpoint | Task 3 |
| `--dashboard-write-dotenv` flag | Task 4 |
| `PUT /config/dotenv` endpoint | Task 4 |
| `dotenv_writable` in `/config/status` | Task 4 |
| providers.go reads from struct | Task 5 |
| Validation for api_key / bucket | Task 6 |
| TS types updated | Task 1 |
| `getEnvVars` / `putDotenv` API client | Task 7 |
| Providers page: status badges + `default_from_email` + S3 toggle | Task 8 |
| Auth page: var-badge OAuth section | Task 9 |
| YAML `${VAR}` refs written on enable | Tasks 8 + 9 |
| TypeScript `ConfigStatus.dotenv_writable` | Task 8 |
| `useConfig` exposes `dotenvWritable` | Task 8 |

All spec requirements covered. ✓
