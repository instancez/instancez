# Ultra CLI Cloud Commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add CLI commands and flags that let `ultra` talk to the Ultrabase Cloud backend hosted in `instancez-coder/v2` — `ultra login`, `ultra logout`, `ultra whoami`, `ultra init --with-cloud`, `ultra init --generate-like`, `ultra deploy`, `ultra validate --project`.

**Architecture:**
- New `internal/cloud/` package owns: credential storage at `~/.ultra/credentials` (mode 0600 JSON), a typed HTTP client over the v2 API, and YAML helpers for reading/writing `project_id` inside `ultrabase.yaml`.
- Two new top-level commands: `ultra login` (device-code flow) and `ultra logout` (forgets local credentials).
- One new command: `ultra deploy` (push to a cloud project).
- Two existing commands get new flags: `ultra init --with-cloud --generate-like` and `ultra validate --project`.
- `--with-cloud` is **independent** of `--with-dsn` / `--with-docker`. The first sets up the cloud target; the latter two set up a local dev DB. They compose freely.
- Idle / heartbeat / `--use-cloud-ephemeral` is **out of scope** (deferred). Local dev still uses `--use-dsn` against the user's own Postgres.

**Tech Stack:** Go 1.25, cobra, stdlib `net/http`, stdlib `os/exec` for browser open (cross-platform via `xdg-open` / `open` / `cmd /c start`), `gopkg.in/yaml.v3` (already used in `internal/config`).

**Prerequisite:** the v2 backend plan at `/home/saedx1/repos/instancez-coder/v2/docs/superpowers/plans/2026-05-25-ultrabase-cloud-cli-backend.md` must be merged (or at least Part A + Part B) before the CLI commands can hit anything real. Parts D–F here can be developed against a stub server if v2 is in-flight.

---

## Scope

The plan is organized into **seven independently shippable parts**. Part A is a strict prerequisite for B–G; the rest can be picked up in any order.

- **Part A** — `internal/cloud/` foundation (credentials store + HTTP client + YAML helpers)
- **Part B** — `ultra login`
- **Part C** — `ultra logout`
- **Part D** — `ultra init --with-cloud` and `ultra init --generate-like`
- **Part E** — `ultra deploy`
- **Part F** — `ultra validate --project`
- **Part G** — `ultra whoami`

The engineer should use `superpowers:using-git-worktrees` to isolate this work. Each part should land as its own PR, gated by `make test` (or the underlying `go test -race ./...` + `npm test`).

---

## File Structure

### New files
- `internal/cloud/credentials.go` — `Load() / Save() / Delete()` for `~/.ultra/credentials`
- `internal/cloud/credentials_test.go` — uses `t.TempDir()` to isolate filesystem
- `internal/cloud/client.go` — `*Client` with typed methods (`DeviceCode`, `DeviceToken`, `CreateProject`, `Deploy`, `MigrationPreview`, `GenerateYAML`, `UploadYAML`, `Whoami`)
- `internal/cloud/client_test.go` — uses `httptest.Server`
- `internal/cloud/config.go` — `APIURL()` (env-or-default) + `APIURLFromConfig(path)` (yaml > env > default)
- `internal/cloud/yaml_project_id.go` — read/write `project.cloud.project_id` and read `project.cloud.api_url` in ultrabase.yaml
- `internal/cloud/yaml_project_id_test.go`
- `internal/cloud/browser.go` — `OpenBrowser(url)` cross-platform launcher
- `internal/cloud/browser_test.go`
- `internal/cli/login.go` — `newLoginCmd()` cobra command
- `internal/cli/login_test.go`
- `internal/cli/logout.go` — `newLogoutCmd()`
- `internal/cli/logout_test.go`
- `internal/cli/deploy.go` — `newDeployCmd()`
- `internal/cli/deploy_test.go`
- `internal/cli/whoami.go` — `newWhoamiCmd()`
- `internal/cli/whoami_test.go`
- `internal/cli/init.go` (modify) — add `--with-cloud` and `--generate-like` flags + composition logic
- `internal/cli/init_test.go` (modify) — new test cases
- `internal/cli/validate.go` (modify) — add `--project` flag mode + `planAgainstProject`
- `internal/cli/validate_test.go` (modify)
- `internal/cli/root.go` (modify) — register `newLoginCmd`, `newLogoutCmd`, `newDeployCmd`, `newWhoamiCmd`

### Naming & conventions (match the existing repo)
- Cobra command constructors are `new<Verb>Cmd()` returning `*cobra.Command`.
- Tests use Go's stdlib `testing` + `testify/assert` (present in `go.mod`).
- File mode for credentials: `0600`. Directory: `0700`.
- Use `internal/config` for any YAML touching — don't shell out to external tools.
- Error wrapping uses `fmt.Errorf("…: %w", err)` (matches existing style).
- Bash error messages start lower-case and don't end with a period (matches existing `errors.New("--with-dsn and --with-docker are mutually exclusive")` style).

---

# Part A — `internal/cloud/` Foundation

Three concerns:

1. **Credentials**: a single PAT and the user's email/login state, stored at `~/.ultra/credentials` (JSON, 0600).
2. **HTTP client**: thin wrapper over `http.Client` with typed methods for each v2 endpoint we call. Sends `Authorization: Bearer <pat>` automatically when credentials are loaded.
3. **YAML project_id**: helpers to read/write `project.cloud.project_id` inside `ultrabase.yaml` without disturbing the rest of the file. Uses `yaml.Node` round-tripping.

### Task A1: Implement `credentials.go`

**Files:**
- Create: `internal/cloud/credentials.go`
- Create: `internal/cloud/credentials_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cloud

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCredentialsRoundtrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Empty load returns ErrNoCredentials.
	_, err := Load()
	assert.ErrorIs(t, err, ErrNoCredentials)

	// Save then Load returns the same value.
	saved := Credentials{PAT: "ultra_pat_abc123", Email: "me@example.com"}
	assert.NoError(t, Save(saved))

	loaded, err := Load()
	assert.NoError(t, err)
	assert.Equal(t, saved, loaded)

	// File mode is 0600.
	info, err := os.Stat(filepath.Join(dir, ".ultra", "credentials"))
	assert.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// Delete removes the file.
	assert.NoError(t, Delete())
	_, err = Load()
	assert.ErrorIs(t, err, ErrNoCredentials)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestCredentialsRoundtrip -v`
Expected: FAIL with `no Go files in …/internal/cloud` (directory doesn't exist).

- [ ] **Step 3: Implement the package**

Create `internal/cloud/credentials.go`:

```go
// Package cloud provides the CLI-side client for Ultrabase Cloud (v2 backend).
package cloud

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoCredentials means no credentials file exists yet. Callers typically
// translate this into "run `ultra login` first".
var ErrNoCredentials = errors.New("no credentials; run `ultra login` first")

// Credentials are the minimal state needed to authenticate against the
// Ultrabase Cloud API. PAT is a Personal Access Token returned by the
// device-code flow. Email is informational (printed in `whoami`-style
// messages); never derived from the token client-side.
type Credentials struct {
	PAT   string `json:"pat"`
	Email string `json:"email,omitempty"`
}

// credentialsPath returns the absolute path to ~/.ultra/credentials.
// Honors HOME for testability.
func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, ".ultra", "credentials"), nil
}

// Load reads credentials from disk. Returns ErrNoCredentials if the file
// does not exist; any other error (corrupt JSON, permission denied) is
// surfaced verbatim.
func Load() (Credentials, error) {
	p, err := credentialsPath()
	if err != nil {
		return Credentials{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Credentials{}, ErrNoCredentials
		}
		return Credentials{}, fmt.Errorf("read credentials: %w", err)
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return Credentials{}, fmt.Errorf("parse credentials: %w", err)
	}
	return c, nil
}

// Save writes credentials to ~/.ultra/credentials with mode 0600. Creates
// the parent directory (mode 0700) if missing. Overwrites any existing
// file atomically (write-to-temp + rename).
func Save(c Credentials) error {
	p, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credentials: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("rename credentials: %w", err)
	}
	return nil
}

// Delete removes the credentials file. Missing file is not an error.
func Delete() error {
	p, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete credentials: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestCredentialsRoundtrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cloud/credentials.go internal/cloud/credentials_test.go
git commit -m "cloud: add credentials store at ~/.ultra/credentials"
```

---

### Task A2: Implement `config.go` (API URL resolution)

Three layers of override, in precedence order:
1. **Explicit argument** — pass-through for callers who already resolved a value (rarely used).
2. **`ultrabase.yaml`** at `project.cloud.api_url` — project-pinned override.
3. **`ULTRABASE_CLOUD_API` env var** — per-shell override.
4. **Built-in placeholder default** — explicitly marked as TBD; deployment must set one of the above.

Login/logout (which run before any project exists) use `APIURL()` — env-or-default. Project-bound commands (deploy, validate, whoami inside a project dir) use `APIURLFromConfig(configPath)`.

**Files:**
- Create: `internal/cloud/config.go`
- Create: `internal/cloud/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cloud

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIURLDefault(t *testing.T) {
	t.Setenv("ULTRABASE_CLOUD_API", "")
	assert.Equal(t, defaultCloudAPI, APIURL())
}

func TestAPIURLFromEnv(t *testing.T) {
	t.Setenv("ULTRABASE_CLOUD_API", "https://staging.cloud.example.com")
	assert.Equal(t, "https://staging.cloud.example.com", APIURL())
}

func TestAPIURLTrimsTrailingSlash(t *testing.T) {
	t.Setenv("ULTRABASE_CLOUD_API", "https://x.example.com/")
	assert.Equal(t, "https://x.example.com", APIURL())
}

func TestAPIURLFromConfigPrefersYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "ultrabase.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte(`
version: 1
project:
  cloud:
    api_url: https://project-pinned.example.com
`), 0o644))

	t.Setenv("ULTRABASE_CLOUD_API", "https://env.example.com")
	got, err := APIURLFromConfig(yamlPath)
	assert.NoError(t, err)
	assert.Equal(t, "https://project-pinned.example.com", got)
}

func TestAPIURLFromConfigFallsBackToEnv(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "ultrabase.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte(`
version: 1
project:
  name: x
`), 0o644))

	t.Setenv("ULTRABASE_CLOUD_API", "https://env.example.com")
	got, err := APIURLFromConfig(yamlPath)
	assert.NoError(t, err)
	assert.Equal(t, "https://env.example.com", got)
}

func TestAPIURLFromConfigMissingFile(t *testing.T) {
	t.Setenv("ULTRABASE_CLOUD_API", "https://env.example.com")
	// Missing file → fall back to env, no error.
	got, err := APIURLFromConfig("/no/such/file.yaml")
	assert.NoError(t, err)
	assert.Equal(t, "https://env.example.com", got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestAPIURL -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `internal/cloud/config.go`:

```go
package cloud

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// defaultCloudAPI is a placeholder. The real hostname is not yet locked in;
// deployments are expected to set ULTRABASE_CLOUD_API or pin api_url in
// ultrabase.yaml. The default exists only so `ultra login` produces a
// recognizable "couldn't connect to ..." error instead of an empty URL.
const defaultCloudAPI = "https://api.ultrabase.invalid"

// APIURL returns the base URL for the Ultrabase Cloud API, considering only
// the environment variable. Used by commands that run without a project
// context (login, logout).
func APIURL() string {
	if v := os.Getenv("ULTRABASE_CLOUD_API"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultCloudAPI
}

// APIURLFromConfig returns the base URL with project-level override applied.
// Reads project.cloud.api_url from the given ultrabase.yaml; falls back to
// APIURL() if the file is missing or has no api_url field.
//
// Returns an error only on malformed YAML — a missing file or absent field
// is fine and yields the env/default value.
func APIURLFromConfig(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return APIURL(), nil
		}
		return "", fmt.Errorf("read %s: %w", configPath, err)
	}
	pinned, err := ReadAPIURL(data)
	if err != nil {
		return "", err
	}
	if pinned != "" {
		return strings.TrimRight(pinned, "/"), nil
	}
	return APIURL(), nil
}
```

(`ReadAPIURL` is defined in Task A6 — the test will fail until both Task A2 and Task A6 are complete. Order: write A2 code, write A6 code, then both tests pass.)

- [ ] **Step 4: Run test to verify it passes**

After completing Task A6 below, re-run:

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestAPIURL -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cloud/config.go internal/cloud/config_test.go
git commit -m "cloud: add APIURL/APIURLFromConfig with yaml + env overrides"
```

---

### Task A3: Implement HTTP client skeleton + `DeviceCode` method

**Files:**
- Create: `internal/cloud/client.go`
- Create: `internal/cloud/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientDeviceCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/auth/device/code", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "dc_abc",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://x/device",
			"expires_in":       900,
			"interval":         5,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	resp, err := c.DeviceCode()
	assert.NoError(t, err)
	assert.Equal(t, "dc_abc", resp.DeviceCode)
	assert.Equal(t, "WDJB-MJHT", resp.UserCode)
	assert.Equal(t, "https://x/device", resp.VerificationURI)
	assert.Equal(t, 900, resp.ExpiresIn)
	assert.Equal(t, 5, resp.Interval)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestClientDeviceCode -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement the client skeleton + DeviceCode**

Create `internal/cloud/client.go`:

```go
package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to the Ultrabase Cloud API. Bearer is the PAT (or "" for
// unauthenticated calls like the device-flow start). HTTP is the underlying
// http.Client; tests inject one bound to httptest.Server.
type Client struct {
	BaseURL string
	Bearer  string
	HTTP    *http.Client
}

// NewClient returns a client with sane defaults.
func NewClient(baseURL, bearer string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Bearer:  bearer,
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

// DeviceCodeResponse mirrors POST /auth/device/code in the v2 backend.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// DeviceCode starts a new device authorization flow.
func (c *Client) DeviceCode() (*DeviceCodeResponse, error) {
	var out DeviceCodeResponse
	if err := c.do("POST", "/auth/device/code", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// APIError is returned for non-2xx responses. Code is the body's "error" field
// if present (matches the v2 envelope), otherwise the HTTP status text.
type APIError struct {
	Status int
	Code   string
	Body   string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("cloud api: %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("cloud api: %d %s", e.Status, http.StatusText(e.Status))
}

// do is the low-level request helper. payload is JSON-encoded if non-nil;
// out is JSON-decoded if non-nil and status is 2xx.
func (c *Client) do(method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.Bearer)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		apiErr := &APIError{Status: resp.StatusCode, Body: string(respBody)}
		var env struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &env) == nil {
			apiErr.Code = env.Error
		}
		return apiErr
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestClientDeviceCode -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cloud/client.go internal/cloud/client_test.go
git commit -m "cloud: add Client skeleton and DeviceCode method"
```

---

### Task A4: Add `DeviceToken` polling method

**Files:**
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/client_test.go`

- [ ] **Step 1: Write the failing test**

Append to `client_test.go`:

```go
func TestClientDeviceTokenSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ultra_pat_xyz"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	token, err := c.DeviceToken("dc_abc")
	assert.NoError(t, err)
	assert.Equal(t, "ultra_pat_xyz", token)
}

func TestClientDeviceTokenPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	_, err := c.DeviceToken("dc_abc")
	var apiErr *APIError
	assert.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "authorization_pending", apiErr.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestClientDeviceToken -v`
Expected: FAIL — `c.DeviceToken undefined`.

- [ ] **Step 3: Implement**

Append to `client.go`:

```go
// DeviceToken polls for completion of an in-flight device authorization
// grant. On success returns the raw PAT. On RFC 8628 polling errors
// (authorization_pending, slow_down, access_denied, expired_token), returns
// an *APIError with Code set — caller inspects to decide whether to keep polling.
func (c *Client) DeviceToken(deviceCode string) (string, error) {
	payload := map[string]string{"device_code": deviceCode}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do("POST", "/auth/device/token", payload, &out); err != nil {
		return "", err
	}
	return out.Token, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestClientDeviceToken -v`
Expected: PASS for both sub-tests.

- [ ] **Step 5: Commit**

```bash
git add internal/cloud/client.go internal/cloud/client_test.go
git commit -m "cloud: add DeviceToken polling method"
```

---

### Task A5: Add project + deploy + migration-preview + generate-yaml + upload-yaml + whoami methods

These all follow the same shape as `DeviceCode`. Group them in one task because each is small.

**Files:**
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/client_test.go`

- [ ] **Step 1: Write the failing test (table-driven)**

Append to `client_test.go`:

```go
func TestClientCreateProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/ultrabase/projects", r.URL.Path)
		assert.Equal(t, "Bearer ultra_pat_test", r.Header.Get("Authorization"))

		var body struct{ Name string `json:"name"` }
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "myapp", body.Name)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"project_id": "app-uuid",
			"slug":       "myapp-abc",
			"name":       "myapp",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	resp, err := c.CreateProject("myapp")
	assert.NoError(t, err)
	assert.Equal(t, "app-uuid", resp.ProjectID)
}

func TestClientDeploy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/ultrabase/projects/app-uuid/deploy", r.URL.Path)

		_ = json.NewEncoder(w).Encode(map[string]any{"version_id": "v-1"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	resp, err := c.Deploy("app-uuid")
	assert.NoError(t, err)
	assert.Equal(t, "v-1", resp.VersionID)
}

func TestClientMigrationPreview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/ultrabase/projects/app-uuid/migration-preview", r.URL.Path)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"diff": "+ added table todos",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	resp, err := c.MigrationPreview("app-uuid")
	assert.NoError(t, err)
	assert.Contains(t, resp.Diff, "todos")
}

func TestClientGenerateYAML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/ai/generate-yaml", r.URL.Path)

		var body struct{ Prompt string `json:"prompt"` }
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "twitter clone", body.Prompt)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"yaml":   "version: 1\nproject:\n  name: t\n",
			"tokens": map[string]int{"input": 100, "output": 200},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	resp, err := c.GenerateYAML("twitter clone")
	assert.NoError(t, err)
	assert.Contains(t, resp.YAML, "version: 1")
	assert.Equal(t, 100, resp.Tokens.Input)
	assert.Equal(t, 200, resp.Tokens.Output)
}

func TestClientUploadYAML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PUT", r.Method)
		assert.Equal(t, "/ultrabase/projects/app-uuid/yaml", r.URL.Path)

		var body struct{ YAML string `json:"yaml"` }
		_ = json.NewDecoder(r.Body).Decode(&body)
		assert.Contains(t, body.YAML, "version: 1")

		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "version_id": "v-2"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	err := c.UploadYAML("app-uuid", "version: 1\n")
	assert.NoError(t, err)
}

func TestClientWhoami(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/ultrabase/whoami", r.URL.Path)
		assert.Equal(t, "Bearer ultra_pat_test", r.Header.Get("Authorization"))

		_ = json.NewEncoder(w).Encode(map[string]any{
			"email":   "me@example.com",
			"user_id": "me@example.com",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "ultra_pat_test")
	resp, err := c.Whoami()
	assert.NoError(t, err)
	assert.Equal(t, "me@example.com", resp.Email)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run "TestClientCreateProject|TestClientDeploy|TestClientMigrationPreview|TestClientGenerateYAML|TestClientUploadYAML|TestClientWhoami" -v`
Expected: FAIL with undefined methods.

- [ ] **Step 3: Implement**

Append to `client.go`:

```go
// CreateProjectResponse mirrors POST /ultrabase/projects.
type CreateProjectResponse struct {
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
}

// CreateProject creates a new backend-only App in Ultrabase Cloud. Requires
// a Bearer PAT.
func (c *Client) CreateProject(name string) (*CreateProjectResponse, error) {
	var out CreateProjectResponse
	if err := c.do("POST", "/ultrabase/projects", map[string]string{"name": name}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeployResponse mirrors POST /ultrabase/projects/:id/deploy. The version_id
// can be polled via GET /data/apps/:id to track status.
type DeployResponse struct {
	VersionID string `json:"version_id"`
	Message   string `json:"message,omitempty"`
}

// Deploy triggers a production deploy for the given project.
func (c *Client) Deploy(projectID string) (*DeployResponse, error) {
	var out DeployResponse
	if err := c.do("POST", "/ultrabase/projects/"+projectID+"/deploy", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MigrationPreviewResponse mirrors GET /ultrabase/projects/:id/migration-preview.
// The exact shape of `diff` depends on v2 — keep it loose so the engineer
// adapts after the v2 plan's GetMigrationPreviewHandler is finalized.
type MigrationPreviewResponse struct {
	Diff string `json:"diff"`
	// Add structured fields here once the v2 response shape stabilizes.
}

// MigrationPreview returns the diff between the current ultrabase.yaml and
// what's deployed to the cloud project.
func (c *Client) MigrationPreview(projectID string) (*MigrationPreviewResponse, error) {
	var out MigrationPreviewResponse
	if err := c.do("GET", "/ultrabase/projects/"+projectID+"/migration-preview", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GenerateYAMLResponse mirrors POST /ai/generate-yaml.
type GenerateYAMLResponse struct {
	YAML   string `json:"yaml"`
	Tokens struct {
		Input  int `json:"input"`
		Output int `json:"output"`
	} `json:"tokens"`
}

// GenerateYAML asks the AI service to produce a starter ultrabase.yaml from
// a free-form prompt (≤ 256 chars).
func (c *Client) GenerateYAML(prompt string) (*GenerateYAMLResponse, error) {
	var out GenerateYAMLResponse
	if err := c.do("POST", "/ai/generate-yaml", map[string]string{"prompt": prompt}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadYAML pushes the local ultrabase.yaml to the project's server-side
// draft Defs. Called by `ultra deploy` and `ultra validate --project` before
// their respective actions so the server sees the latest local source.
func (c *Client) UploadYAML(projectID, yamlContent string) error {
	return c.do("PUT", "/ultrabase/projects/"+projectID+"/yaml", map[string]string{"yaml": yamlContent}, nil)
}

// WhoamiResponse mirrors GET /ultrabase/whoami.
type WhoamiResponse struct {
	Email  string `json:"email"`
	UserID string `json:"user_id"`
}

// Whoami returns the identity of the PAT holder. Useful for `ultra whoami`
// and as a post-login sanity check.
func (c *Client) Whoami() (*WhoamiResponse, error) {
	var out WhoamiResponse
	if err := c.do("GET", "/ultrabase/whoami", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cloud/client.go internal/cloud/client_test.go
git commit -m "cloud: add CreateProject, Deploy, MigrationPreview, GenerateYAML, UploadYAML, Whoami"
```

---

### Task A6: YAML helpers — read/write `project.cloud.{project_id,api_url}`

The cloud project state lives inside `ultrabase.yaml` under `project.cloud`. Two fields matter today:
- `project_id` — set by `ultra init --with-cloud`, read by `ultra deploy` / `validate --project`.
- `api_url` — optional; if present, overrides `ULTRABASE_CLOUD_API` for this project.

We must read and merge these without disturbing the rest of the file (comments, ordering, trailing newlines). Uses `yaml.v3`'s `yaml.Node` round-tripping.

**Files:**
- Create: `internal/cloud/yaml_project_id.go`
- Create: `internal/cloud/yaml_project_id_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cloud

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadProjectID(t *testing.T) {
	src := `version: 1
project:
  name: my app
  cloud:
    project_id: abc-123
tables:
  todos: {}
`
	id, err := ReadProjectID([]byte(src))
	assert.NoError(t, err)
	assert.Equal(t, "abc-123", id)
}

func TestReadProjectIDMissing(t *testing.T) {
	src := `version: 1
project:
  name: my app
`
	id, err := ReadProjectID([]byte(src))
	assert.NoError(t, err)
	assert.Equal(t, "", id)
}

func TestWriteProjectIDNew(t *testing.T) {
	src := `version: 1
project:
  name: my app
tables:
  todos: {}
`
	out, err := WriteProjectID([]byte(src), "abc-123")
	assert.NoError(t, err)
	assert.Contains(t, string(out), "project_id: abc-123")
	// Existing structure is preserved (table todos still there).
	assert.Contains(t, string(out), "todos:")
	// Order preserved: project before tables.
	assert.Less(t, strings.Index(string(out), "project:"), strings.Index(string(out), "tables:"))
}

func TestWriteProjectIDUpdate(t *testing.T) {
	src := `version: 1
project:
  name: my app
  cloud:
    project_id: old-id
`
	out, err := WriteProjectID([]byte(src), "new-id")
	assert.NoError(t, err)
	assert.Contains(t, string(out), "project_id: new-id")
	assert.NotContains(t, string(out), "old-id")
}

func TestReadAPIURL(t *testing.T) {
	src := `version: 1
project:
  cloud:
    api_url: https://staging.cloud.example.com
`
	got, err := ReadAPIURL([]byte(src))
	assert.NoError(t, err)
	assert.Equal(t, "https://staging.cloud.example.com", got)
}

func TestReadAPIURLMissing(t *testing.T) {
	src := `version: 1
project:
  name: my app
`
	got, err := ReadAPIURL([]byte(src))
	assert.NoError(t, err)
	assert.Equal(t, "", got)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run "TestReadProjectID|TestWriteProjectID" -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `internal/cloud/yaml_project_id.go`:

```go
package cloud

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ReadProjectID extracts project.cloud.project_id from a YAML document.
// Returns "" if the field is not present. Never modifies the input.
func ReadProjectID(src []byte) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return "", fmt.Errorf("parse yaml: %w", err)
	}
	if len(root.Content) == 0 {
		return "", nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return "", nil
	}
	proj := findMapValue(doc, "project")
	if proj == nil {
		return "", nil
	}
	cloud := findMapValue(proj, "cloud")
	if cloud == nil {
		return "", nil
	}
	pid := findMapValue(cloud, "project_id")
	if pid == nil || pid.Kind != yaml.ScalarNode {
		return "", nil
	}
	return pid.Value, nil
}

// WriteProjectID sets project.cloud.project_id to the given value. Creates
// the cloud subtree if missing. Returns the rewritten YAML bytes.
//
// Preserves the document's existing structure as much as yaml.v3 supports;
// comment preservation depends on node ordering and may not be perfect.
func WriteProjectID(src []byte, projectID string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if len(root.Content) == 0 {
		return nil, errors.New("empty yaml document")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, errors.New("top-level yaml must be a mapping")
	}

	proj := findMapValue(doc, "project")
	if proj == nil {
		// Insert empty project: {} before continuing.
		proj = &yaml.Node{Kind: yaml.MappingNode}
		appendMapEntry(doc, "project", proj)
	}

	cloud := findMapValue(proj, "cloud")
	if cloud == nil {
		cloud = &yaml.Node{Kind: yaml.MappingNode}
		appendMapEntry(proj, "cloud", cloud)
	}

	pid := findMapValue(cloud, "project_id")
	if pid == nil {
		appendMapEntry(cloud, "project_id", &yaml.Node{Kind: yaml.ScalarNode, Value: projectID, Tag: "!!str"})
	} else {
		pid.Kind = yaml.ScalarNode
		pid.Value = projectID
		pid.Tag = "!!str"
	}

	out, err := yaml.Marshal(&root)
	if err != nil {
		return nil, fmt.Errorf("encode yaml: %w", err)
	}
	return out, nil
}

// findMapValue returns the value node for the given key in a MappingNode,
// or nil if the key is absent.
func findMapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// appendMapEntry adds a key/value pair to a MappingNode.
func appendMapEntry(m *yaml.Node, key string, value *yaml.Node) {
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		value,
	)
}

// ReadAPIURL extracts project.cloud.api_url from a YAML document. Returns
// "" if the field is not present. Used by APIURLFromConfig to allow a
// project to pin its own cloud endpoint (overrides ULTRABASE_CLOUD_API).
func ReadAPIURL(src []byte) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		return "", fmt.Errorf("parse yaml: %w", err)
	}
	if len(root.Content) == 0 {
		return "", nil
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return "", nil
	}
	proj := findMapValue(doc, "project")
	if proj == nil {
		return "", nil
	}
	cloud := findMapValue(proj, "cloud")
	if cloud == nil {
		return "", nil
	}
	url := findMapValue(cloud, "api_url")
	if url == nil || url.Kind != yaml.ScalarNode {
		return "", nil
	}
	return url.Value, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run "TestReadProjectID|TestWriteProjectID" -v`
Expected: PASS for all four sub-tests.

- [ ] **Step 5: Commit**

```bash
git add internal/cloud/yaml_project_id.go internal/cloud/yaml_project_id_test.go
git commit -m "cloud: add ReadProjectID/WriteProjectID YAML helpers"
```

---

### Part A — Verification Checkpoint

```bash
cd /home/saedx1/repos/ultrabase/main
go test -race ./internal/cloud/...
go build ./...
```

Both must pass. PR title: `cloud: scaffold internal/cloud package (credentials + client + yaml helpers)`.

---

# Part B — `ultra login`

Device-flow CLI command. Flow:
1. POST `/auth/device/code` → get `device_code`, `user_code`, `verification_uri`, `interval`.
2. Print "Visit <uri> and enter <user_code>".
3. Best-effort: open browser to `<uri>?code=<user_code>` (pre-filled).
4. Poll `/auth/device/token` every `interval` seconds.
5. On success: write PAT + email to `~/.ultra/credentials`. Print "logged in as <email>".

Error handling:
- `authorization_pending` → keep polling
- `slow_down` → add 5s to current interval
- `access_denied` → exit with "denied"
- `expired_token` → exit with "code expired"
- Network error → 3 retries with backoff, then exit

### Task B1: Implement `openBrowser` helper

**Files:**
- Create: `internal/cloud/browser.go`
- Create: `internal/cloud/browser_test.go`

- [ ] **Step 1: Write the failing test**

```go
package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBrowserCommandForOS(t *testing.T) {
	tests := []struct {
		goos   string
		want   string
	}{
		{"linux", "xdg-open"},
		{"darwin", "open"},
		{"windows", "rundll32"},
		{"freebsd", ""},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			assert.Equal(t, tt.want, browserCommand(tt.goos))
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestBrowserCommand -v`
Expected: FAIL — `browserCommand undefined`.

- [ ] **Step 3: Implement**

Create `internal/cloud/browser.go`:

```go
package cloud

import (
	"errors"
	"os/exec"
	"runtime"
)

// OpenBrowser attempts to open url in the user's default browser. Returns
// an error if no suitable launcher is available on this OS; callers should
// not treat that as fatal — print the URL and let the user open it manually.
func OpenBrowser(url string) error {
	cmd := browserCommand(runtime.GOOS)
	if cmd == "" {
		return errors.New("no browser launcher for this OS")
	}
	args := []string{url}
	if cmd == "rundll32" {
		// Windows: rundll32 url.dll,FileProtocolHandler <url>
		args = []string{"url.dll,FileProtocolHandler", url}
	}
	return exec.Command(cmd, args...).Start()
}

// browserCommand returns the OS-specific browser launcher binary name, or
// "" if unsupported. Extracted so it's unit-testable.
func browserCommand(goos string) string {
	switch goos {
	case "linux":
		return "xdg-open"
	case "darwin":
		return "open"
	case "windows":
		return "rundll32"
	default:
		return ""
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestBrowserCommand -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cloud/browser.go internal/cloud/browser_test.go
git commit -m "cloud: add OpenBrowser helper for device-flow login"
```

---

### Task B2: Implement `pollDeviceToken` helper

The polling loop has enough state-machine logic to deserve its own testable function, separate from the cobra wiring.

**Files:**
- Modify: `internal/cloud/client.go`
- Modify: `internal/cloud/client_test.go`

- [ ] **Step 1: Write the failing test**

Append to `client_test.go`:

```go
import (
	"sync/atomic"
)

func TestPollDeviceTokenSucceedsAfterPending(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch n {
		case 1, 2:
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "ultra_pat_ok"})
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	// Override Sleep to make tests fast.
	token, err := pollDeviceToken(c, "dc_abc", 30*time.Second, 1*time.Millisecond, func(time.Duration) {})
	assert.NoError(t, err)
	assert.Equal(t, "ultra_pat_ok", token)
	assert.Equal(t, int32(3), calls.Load())
}

func TestPollDeviceTokenDenied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "access_denied"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	_, err := pollDeviceToken(c, "dc_abc", 30*time.Second, 1*time.Millisecond, func(time.Duration) {})
	assert.ErrorIs(t, err, ErrDeviceAccessDenied)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestPollDeviceToken -v`
Expected: FAIL — `pollDeviceToken undefined`.

- [ ] **Step 3: Implement**

Append to `client.go`:

```go
// ErrDeviceAccessDenied is returned when the user denies the device flow in
// the browser. Terminal — don't retry.
var ErrDeviceAccessDenied = errors.New("user denied authorization")

// ErrDeviceExpired is returned when the device flow's expires_in window
// passes without confirmation. Terminal.
var ErrDeviceExpired = errors.New("device code expired")

// pollDeviceToken polls /auth/device/token until success, denial, or timeout.
// `sleep` is parameterized for tests (use time.Sleep in production).
func pollDeviceToken(c *Client, deviceCode string, timeout, interval time.Duration, sleep func(time.Duration)) (string, error) {
	deadline := time.Now().Add(timeout)
	curInterval := interval

	for time.Now().Before(deadline) {
		token, err := c.DeviceToken(deviceCode)
		if err == nil {
			return token, nil
		}
		var apiErr *APIError
		if !errorsAs(err, &apiErr) {
			// Network error — back off and retry.
			sleep(curInterval)
			continue
		}
		switch apiErr.Code {
		case "authorization_pending":
			sleep(curInterval)
		case "slow_down":
			curInterval += 5 * time.Second
			sleep(curInterval)
		case "access_denied":
			return "", ErrDeviceAccessDenied
		case "expired_token":
			return "", ErrDeviceExpired
		default:
			return "", err
		}
	}
	return "", ErrDeviceExpired
}

// errorsAs is a thin shim so tests can run without pulling in errors.As
// repeatedly. Equivalent to errors.As(err, &target).
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}
```

Add `"errors"` and `"time"` to the import block (likely already present).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cloud/ -run TestPollDeviceToken -v`
Expected: PASS for both.

- [ ] **Step 5: Commit**

```bash
git add internal/cloud/client.go internal/cloud/client_test.go
git commit -m "cloud: add pollDeviceToken with RFC 8628 error handling"
```

---

### Task B3: Implement `ultra login` cobra command

**Files:**
- Create: `internal/cli/login.go`
- Create: `internal/cli/login_test.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Write the failing test (smoke-test the cobra wiring)**

```go
package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewLoginCmdHasUseAndShort(t *testing.T) {
	cmd := newLoginCmd()
	assert.Equal(t, "login", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestNewLoginCmd -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement the command**

Create `internal/cli/login.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/saedx1/ultrabase/internal/cloud"
	"github.com/spf13/cobra"
)

func newLoginCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate against Ultrabase Cloud via device-code flow",
		Long: `Sign in to Ultrabase Cloud. Opens a browser to confirm a one-time
code, then stores a Personal Access Token at ~/.ultra/credentials for
subsequent commands.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogin(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "re-authenticate even if already logged in")
	return cmd
}

func runLogin(force bool) error {
	// Short-circuit if already logged in.
	if !force {
		if existing, err := cloud.Load(); err == nil && existing.PAT != "" {
			who := existing.Email
			if who == "" {
				who = "(unknown email)"
			}
			fmt.Printf("Already logged in as %s. Use --force to re-authenticate.\n", who)
			return nil
		}
	}

	c := cloud.NewClient(cloud.APIURL(), "")
	dc, err := c.DeviceCode()
	if err != nil {
		return fmt.Errorf("requesting device code: %w", err)
	}

	verifyURL := fmt.Sprintf("%s?code=%s", dc.VerificationURI, dc.UserCode)
	fmt.Printf("\n  Visit: %s\n  Code:  %s\n\n", dc.VerificationURI, dc.UserCode)

	if err := cloud.OpenBrowser(verifyURL); err != nil {
		fmt.Println("  (couldn't open browser automatically — copy the URL above)")
	}

	fmt.Println("  Waiting for confirmation...")

	timeout := time.Duration(dc.ExpiresIn) * time.Second
	interval := time.Duration(dc.Interval) * time.Second
	token, err := pollDeviceTokenWithSleep(c, dc.DeviceCode, timeout, interval)
	if err != nil {
		switch {
		case errors.Is(err, cloud.ErrDeviceAccessDenied):
			return errors.New("authorization denied")
		case errors.Is(err, cloud.ErrDeviceExpired):
			return errors.New("code expired before confirmation; run `ultra login` again")
		default:
			return fmt.Errorf("polling for token: %w", err)
		}
	}

	creds := cloud.Credentials{PAT: token}
	// Try to fetch the user's email for nicer messages. The v2 plan
	// includes GET /auth/user (cookie-auth) — for PAT-auth we'd need an
	// equivalent endpoint. If absent, skip the email lookup.
	// TODO(v2): add GET /auth/user Bearer-supported endpoint.
	if err := cloud.Save(creds); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}

	fmt.Println("  ✓ Logged in successfully.")
	return nil
}

// pollDeviceTokenWithSleep wraps cloud.pollDeviceToken with real time.Sleep,
// keeping the CLI code free of test-only injection.
func pollDeviceTokenWithSleep(c *cloud.Client, code string, timeout, interval time.Duration) (string, error) {
	// pollDeviceToken is unexported in cloud; we expose a wrapper there.
	// See Task B4 — if not yet exported, this won't compile.
	return cloud.PollDeviceToken(c, code, timeout, interval)
}
```

- [ ] **Step 4: Export `PollDeviceToken` from `cloud`**

The internal `pollDeviceToken` from Task B2 is package-private. CLI needs to call it. Edit `internal/cloud/client.go` and add a public wrapper:

```go
// PollDeviceToken is the exported wrapper. Uses time.Sleep for waits.
func PollDeviceToken(c *Client, deviceCode string, timeout, interval time.Duration) (string, error) {
	return pollDeviceToken(c, deviceCode, timeout, interval, time.Sleep)
}
```

- [ ] **Step 5: Register the command**

Edit `internal/cli/root.go`:

```go
root.AddCommand(
	newInitCmd(),
	newValidateCmd(),
	newDevCmd(),
	newServeCmd(),
	newSlotCmd(),
	newVersionCmd(),
	newLoginCmd(),  // new
)
```

- [ ] **Step 6: Run tests**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestNewLoginCmd -v && go build ./...`
Expected: PASS + clean build.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/login.go internal/cli/login_test.go internal/cli/root.go internal/cloud/client.go
git commit -m "cli: add `ultra login` device-code flow"
```

---

### Part B — Verification Checkpoint

```bash
cd /home/saedx1/repos/ultrabase/main
go test -race ./internal/...
go build ./...
```

Manual smoke (against a running v2 stack at `http://localhost:80`):

```bash
ULTRABASE_CLOUD_API=http://localhost ./ultra login
```

Should open the browser to `<v2>/device?code=XXXX-XXXX`. After approval in the dashboard, the CLI should print "Logged in successfully."

PR title: `cli: add ultra login (device-code flow)`.

---

# Part C — `ultra logout`

Simple: delete `~/.ultra/credentials`. The PAT remains valid server-side until the user revokes it from the dashboard — document this. (A follow-up adds a v2 endpoint to revoke-by-Bearer.)

### Task C1: Implement `ultra logout`

**Files:**
- Create: `internal/cli/logout.go`
- Create: `internal/cli/logout_test.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Write the failing test**

```go
package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewLogoutCmd(t *testing.T) {
	cmd := newLogoutCmd()
	assert.Equal(t, "logout", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestNewLogoutCmd -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `internal/cli/logout.go`:

```go
package cli

import (
	"fmt"

	"github.com/saedx1/ultrabase/internal/cloud"
	"github.com/spf13/cobra"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Forget local Ultrabase Cloud credentials",
		Long: `Remove the PAT stored at ~/.ultra/credentials. The token itself
remains valid server-side until you revoke it from the dashboard.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cloud.Delete(); err != nil {
				return fmt.Errorf("removing credentials: %w", err)
			}
			fmt.Println("  ✓ Logged out.")
			return nil
		},
	}
}
```

- [ ] **Step 4: Register**

Add to `internal/cli/root.go`'s `root.AddCommand(...)`:

```go
newLogoutCmd(),
```

- [ ] **Step 5: Run tests + build**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestNewLogoutCmd -v && go build ./...`
Expected: PASS + clean.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/logout.go internal/cli/logout_test.go internal/cli/root.go
git commit -m "cli: add `ultra logout`"
```

---

# Part D — `ultra init --with-cloud` and `--generate-like`

Two new flags on the existing `init` command. They compose freely with `--with-dsn` and `--with-docker`:

| Flag | Effect |
|---|---|
| `--with-cloud` | Creates a cloud project; writes `project.cloud.project_id` into `ultrabase.yaml`. Requires login. |
| `--generate-like "<prompt>"` | Instead of the static scaffold YAML, generate one from the prompt via `/ai/generate-yaml`. Requires login. |

If both are set: generate the YAML first (so the project name can be inferred from the generated config or the positional arg), then create the cloud project, then write project_id into the generated YAML.

### Task D1: Wire the flags + mutual-exclusion

**Files:**
- Modify: `internal/cli/init.go` (the `initOptions` struct + `newInitCmd()`)
- Modify: `internal/cli/init_test.go`

- [ ] **Step 1: Write the failing test**

Append to `init_test.go`:

```go
func TestInitFlagsCloudAndGenerateLike(t *testing.T) {
	cmd := newInitCmd()
	assert.NotNil(t, cmd.Flags().Lookup("with-cloud"))
	assert.NotNil(t, cmd.Flags().Lookup("generate-like"))
}

func TestInitWithDockerAndCloudMutuallyExclusive(t *testing.T) {
	// --with-cloud and --with-docker are NOT mutually exclusive — they're
	// orthogonal axes (cloud target vs local dev DB).
	// This test guards against accidentally adding such a constraint.
	opts := initOptions{useDock: true, withCloud: true}
	err := validateInitFlags(opts)
	assert.NoError(t, err)
}

func TestInitWithDSNAndDockerStillMutuallyExclusive(t *testing.T) {
	opts := initOptions{withDSN: "x", useDock: true}
	err := validateInitFlags(opts)
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestInitFlags -v`
Expected: FAIL — `withCloud undefined` etc.

- [ ] **Step 3: Add fields + flags + validator**

Edit `internal/cli/init.go`. Update `initOptions`:

```go
type initOptions struct {
	name         string
	dir          string
	withDSN      string
	useDock      bool
	withCloud    bool
	generateLike string
	force        bool
}
```

In `newInitCmd()`, after the existing `cmd.Flags()` calls, add:

```go
cmd.Flags().BoolVar(&opts.withCloud, "with-cloud", false, "create a project in Ultrabase Cloud (requires `ultra login`)")
cmd.Flags().StringVar(&opts.generateLike, "generate-like", "", "generate ultrabase.yaml from a free-form prompt (requires `ultra login`)")
```

Extract the existing mutual-exclusion check into a named function. Replace the `if opts.withDSN != "" && opts.useDock {…}` block in `runInit` with:

```go
if err := validateInitFlags(opts); err != nil {
	return err
}
```

Add `validateInitFlags` at the bottom of `init.go`:

```go
// validateInitFlags enforces mutual exclusions between init's flags.
// --with-dsn and --with-docker are mutually exclusive (both are local dev
// DB sources). --with-cloud is orthogonal — it specifies the cloud target.
// --generate-like is also orthogonal (it shapes the scaffold YAML).
func validateInitFlags(opts initOptions) error {
	if opts.withDSN != "" && opts.useDock {
		return errors.New("--with-dsn and --with-docker are mutually exclusive")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestInitFlags -v`
Expected: PASS for all.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "cli init: add --with-cloud and --generate-like flags"
```

---

### Task D2: Implement `--generate-like` behavior

**Files:**
- Modify: `internal/cli/init.go`
- Modify: `internal/cli/init_test.go`

- [ ] **Step 1: Write the failing test**

Append to `init_test.go`:

```go
func TestInitGenerateLikeRequiresLogin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir) // no credentials in this HOME

	opts := initOptions{dir: dir, generateLike: "twitter"}
	err := runInit(context.Background(), opts)
	assert.ErrorContains(t, err, "ultra login")
}
```

(Adapt `runInit` signature if currently `ctx context.Context, opts initOptions`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestInitGenerateLikeRequiresLogin -v`
Expected: FAIL (depending on existing behavior — might already fail-open).

- [ ] **Step 3: Add credential check + generate flow in `runInit`**

Edit `internal/cli/init.go`. After `validateInitFlags`, add:

```go
// Cloud-dependent flags require credentials.
if opts.withCloud || opts.generateLike != "" {
	if _, err := cloud.Load(); err != nil {
		return fmt.Errorf("--with-cloud / --generate-like require authentication: %w", err)
	}
}
```

Add `"github.com/saedx1/ultrabase/internal/cloud"` to the import block.

After the existing `ultrabase.yaml` existence gate, add (before the file write):

```go
var generatedYAML string
if opts.generateLike != "" {
	fmt.Println("  Generating ultrabase.yaml from prompt...")
	creds, _ := cloud.Load()
	c := cloud.NewClient(cloud.APIURL(), creds.PAT)
	resp, err := c.GenerateYAML(opts.generateLike)
	if err != nil {
		return fmt.Errorf("generate-yaml: %w", err)
	}
	generatedYAML = resp.YAML
	fmt.Printf("  ✓ Generated (%d input + %d output tokens)\n", resp.Tokens.Input, resp.Tokens.Output)
}
```

Then change the `applyWrite` call for `ultrabase.yaml` to use the generated content when present:

```go
if err := applyWrite(dir, "ultrabase.yaml", func(_ string) (string, writeAction) {
	if generatedYAML != "" {
		return generatedYAML, actionCreate
	}
	return scaffoldYAML(name), actionCreate
}); err != nil {
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestInitGenerateLikeRequiresLogin -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "cli init: --generate-like fetches YAML from /ai/generate-yaml"
```

---

### Task D3: Implement `--with-cloud` behavior

**Files:**
- Modify: `internal/cli/init.go`
- Modify: `internal/cli/init_test.go`

- [ ] **Step 1: Write the failing test**

Append to `init_test.go`:

```go
func TestInitWithCloudRequiresLogin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	opts := initOptions{dir: dir, withCloud: true, name: "myapp"}
	err := runInit(context.Background(), opts)
	assert.ErrorContains(t, err, "ultra login")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestInitWithCloudRequiresLogin -v`
Expected: PASS already (Task D2 added the credential check). If FAIL, debug — likely a missing path through the credential check.

- [ ] **Step 3: Add the cloud-project create + project_id write**

After the YAML file is written (the `applyWrite("ultrabase.yaml", …)` call), add:

```go
if opts.withCloud {
	fmt.Println("  Creating Ultrabase Cloud project...")
	creds, _ := cloud.Load()
	c := cloud.NewClient(cloud.APIURL(), creds.PAT)
	resp, err := c.CreateProject(name)
	if err != nil {
		return fmt.Errorf("creating cloud project: %w", err)
	}
	fmt.Printf("  ✓ Project created (id: %s)\n", resp.ProjectID)

	yamlPath := filepath.Join(dir, "ultrabase.yaml")
	existing, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("re-reading ultrabase.yaml: %w", err)
	}
	updated, err := cloud.WriteProjectID(existing, resp.ProjectID)
	if err != nil {
		return fmt.Errorf("injecting project_id: %w", err)
	}
	if err := os.WriteFile(yamlPath, updated, 0o644); err != nil {
		return fmt.Errorf("writing ultrabase.yaml: %w", err)
	}
	fmt.Println("  ~ ultrabase.yaml (added project.cloud.project_id)")
}
```

- [ ] **Step 4: Update the "next steps" hint**

Find the existing `fmt.Println("Done! Next steps:")` block. Add a branch for `opts.withCloud`:

```go
switch {
case opts.withCloud:
	fmt.Println("  ultra deploy            # push your YAML to the cloud project")
case opts.withDSN != "":
	fmt.Println("  ultra dev --use-dsn")
case opts.useDock:
	fmt.Println("  ultra dev --use-docker")
default:
	fmt.Println("  # Configure a data source, then:")
	fmt.Println("  ultra dev --use-dsn        # point at your own Postgres")
}
```

- [ ] **Step 5: Run all init tests + build**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -race && go build ./...`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "cli init: --with-cloud creates project and writes project_id"
```

---

### Part D — Verification Checkpoint

```bash
cd /home/saedx1/repos/ultrabase/main
go test -race ./internal/...
go build ./...
```

Manual smoke (against a v2 stack):

```bash
ULTRABASE_CLOUD_API=http://localhost ./ultra login
mkdir /tmp/myapp && cd /tmp/myapp
ULTRABASE_CLOUD_API=http://localhost /path/to/ultra init --with-cloud --generate-like "todo list"
grep "project_id" ultrabase.yaml
```

Should print a YAML with `project.cloud.project_id` set. PR title: `cli init: --with-cloud and --generate-like flags`.

---

# Part E — `ultra deploy`

Reads `project.cloud.project_id` from `ultrabase.yaml`, POSTs to `/ultrabase/projects/:id/deploy`, prints the version_id. Polling for deploy completion is **deferred** — just kick it off and tell the user to check the dashboard.

### Task E1: Implement `newDeployCmd`

**Files:**
- Create: `internal/cli/deploy.go`
- Create: `internal/cli/deploy_test.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Write the failing test**

```go
package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewDeployCmd(t *testing.T) {
	cmd := newDeployCmd()
	assert.Equal(t, "deploy", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestNewDeployCmd -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `internal/cli/deploy.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/saedx1/ultrabase/internal/cloud"
	"github.com/spf13/cobra"
)

func newDeployCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Push the current ultrabase.yaml to an Ultrabase Cloud project",
		Long: `Deploy the current project's ultrabase.yaml to the cloud. The
project_id is read from project.cloud.project_id inside ultrabase.yaml. Run
ultra init --with-cloud first if no project is set yet.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "path to ultrabase.yaml")
	return cmd
}

func runDeploy(configPath string) error {
	if err := requireConfigFile(configPath); err != nil {
		return err
	}

	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("--with-cloud requires authentication: %w", err)
	}

	src, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	projectID, err := cloud.ReadProjectID(src)
	if err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if projectID == "" {
		return errors.New("no project.cloud.project_id in ultrabase.yaml; run `ultra init --with-cloud` first")
	}

	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		return err
	}
	c := cloud.NewClient(apiURL, creds.PAT)

	// Push the local YAML to the project's draft Defs before deploying,
	// so the server sees exactly what's on disk.
	fmt.Println("  Uploading ultrabase.yaml...")
	if err := c.UploadYAML(projectID, string(src)); err != nil {
		return fmt.Errorf("upload yaml: %w", err)
	}

	fmt.Println("  Deploying...")
	resp, err := c.Deploy(projectID)
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}

	fmt.Printf("  ✓ Deploy queued (version_id: %s)\n", resp.VersionID)
	fmt.Println("  Track progress in the Ultrabase Cloud dashboard.")
	return nil
}
```

- [ ] **Step 4: Register**

Edit `internal/cli/root.go` and add `newDeployCmd(),` to the `AddCommand` list.

- [ ] **Step 5: Run tests + build**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -race && go build ./...`
Expected: PASS + clean.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/deploy.go internal/cli/deploy_test.go internal/cli/root.go
git commit -m "cli: add `ultra deploy`"
```

---

# Part F — `ultra validate --project`

Add a `--project` flag mode to the existing validate command. When set, it calls the cloud `/ultrabase/projects/:id/migration-preview` endpoint instead of doing a local DSN-based plan.

### Task F1: Add `--project` flag

**Files:**
- Modify: `internal/cli/validate.go`
- Modify: `internal/cli/validate_test.go`

- [ ] **Step 1: Write the failing test**

Append to `validate_test.go` (create if absent):

```go
func TestValidateHasProjectFlag(t *testing.T) {
	cmd := newValidateCmd()
	assert.NotNil(t, cmd.Flags().Lookup("project"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestValidateHasProjectFlag -v`
Expected: FAIL — flag not present.

- [ ] **Step 3: Add the flag**

In `internal/cli/validate.go`'s `newValidateCmd()`, after the existing `--use-dsn` flag declaration, add:

```go
var useProject bool
cmd.Flags().BoolVar(&useProject, "project", false, "preview migration against the cloud project from ultrabase.yaml")
```

In the `RunE` closure, dispatch on `useProject`:

```go
if useProject {
	return planAgainstProject(cmd.Context(), configPath, jsonOutput)
}
// existing useDSN dispatch below
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestValidateHasProjectFlag -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/validate.go internal/cli/validate_test.go
git commit -m "cli validate: add --project flag"
```

---

### Task F2: Implement `planAgainstProject`

**Files:**
- Modify: `internal/cli/validate.go`

- [ ] **Step 1: Write the failing test**

Append to `validate_test.go`:

```go
func TestPlanAgainstProjectRequiresCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// Write an ultrabase.yaml with a project_id.
	yamlPath := filepath.Join(dir, "ultrabase.yaml")
	require.NoError(t, os.WriteFile(yamlPath, []byte("version: 1\nproject:\n  cloud:\n    project_id: abc\n"), 0o644))

	err := planAgainstProject(context.Background(), yamlPath, false)
	assert.ErrorContains(t, err, "ultra login")
}
```

(Adapt imports as needed: `context`, `filepath`, `os`, `require`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestPlanAgainstProjectRequiresCredentials -v`
Expected: FAIL — `planAgainstProject undefined`.

- [ ] **Step 3: Implement**

Append to `validate.go`:

```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/saedx1/ultrabase/internal/cloud"
)

func planAgainstProject(ctx context.Context, configPath string, jsonOutput bool) error {
	if err := requireConfigFile(configPath); err != nil {
		return err
	}
	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("--project requires authentication: %w", err)
	}

	src, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", configPath, err)
	}
	projectID, err := cloud.ReadProjectID(src)
	if err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if projectID == "" {
		return errors.New("no project.cloud.project_id in ultrabase.yaml; run `ultra init --with-cloud` first")
	}

	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		return err
	}
	c := cloud.NewClient(apiURL, creds.PAT)

	// Push the local YAML to the project's draft so the diff reflects
	// what's actually on disk, not whatever stale draft was uploaded last.
	if err := c.UploadYAML(projectID, string(src)); err != nil {
		return fmt.Errorf("upload yaml: %w", err)
	}

	resp, err := c.MigrationPreview(projectID)
	if err != nil {
		return fmt.Errorf("migration preview: %w", err)
	}

	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(resp)
	}
	if resp.Diff == "" {
		fmt.Println("  ✓ No pending changes.")
		return nil
	}
	fmt.Println(resp.Diff)
	return nil
}
```

- [ ] **Step 4: Run tests + build**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -race && go build ./...`
Expected: PASS + clean.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/validate.go internal/cli/validate_test.go
git commit -m "cli validate: implement --project using /ultrabase migration-preview"
```

---

# Part G — `ultra whoami`

Tiny command. Reads credentials, calls `GET /ultrabase/whoami`, prints email. Useful as a "did login work?" sanity check and for scripts that need to know the active user.

### Task G1: Implement `ultra whoami`

**Files:**
- Create: `internal/cli/whoami.go`
- Create: `internal/cli/whoami_test.go`
- Modify: `internal/cli/root.go`

- [ ] **Step 1: Write the failing test**

```go
package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewWhoamiCmd(t *testing.T) {
	cmd := newWhoamiCmd()
	assert.Equal(t, "whoami", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestNewWhoamiCmd -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement**

Create `internal/cli/whoami.go`:

```go
package cli

import (
	"fmt"

	"github.com/saedx1/ultrabase/internal/cloud"
	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	var configPath string

	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Print the currently logged-in Ultrabase Cloud user",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWhoami(configPath)
		},
	}
	// configPath is optional: whoami works outside a project too. When
	// provided (and the file exists), we honor project.cloud.api_url.
	cmd.Flags().StringVar(&configPath, "config", "ultrabase.yaml", "path to ultrabase.yaml (used to honor project.cloud.api_url; ignored if missing)")
	return cmd
}

func runWhoami(configPath string) error {
	creds, err := cloud.Load()
	if err != nil {
		return fmt.Errorf("not logged in: %w", err)
	}

	// Project-pinned api_url wins if present, else env, else default.
	apiURL, err := cloud.APIURLFromConfig(configPath)
	if err != nil {
		// Bad yaml shouldn't break whoami. Fall back to env/default.
		apiURL = cloud.APIURL()
	}

	c := cloud.NewClient(apiURL, creds.PAT)
	resp, err := c.Whoami()
	if err != nil {
		return fmt.Errorf("whoami: %w", err)
	}
	fmt.Println(resp.Email)
	return nil
}
```

- [ ] **Step 4: Register the command**

Edit `internal/cli/root.go` and add `newWhoamiCmd(),` to the `AddCommand` list:

```go
root.AddCommand(
	newInitCmd(),
	newValidateCmd(),
	newDevCmd(),
	newServeCmd(),
	newSlotCmd(),
	newVersionCmd(),
	newLoginCmd(),
	newLogoutCmd(),
	newDeployCmd(),
	newWhoamiCmd(), // new
)
```

- [ ] **Step 5: Run tests + build**

Run: `cd /home/saedx1/repos/ultrabase/main && go test ./internal/cli/ -run TestNewWhoamiCmd -v && go build ./...`
Expected: PASS + clean.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/whoami.go internal/cli/whoami_test.go internal/cli/root.go
git commit -m "cli: add `ultra whoami`"
```

---

# Cross-Part Verification

After all seven parts land, run the full ultrabase test suite:

```bash
cd /home/saedx1/repos/ultrabase/main
go build ./...
go test -race ./...
go test -tags=integration -race ./...   # needs Docker
cd dashboard && npm test && cd ..
```

End-to-end smoke (assumes v2 backend running locally on port 80, dashboard at the same origin):

```bash
# Start fresh
rm -rf ~/.ultra
mkdir /tmp/smoketest && cd /tmp/smoketest

# 1. Login
ULTRABASE_CLOUD_API=http://localhost ultra login

# 2. Confirm identity
ULTRABASE_CLOUD_API=http://localhost ultra whoami   # → me@example.com

# 3. Init with cloud + AI-generated YAML
ULTRABASE_CLOUD_API=http://localhost ultra init --with-cloud --generate-like "todo app"

# 4. Validate against cloud (should show diff since nothing deployed yet)
ULTRABASE_CLOUD_API=http://localhost ultra validate --project

# 5. Deploy
ULTRABASE_CLOUD_API=http://localhost ultra deploy

# 6. Validate again (should now be in sync — or close to it)
ULTRABASE_CLOUD_API=http://localhost ultra validate --project

# 7. Logout
ultra logout
ls -la ~/.ultra/credentials  # → file not found
```

All seven commands should run cleanly. PR title for the umbrella branch: `cli: Ultrabase Cloud commands (Phase 2)`.

---

## Follow-ups (Out of Scope)

These are intentionally deferred:

- **Server-side PAT revocation on logout** — currently `logout` only deletes the local file. Add a `DELETE /auth/pats/self` Bearer-auth endpoint to v2 and have `logout` call it.
- **Deploy status polling** — `ultra deploy` returns the version_id and exits; doesn't tail status. Add a `--wait` flag + `GET /ultrabase/projects/:id/versions/:version_id` endpoint to v2.
- **`ultra dev --use-cloud`** — develop locally against a cloud-provisioned DSN. Requires v2 to return a DSN at project-creation time (also deferred there).
- **`ultra logout --all`** — revoke every PAT, not just this one. Requires the dashboard's existing PAT revoke endpoint to be callable via Bearer or a new "revoke all mine" endpoint.
- **Browser confirmation page polish** — the `/device` confirmation page lives in the dashboard and is part of the v2 work, but UX (showing the requesting client name, geo, etc.) is a follow-up.
- **Token-balance pre-check on `--generate-like`** — call `/internal/token-balance/check` first so users see "insufficient tokens" before the LLM spins up.
- **CI auth via `ULTRABASE_PAT` env var** — currently the CLI only reads from `~/.ultra/credentials`. For CI, support reading the PAT from an env var so users don't need to write a file.
