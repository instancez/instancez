# Custom Code Functions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add JavaScript HTTP-handler functions served at `/functions/v1/<name>`, executed by a pool of Node worker processes (each a tiny HTTP server over a Unix socket), wire-compatible with `supabase.functions.invoke()`.

**Architecture:** ultra forwards each call as a plain HTTP request over a Unix domain socket to a Node worker (built-in `http`, no framework) that imported the function once; the worker returns the handler's HTTP response verbatim. Data access is loopback HTTP to ultra's own `/rest/v1` (RLS as caller) via an injected supabase-js client whose per-request credentials ride in an `X-Ultra-Context` header. Code + vendored `node_modules` ship as a tarball built by `ultra deploy` and consumed (never built) by `serve`; `dev` builds on the fly. Function `env:` secrets resolve from an in-memory `ULTRA_ENV_` map at invoke time. Workers run with a scrubbed environment.

**Tech Stack:** Go (gin HTTP, pgx), Node ≥22 (built-in `http`, `AsyncLocalStorage`, vendored `@supabase/supabase-js`), `go:embed` for the worker shim, existing `config.Source`/S3 adapter for shipping.

**Commit policy:** Work proceeds on the existing `design/custom-code-functions` branch with one commit **per task** (TDD rhythm). At the very end (Task 15) the branch is **squash-merged into a single commit** so the whole feature is one revertable unit, per the user's request. The per-task commits are intermediate and disappear in the squash.

**Feedback loop (per repo CLAUDE.md — must be green before each commit):** `go build ./...`, `go test -race ./...`, and for any package touched, `go test -tags=integration -race ./<pkg>/...` (needs Docker; the supabase-js suite also needs `node`+`npm`). `npm test` in `dashboard/` only if dashboard files change (none planned here).

**Design source of truth:** `docs/superpowers/specs/2026-06-06-custom-code-functions-design.md` and `…-examples.md`.

**Key new Go identifiers (use these exact names across tasks):**
- `domain.Config.RPC map[string]Function` — renamed from `Config.Functions` (the Postgres-RPC block). Types `Function`/`FuncArg`/`FuncReturn` keep their names.
- `domain.Config.Functions map[string]CodeFunction` — the NEW code-function block.
- `domain.CodeFunction struct { Runtime, File string; AuthRequired bool; Timeout string; Env map[string]string }`.
- `domain.FunctionRuntime` — port interface (Invoke/Reload/Close).
- `internal/adapter/funcs` — package implementing `FunctionRuntime` (pool, worker, UDS client, embedded `worker.js`).
- `internal/adapter/http/functions_handler.go` — the `/functions/v1/:name` gin handler.

---

## Task 1: Rename the Postgres-RPC block `functions:` → `rpc:` and add the empty `CodeFunction` type

Mechanical, done first so the rest compiles against the new names. The wire route `/rest/v1/rpc/<name>` is unchanged.

**Files:**
- Modify: `internal/domain/schema.go` (Config struct; add `CodeFunction`)
- Modify: `internal/config/validate.go:181` (`validateFunctions` call site)
- Modify: `internal/app/migrate.go:130,210` and `internal/app/migrate_config_diff.go:189-190`
- Modify: `internal/adapter/http/openapi.go:90`, `internal/adapter/http/rpc_handler.go:32`
- Modify: `ultrabase.yaml`, `docs/example-ultrabase.yaml`, `docs/examples/react-catalog/ultrabase.yaml`
- Test: existing `internal/app/migrate_test.go`, `internal/config/validate_test.go`, etc. (update references)

- [ ] **Step 1: Rename the field and add the new type in `schema.go`**

In `internal/domain/schema.go`, change the `Config` struct line:
```go
	RPC        map[string]Function     `yaml:"rpc" json:"rpc"`
	Functions  map[string]CodeFunction `yaml:"functions" json:"functions"`
```
(Replace the single old `Functions map[string]Function` line with these two.) Then add, near the `Function` type:
```go
// CodeFunction is a user-declared HTTP handler written in JS, served at
// /functions/v1/<name>. Distinct from Function (the Postgres-RPC block, now
// under `rpc:`).
type CodeFunction struct {
	Runtime      string            `yaml:"runtime" json:"runtime"`           // "node" (v1)
	File         string            `yaml:"file" json:"file"`                 // path relative to config root
	AuthRequired bool              `yaml:"auth_required" json:"auth_required"`
	Timeout      string            `yaml:"timeout" json:"timeout"`           // e.g. "30s"; default applied at runtime
	Env          map[string]string `yaml:"env" json:"env"`                   // name -> literal or ${ULTRA_ENV_*}
}
```

- [ ] **Step 2: Update every `cfg.Functions` reference that meant RPC to `cfg.RPC`**

Change these to `.RPC` (they all operate on Postgres functions): `internal/config/loader.go:238,249`, `internal/config/validate.go:181`, `internal/app/migrate.go:130-133,210-213`, `internal/app/migrate_config_diff.go:189-190`, `internal/adapter/http/openapi.go:90`, `internal/adapter/http/rpc_handler.go:32`. Example for `rpc_handler.go:32`:
```go
		fn, ok := h.cfg.RPC[name]
```

- [ ] **Step 3: Build to find all remaining references**

Run: `go build ./... 2>&1 | head -40`
Expected: compile errors ONLY in `_test.go` files still using `.Functions` for RPC; fix each to `.RPC`. Re-run until `go build ./...` is clean.

- [ ] **Step 4: Update the YAML examples (RPC blocks move to `rpc:`)**

In `ultrabase.yaml`, `docs/example-ultrabase.yaml`, and `docs/examples/react-catalog/ultrabase.yaml`, rename the top-level `functions:` key (the one containing `language:`/`body:`/`returns:`) to `rpc:`.

- [ ] **Step 5: Run the full unit suite**

Run: `go test -race ./...`
Expected: PASS (the rename is behavior-preserving for RPC).

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor: rename pg-function block functions: -> rpc:, add CodeFunction type"
```

---

## Task 2: Validate `functions:` (CodeFunction) and reject the old RPC shape

**Files:**
- Modify: `internal/config/validate.go` (add `validateCodeFunctions`, call it from `Validate`)
- Test: `internal/config/validate_test.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/config/validate_test.go`:
```go
func TestValidateCodeFunctions(t *testing.T) {
	good := &domain.Config{Functions: map[string]domain.CodeFunction{
		"send-welcome": {Runtime: "node", File: "functions/send-welcome.js", Timeout: "30s"},
	}}
	if errs := config.Validate(good); errs != nil {
		t.Fatalf("expected valid, got %v", errs)
	}

	badRuntime := &domain.Config{Functions: map[string]domain.CodeFunction{
		"x": {Runtime: "ruby", File: "functions/x.js"},
	}}
	if errs := config.Validate(badRuntime); errs == nil {
		t.Fatal("expected error for unknown runtime")
	}

	missingFile := &domain.Config{Functions: map[string]domain.CodeFunction{
		"x": {Runtime: "node"},
	}}
	if errs := config.Validate(missingFile); errs == nil {
		t.Fatal("expected error for missing file")
	}

	badName := &domain.Config{Functions: map[string]domain.CodeFunction{
		"bad name!": {Runtime: "node", File: "functions/x.js"},
	}}
	if errs := config.Validate(badName); errs == nil {
		t.Fatal("expected error for invalid path-segment name")
	}

	badTimeout := &domain.Config{Functions: map[string]domain.CodeFunction{
		"x": {Runtime: "node", File: "functions/x.js", Timeout: "soon"},
	}}
	if errs := config.Validate(badTimeout); errs == nil {
		t.Fatal("expected error for unparseable timeout")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/config/ -run TestValidateCodeFunctions -v`
Expected: FAIL (no validation yet → `good` passes but the bad cases return nil).

- [ ] **Step 3: Implement `validateCodeFunctions`**

In `internal/config/validate.go`, after the `validateFunctions` call in `Validate` (line ~181) add:
```go
	errs = append(errs, validateCodeFunctions(cfg.Functions)...)
```
And add the function:
```go
var codeFnNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func validateCodeFunctions(fns map[string]domain.CodeFunction) domain.ValidationErrors {
	var errs domain.ValidationErrors
	for name, fn := range fns {
		path := fmt.Sprintf("functions.%s", name)
		if !codeFnNameRe.MatchString(name) {
			errs = append(errs, domain.ValidationError{Path: path, Message: "name must be a URL path segment ([A-Za-z0-9_-], not starting with - )"})
		}
		if fn.Runtime != "node" {
			errs = append(errs, domain.ValidationError{Path: path + ".runtime", Message: `must be "node" (v1)`})
		}
		if fn.File == "" {
			errs = append(errs, domain.ValidationError{Path: path + ".file", Message: "is required"})
		}
		if fn.Timeout != "" {
			if _, err := time.ParseDuration(fn.Timeout); err != nil {
				errs = append(errs, domain.ValidationError{Path: path + ".timeout", Message: "not a duration (e.g. 30s)"})
			}
		}
	}
	return errs
}
```
Add `"regexp"` and `"time"` to the imports if missing. (Match the actual `domain.ValidationError` field names — verify against an existing use in `validate.go`.)

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/config/ -run TestValidateCodeFunctions -v`
Expected: PASS.

- [ ] **Step 5: Reject the old RPC shape mistakenly left under `functions:`**

Add test:
```go
func TestRejectOldRPCShapeUnderFunctions(t *testing.T) {
	raw := []byte("version: 1\nfunctions:\n  legacy:\n    language: sql\n    body: \"SELECT 1\"\n    returns:\n      type: void\n")
	cfg, err := config.ParseBytes(raw, "test")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := config.Validate(cfg); errs == nil {
		t.Fatal("expected error directing user to rpc:")
	}
}
```
Run it (FAIL), then in `validateCodeFunctions` add, per function, a heuristic on the *raw* presence of RPC-only keys. Since `CodeFunction` has no `language`/`body`/`returns`, those keys land nowhere on unmarshal; detect instead by a sentinel: a `CodeFunction` with empty `Runtime` AND empty `File` is almost certainly a mis-placed RPC entry, so emit:
```go
		if fn.Runtime == "" && fn.File == "" {
			errs = append(errs, domain.ValidationError{Path: path, Message: "`functions:` now defines code functions (needs runtime+file); move Postgres functions to `rpc:`"})
		}
```
Re-run: PASS. (This also covers the empty-stub case.)

- [ ] **Step 6: Run config suite + commit**

Run: `go test -race ./internal/config/...`
Expected: PASS.
```bash
git add internal/config/validate.go internal/config/validate_test.go
git commit -m "feat(config): validate code functions; reject old RPC shape under functions:"
```

---

## Task 3: Domain port + route skeleton (`/functions/v1/:name` → 501)

Proves routing + apikey gating before any Node exists.

**Files:**
- Modify: `internal/domain/database.go` (or a new `internal/domain/functions.go`) — add `FunctionRuntime` interface + `FunctionRequest`/`FunctionResponse`
- Create: `internal/adapter/http/functions_handler.go`
- Modify: `internal/adapter/http/server.go` (`ServerDeps` + route registration)
- Test: `internal/adapter/http/functions_handler_test.go`

- [ ] **Step 1: Define the port**

Create `internal/domain/functions.go`:
```go
package domain

import "context"

// FunctionRequest is the invocation passed to a code function.
type FunctionRequest struct {
	Name    string
	Method  string
	Path    string
	Query   map[string][]string
	Headers map[string][]string
	Body    []byte
	Claims  map[string]any // nil when anonymous
}

// FunctionResponse is the handler's HTTP response.
type FunctionResponse struct {
	Status  int
	Headers map[string][]string
	Body    []byte
}

// FunctionRuntime invokes code functions. Implemented by internal/adapter/funcs.
type FunctionRuntime interface {
	Has(name string) bool
	Invoke(ctx context.Context, req FunctionRequest) (*FunctionResponse, error)
	Close() error
}
```

- [ ] **Step 2: Write a failing handler test (nil runtime → 501)**

Create `internal/adapter/http/functions_handler_test.go`:
```go
func TestFunctionsRoute501WhenNoRuntime(t *testing.T) {
	h := NewFunctionsHandler(nil) // no runtime configured
	r := gin.New()
	h.Register(r.Group("/functions/v1"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/functions/v1/whatever", nil))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d", w.Code)
	}
}
```

- [ ] **Step 3: Run to verify fail**

Run: `go test ./internal/adapter/http/ -run TestFunctionsRoute501WhenNoRuntime -v`
Expected: FAIL (`NewFunctionsHandler` undefined).

- [ ] **Step 4: Implement the handler skeleton**

Create `internal/adapter/http/functions_handler.go`:
```go
package http

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/saedx1/ultrabase/internal/domain"
)

type FunctionsHandler struct{ rt domain.FunctionRuntime }

func NewFunctionsHandler(rt domain.FunctionRuntime) *FunctionsHandler { return &FunctionsHandler{rt: rt} }

func (h *FunctionsHandler) Register(g *gin.RouterGroup) {
	g.Any("/:name", h.invoke)
}

func (h *FunctionsHandler) invoke(c *gin.Context) {
	if h.rt == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"message": "functions runtime not available"})
		return
	}
	name := c.Param("name")
	if !h.rt.Has(name) {
		c.JSON(http.StatusNotFound, gin.H{"message": "function not found"})
		return
	}
	body, _ := io.ReadAll(c.Request.Body)
	resp, err := h.rt.Invoke(c.Request.Context(), domain.FunctionRequest{
		Name: name, Method: c.Request.Method, Path: c.Request.URL.Path,
		Query: c.Request.URL.Query(), Headers: c.Request.Header, Body: body,
		Claims: claimsFromContext(c), // reuse the existing claims accessor; see Step 5
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"message": err.Error()})
		return
	}
	for k, vs := range resp.Headers {
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(resp.Status)
	_, _ = c.Writer.Write(resp.Body)
}
```

- [ ] **Step 5: Wire claims + the apikey-gated group in `server.go`**

In `functions_handler.go` add a `claimsFromContext(c *gin.Context) map[string]any` helper that reads whatever the existing JWT middleware stores on the gin context (inspect `middleware.go` for the context key it sets, e.g. `c.Get("claims")`; reuse it — do NOT invent a new key). In `server.go`, after the existing `/rest/v1` group is created with the apikey middleware, register:
```go
	fnGroup := router.Group("/functions/v1")
	fnGroup.Use(apiKeyMiddleware) // the SAME middleware value used for /rest/v1
	NewFunctionsHandler(deps.FunctionRuntime).Register(fnGroup)
```
Add `FunctionRuntime domain.FunctionRuntime` to `ServerDeps` (nil-safe: the handler returns 501 when nil).

- [ ] **Step 6: Run + commit**

Run: `go test -race ./internal/adapter/http/ -run TestFunctionsRoute -v` then `go build ./...`
Expected: PASS / clean.
```bash
git add internal/domain/functions.go internal/adapter/http/functions_handler.go internal/adapter/http/functions_handler_test.go internal/adapter/http/server.go
git commit -m "feat(http): add /functions/v1/:name route + FunctionRuntime port (501 stub)"
```

---

## Task 4: Vertical slice — embed `worker.js`, spawn one worker, invoke over UDS, relay response

The novel core. End-to-end: a `.js` function returns 200 in dev mode. No bundle/isolation/logs/creds yet.

**Files:**
- Create: `internal/adapter/funcs/worker.js` (embedded shim)
- Create: `internal/adapter/funcs/runtime.go` (pool of 1, UDS client, `Invoke`)
- Create: `internal/adapter/funcs/runtime_integration_test.go` (`//go:build integration`, needs `node`)

- [ ] **Step 1: Write the minimal worker shim**

Create `internal/adapter/funcs/worker.js`:
```js
import http from "node:http";
import { pathToFileURL } from "node:url";

// args: <socketPath> <fnName=absPath,...>
const [, , socketPath, fnSpec] = process.argv;
const fns = {};
for (const pair of fnSpec.split(",").filter(Boolean)) {
  const [name, file] = pair.split("=");
  const mod = await import(pathToFileURL(file).href);
  fns[name] = mod.default;
}

const server = http.createServer(async (req, res) => {
  if (req.url === "/healthz") { res.writeHead(200); res.end("ok"); return; }
  const fnName = req.headers["x-ultra-fn"];
  const handler = fns[fnName];
  if (!handler) { res.writeHead(404); res.end(JSON.stringify({ message: "unknown fn" })); return; }

  const chunks = [];
  for await (const c of req) chunks.push(c);
  const rawBody = Buffer.concat(chunks);

  const reqObj = { method: req.headers["x-ultra-method"] || "POST", path: req.headers["x-ultra-path"] || "/",
                   query: {}, headers: {}, body: rawBody.length ? JSON.parse(rawBody.toString()) : undefined };
  try {
    const result = await handler(reqObj, {});
    const headers = result.headers || { "content-type": "application/json" };
    res.writeHead(result.status || 200, headers);
    res.end(typeof result.body === "string" ? result.body : JSON.stringify(result.body ?? null));
  } catch (e) {
    res.writeHead(500); res.end(JSON.stringify({ message: String(e && e.message || e) }));
  }
});
server.listen(socketPath);
```

- [ ] **Step 2: Write the failing integration test**

Create `internal/adapter/funcs/runtime_integration_test.go`:
```go
//go:build integration

package funcs_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/saedx1/ultrabase/internal/adapter/funcs"
	"github.com/saedx1/ultrabase/internal/domain"
)

func TestInvokeHelloFunction(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.js"),
		[]byte(`export default async (req, ctx) => ({ status: 200, body: { hi: req.body.name } });`), 0o644)

	rt, err := funcs.New(funcs.Options{
		Dir:       dir,
		Functions: map[string]domain.CodeFunction{"hello": {Runtime: "node", File: "hello.js"}},
	})
	if err != nil { t.Fatal(err) }
	defer rt.Close()

	resp, err := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "hello", Method: "POST", Body: []byte(`{"name":"ada"}`),
	})
	if err != nil { t.Fatal(err) }
	if resp.Status != 200 { t.Fatalf("status %d", resp.Status) }
	if string(resp.Body) != `{"hi":"ada"}` { t.Fatalf("body %s", resp.Body) }
}
```

- [ ] **Step 3: Run to verify fail**

Run: `go test -tags=integration ./internal/adapter/funcs/ -run TestInvokeHelloFunction -v`
Expected: FAIL (`funcs.New` undefined).

- [ ] **Step 4: Implement the runtime (single worker)**

Create `internal/adapter/funcs/runtime.go`:
```go
package funcs

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/saedx1/ultrabase/internal/domain"
)

//go:embed worker.js
var workerJS []byte

type Options struct {
	Dir       string // directory holding the function source files (+ node_modules later)
	Functions map[string]domain.CodeFunction
}

type Runtime struct {
	opts    Options
	cmd     *exec.Cmd
	sock    string
	client  *http.Client
	fnNames map[string]bool
}

func New(opts Options) (*Runtime, error) {
	shim := filepath.Join(opts.Dir, ".ultra-worker.mjs")
	if err := os.WriteFile(shim, workerJS, 0o644); err != nil {
		return nil, err
	}
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("ultra-fn-%d.sock", time.Now().UnixNano()))

	var spec []string
	names := map[string]bool{}
	for name, fn := range opts.Functions {
		spec = append(spec, name+"="+filepath.Join(opts.Dir, fn.File))
		names[name] = true
	}

	cmd := exec.Command("node", shim, sock, strings.Join(spec, ","))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	rt := &Runtime{opts: opts, cmd: cmd, sock: sock, client: client, fnNames: names}
	if err := rt.waitHealthy(2 * time.Second); err != nil {
		rt.Close()
		return nil, err
	}
	return rt, nil
}

func (r *Runtime) waitHealthy(d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", "http://unix/healthz", nil)
		if resp, err := r.client.Do(req); err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("worker did not become healthy")
}

func (r *Runtime) Has(name string) bool { return r.fnNames[name] }

func (r *Runtime) Invoke(ctx context.Context, in domain.FunctionRequest) (*domain.FunctionResponse, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://unix/invoke", bytes.NewReader(in.Body))
	req.Header.Set("x-ultra-fn", in.Name)
	req.Header.Set("x-ultra-method", in.Method)
	req.Header.Set("x-ultra-path", in.Path)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	return &domain.FunctionResponse{Status: resp.StatusCode, Headers: resp.Header, Body: body.Bytes()}, nil
}

func (r *Runtime) Close() error {
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	_ = os.Remove(r.sock)
	return nil
}
```

- [ ] **Step 5: Run to verify pass**

Run: `go test -tags=integration ./internal/adapter/funcs/ -run TestInvokeHelloFunction -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/funcs/
git commit -m "feat(funcs): minimal Node worker over UDS + Invoke (vertical slice)"
```

---

## Task 5: Faithful request reconstruction (method/path/query/headers, JSON + raw body)

**Files:**
- Modify: `internal/adapter/funcs/worker.js`, `internal/adapter/funcs/runtime.go`
- Test: extend `runtime_integration_test.go`

- [ ] **Step 1: Add a failing test for query + raw body + headers**

```go
func TestInvokeRequestFidelity(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "echo.js"), []byte(
		`export default async (req) => ({ status: 200, body: { m: req.method, p: req.path, q: req.query.x || null, ct: req.headers["content-type"] || null, raw: typeof req.body } });`), 0o644)
	rt, _ := funcs.New(funcs.Options{Dir: dir, Functions: map[string]domain.CodeFunction{"echo": {Runtime: "node", File: "echo.js"}}})
	defer rt.Close()
	resp, _ := rt.Invoke(context.Background(), domain.FunctionRequest{
		Name: "echo", Method: "PATCH", Path: "/functions/v1/echo",
		Query:   map[string][]string{"x": {"1"}},
		Headers: map[string][]string{"Content-Type": {"text/plain"}},
		Body:    []byte("hello-raw"),
	})
	want := `{"m":"PATCH","p":"/functions/v1/echo","q":"1","ct":"text/plain","raw":"string"}`
	if string(resp.Body) != want { t.Fatalf("got %s", resp.Body) }
}
```

- [ ] **Step 2: Run (FAIL)** — `go test -tags=integration ./internal/adapter/funcs/ -run TestInvokeRequestFidelity -v`

- [ ] **Step 3: Pass full request context via a single base64 `X-Ultra-Context` header**

In `runtime.go`, replace the per-field `x-ultra-method`/`x-ultra-path` headers with one `X-Ultra-Context` header carrying base64(JSON). Add to `Invoke`:
```go
	ctxJSON, _ := json.Marshal(map[string]any{
		"method": in.Method, "path": in.Path, "query": in.Query, "headers": in.Headers,
	})
	req.Header.Set("x-ultra-context", base64.StdEncoding.EncodeToString(ctxJSON))
```
(Add `encoding/json`, `encoding/base64` imports; keep `x-ultra-fn`.) In `worker.js`, decode it and reconstruct `req`, parsing body as JSON only when `content-type` is JSON, else exposing the raw string:
```js
const ctx = JSON.parse(Buffer.from(req.headers["x-ultra-context"], "base64").toString());
const headers = lowerKeys(ctx.headers);
const ct = headers["content-type"] || "";
const body = ct.includes("application/json")
  ? (rawBody.length ? JSON.parse(rawBody.toString()) : undefined)
  : rawBody.toString();
const reqObj = { method: ctx.method, path: ctx.path,
                 query: firstValues(ctx.query), headers, body };
```
Add small helpers `lowerKeys` (map header keys to lowercase, first value) and `firstValues` (Go `map[string][]string` → `{k: v[0]}`) in `worker.js`.

- [ ] **Step 4: Run (PASS)** — same command.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/funcs/
git commit -m "feat(funcs): faithful req reconstruction via X-Ultra-Context header"
```

---

## Task 6: Injected clients (`ctx.supabase` / `ctx.serviceClient`) + minted service-role JWT + loopback

**Files:**
- Create: `internal/app/functoken.go` (mint short-lived service_role JWT using `JWTKeyManager`)
- Modify: `internal/adapter/funcs/runtime.go` (accept loopback URL, anon key, token minter; put creds in context), `worker.js` (build clients)
- Add vendored dep `@supabase/supabase-js` to the dev `functions/` fixture used in tests
- Test: integration test that a function inserts via `ctx.supabase` (RLS as caller) and escalates via `ctx.serviceClient`

- [ ] **Step 1: Mint helper test**

Create `internal/app/functoken_test.go`:
```go
func TestMintServiceToken(t *testing.T) {
	km, _ := app.NewInMemoryJWTKeyManager("kid1", nil)
	tok, err := app.MintServiceToken(context.Background(), km, 30*time.Second)
	if err != nil { t.Fatal(err) }
	parsed, _ := jwt.Parse(tok, func(*jwt.Token) (any, error) {
		k, _ := km.Active(context.Background()); return k.PublicKey, nil
	})
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["role"] != "service_role" { t.Fatalf("role=%v", claims["role"]) }
}
```

- [ ] **Step 2: Run (FAIL)** — `go test ./internal/app/ -run TestMintServiceToken -v`

- [ ] **Step 3: Implement `MintServiceToken`**

Create `internal/app/functoken.go` mirroring the signing used in `auth_handler.go:1580-1606` (RS256, `kid` header, `iss` matching the auth issuer, `role: "service_role"`, short `exp`):
```go
package app

import (
	"context"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func MintServiceToken(ctx context.Context, km *JWTKeyManager, ttl time.Duration) (string, error) {
	key, err := km.Active(ctx)
	if err != nil { return "", err }
	now := time.Now()
	claims := jwt.MapClaims{
		"role": "service_role",
		"iat":  now.Unix(),
		"exp":  now.Add(ttl).Unix(),
		"aud":  "authenticated",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = key.KID // confirm the field name on JWTKey
	return tok.SignedString(key.PrivateKey)
}
```
(Verify `JWTKey`'s key-id field name in `jwtkeys.go`; match the issuer/audience the middleware expects by copying from the auth-handler mint site.)

- [ ] **Step 4: Run (PASS)** — same command.

- [ ] **Step 5: Thread creds through the runtime into `X-Ultra-Context`**

Extend `funcs.Options` with `LoopbackURL string`, `AnonKey string`, and `MintService func(ctx context.Context) (string, error)`. In `Invoke`, add to the context JSON:
```go
		"dataPlane": map[string]any{
			"url": in.LoopbackURL, "anonKey": r.opts.AnonKey,
			"callerToken": in.CallerToken, "serviceToken": serviceTok,
		},
```
where `serviceTok, _ = r.opts.MintService(ctx)` and `in.CallerToken` is added to `domain.FunctionRequest` (the caller's bearer token, forwarded by the HTTP handler from the `Authorization` header).

- [ ] **Step 6: Build clients in the shim**

In `worker.js`, import the vendored client and build `ctx`:
```js
import { createClient } from "@supabase/supabase-js";
function buildCtx(dp, claims) {
  const mk = (token) => createClient(dp.url, dp.anonKey, {
    global: { headers: { Authorization: `Bearer ${token}`, apikey: dp.anonKey } },
  });
  return {
    supabase: mk(dp.callerToken || dp.anonKey),
    serviceClient: mk(dp.serviceToken),
    claims, env: {}, log: console, // env/log filled in later tasks
  };
}
```
Call `handler(reqObj, buildCtx(ctx.dataPlane, ctx.claims))`.

- [ ] **Step 7: Integration test (RLS as caller + escalation)**

Add an integration test that boots Postgres (via the existing `dbboot` helper) + the real HTTP server + the runtime, creates a table with an RLS policy keyed on `auth.uid()`, and a function that (a) inserts via `ctx.supabase` as a signed-in caller (succeeds under RLS) and (b) reads a forbidden row via `ctx.serviceClient` (succeeds, BYPASSRLS). Model the server/Postgres wiring on `internal/adapter/http/supabase_integration_test.go`. The function fixture dir vendors `@supabase/supabase-js` via `npm i --prefix <dir> @supabase/supabase-js` in test setup (skip if offline → `t.Skip`).

- [ ] **Step 8: Run + commit**

Run: `go test -tags=integration ./internal/app/ ./internal/adapter/funcs/ -run 'Token|Inject' -v`
Expected: PASS.
```bash
git add internal/app/functoken.go internal/app/functoken_test.go internal/adapter/funcs/
git commit -m "feat(funcs): injected supabase clients via minted service-role JWT + loopback"
```

---

## Task 7: Scrubbed worker environment

**Files:**
- Modify: `internal/adapter/funcs/runtime.go` (set `cmd.Env` explicitly)
- Test: integration test that the worker cannot read parent env

- [ ] **Step 1: Failing test**

```go
func TestWorkerEnvScrubbed(t *testing.T) {
	os.Setenv("AWS_SECRET_ACCESS_KEY", "leak-me")
	defer os.Unsetenv("AWS_SECRET_ACCESS_KEY")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "peek.js"), []byte(
		`export default async () => ({ status: 200, body: { aws: process.env.AWS_SECRET_ACCESS_KEY || null } });`), 0o644)
	rt, _ := funcs.New(funcs.Options{Dir: dir, Functions: map[string]domain.CodeFunction{"peek": {Runtime: "node", File: "peek.js"}}})
	defer rt.Close()
	resp, _ := rt.Invoke(context.Background(), domain.FunctionRequest{Name: "peek", Method: "GET"})
	if string(resp.Body) != `{"aws":null}` { t.Fatalf("env leaked: %s", resp.Body) }
}
```

- [ ] **Step 2: Run (FAIL)** — the child currently inherits env via default `exec.Cmd`.

- [ ] **Step 3: Construct an explicit env**

In `New`, before `cmd.Start()`:
```go
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"NODE_ENV=production",
		"HOME=" + os.TempDir(),
	}
```
(Function `env:` values are added in Task 8, per request — not here.)

- [ ] **Step 4: Run (PASS)** — same command.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/funcs/
git commit -m "feat(funcs): spawn workers with a scrubbed, explicit environment"
```

---

## Task 8: `ULTRA_ENV_` map + invoke-time function `env:` resolution + fail-early

**Files:**
- Create: `internal/config/ultraenv.go` (`LoadUltraEnv(mode string) (map[string]string, error)`)
- Modify: `internal/adapter/funcs/runtime.go` (resolve `env:` per invoke into context), `worker.js` (expose `ctx.env`)
- Modify: validation to fail-early when a function `${ULTRA_ENV_*}` ref is absent from the map
- Test: `internal/config/ultraenv_test.go` + integration

- [ ] **Step 1: Map loader test**

```go
func TestLoadUltraEnvPrecedenceAndScope(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".env"), []byte("ULTRA_ENV_A=base\nULTRA_ENV_B=base\nSECRET=should-not-load\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".development.env"), []byte("ULTRA_ENV_B=mode\n"), 0o644)
	t.Setenv("ULTRA_ENV_C", "fromproc")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "nope")

	m, err := config.LoadUltraEnv(dir, "development")
	if err != nil { t.Fatal(err) }
	if m["ULTRA_ENV_A"] != "base" || m["ULTRA_ENV_B"] != "mode" || m["ULTRA_ENV_C"] != "fromproc" {
		t.Fatalf("precedence wrong: %v", m)
	}
	if _, ok := m["SECRET"]; ok { t.Fatal("non-prefixed file key leaked") }
	if _, ok := m["AWS_SECRET_ACCESS_KEY"]; ok { t.Fatal("non-prefixed proc env leaked") }
}
```

- [ ] **Step 2: Run (FAIL)** — `go test ./internal/config/ -run TestLoadUltraEnv -v`

- [ ] **Step 3: Implement `LoadUltraEnv`**

Create `internal/config/ultraenv.go`. Parse `.env` then `.<mode>.env` (reuse the existing dotenv line parser — factor out a `parseDotenvBytes([]byte) map[string]string` from `loadDotenv` if not already exported), keep only `ULTRA_ENV_`-prefixed keys, then overlay `ULTRA_ENV_*` keys from `os.Environ()` (highest precedence). Return the map. Do **not** call `os.Setenv`.
```go
func LoadUltraEnv(dir, mode string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range []string{".env", "." + mode + ".env"} {
		kv, err := parseDotenvFile(filepath.Join(dir, f))
		if err != nil { return nil, err }
		for k, v := range kv {
			if strings.HasPrefix(k, "ULTRA_ENV_") { out[k] = v }
		}
	}
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok && strings.HasPrefix(k, "ULTRA_ENV_") { out[k] = v }
	}
	return out, nil
}
```

- [ ] **Step 4: Run (PASS)** — same command.

- [ ] **Step 5: Resolve function `env:` at invoke time (fail-early at load)**

Add `EnvMap map[string]string` to `funcs.Options`. In `Invoke`, build the per-request resolved env:
```go
	resolvedEnv := map[string]string{}
	for k, v := range r.opts.Functions[in.Name].Env {
		if ref, ok := asUltraEnvRef(v); ok { // v == "${ULTRA_ENV_X}"
			val, present := r.opts.EnvMap[ref]
			if !present { return nil, fmt.Errorf("function %q: ${%s} not in ULTRA_ENV_ namespace", in.Name, ref) }
			resolvedEnv[k] = val
		} else {
			resolvedEnv[k] = v // literal
		}
	}
```
Put `resolvedEnv` into the context JSON under `"env"`. Also add a startup check (in the runtime constructor or a validation pass) that every `${ULTRA_ENV_*}` ref across all functions exists in `EnvMap`, returning an error so boot **fails early** rather than at first invoke. Add `asUltraEnvRef` (regex `^\$\{(ULTRA_ENV_[A-Za-z0-9_]+)\}$`). In `worker.js`, set `ctx.env = ctx.env` from the decoded context (replace the `env: {}` placeholder from Task 6).

- [ ] **Step 6: Integration test** — a function reads `ctx.env.FOO` resolved from `ULTRA_ENV_FOO`; a missing ref fails `funcs.New`. Run both.

- [ ] **Step 7: Commit**

```bash
git add internal/config/ultraenv.go internal/config/ultraenv_test.go internal/adapter/funcs/
git commit -m "feat: ULTRA_ENV_ in-memory map + invoke-time function env resolution (fail-early)"
```

---

## Task 9: Log capture (AsyncLocalStorage + console patch + NDJSON → slog)

**Files:**
- Modify: `internal/adapter/funcs/worker.js` (ALS, console patch, NDJSON on stdout)
- Modify: `internal/adapter/funcs/runtime.go` (capture stdout pipe → a `*slog.Logger`)
- Test: integration test asserting a log line is captured with the right `requestId`

- [ ] **Step 1: Failing test**

```go
func TestLogCaptureAttribution(t *testing.T) {
	var buf safeBuffer // mutex-guarded bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "noisy.js"), []byte(
		`export default async (req, ctx) => { ctx.log.info("hello", { who: "ada" }); console.log("plain"); return { status: 200, body: {} }; };`), 0o644)
	rt, _ := funcs.New(funcs.Options{Dir: dir, Logger: logger,
		Functions: map[string]domain.CodeFunction{"noisy": {Runtime: "node", File: "noisy.js"}}})
	defer rt.Close()
	rt.Invoke(context.Background(), domain.FunctionRequest{Name: "noisy", Method: "GET", RequestID: "req-123"})
	time.Sleep(100 * time.Millisecond) // allow stdout drain
	if !strings.Contains(buf.String(), `"requestId":"req-123"`) || !strings.Contains(buf.String(), "hello") {
		t.Fatalf("log not captured/attributed: %s", buf.String())
	}
}
```
(Add `RequestID string` to `domain.FunctionRequest`; the HTTP handler generates it, e.g. a short random id, and passes it in `X-Ultra-Context`.)

- [ ] **Step 2: Run (FAIL)** — `go test -tags=integration ./internal/adapter/funcs/ -run TestLogCapture -v`

- [ ] **Step 3: Shim side — ALS + console patch + NDJSON**

In `worker.js`:
```js
import { AsyncLocalStorage } from "node:async_hooks";
const als = new AsyncLocalStorage();
function emit(level, msg, fields) {
  const s = als.getStore() || {};
  process.stdout.write(JSON.stringify({ ts: Date.now(), level, requestId: s.requestId, fn: s.fn, msg, fields }) + "\n");
}
for (const lvl of ["log", "info", "warn", "error", "debug"]) {
  console[lvl] = (msg, fields) => emit(lvl === "log" ? "info" : lvl, typeof msg === "string" ? msg : JSON.stringify(msg), fields);
}
const ctxLog = { debug: (m, f) => emit("debug", m, f), info: (m, f) => emit("info", m, f),
                 warn: (m, f) => emit("warn", m, f), error: (m, f) => emit("error", m, f) };
```
Wrap the handler call: `await als.run({ requestId: ctx.requestId, fn: fnName }, () => handler(reqObj, { ...buildCtx(...), log: ctxLog }))`. **Important:** the worker must NOT also write its own framing to stdout for responses (responses go over HTTP), so stdout is exclusively NDJSON logs.

- [ ] **Step 4: Go side — capture stdout → slog**

In `runtime.go`, replace `cmd.Stdout = os.Stdout` with a pipe; scan it line-by-line; for each line, `json.Unmarshal` into `{ts,level,requestId,fn,msg,fields}` and forward to `r.opts.Logger` at the mapped level with those attributes. Non-JSON lines → `Logger.Warn(line, "worker", true)`. Capture `cmd.Stderr` similarly at warn/error. Add `Logger *slog.Logger` to `Options` (default `slog.Default()`). Truncate lines > 16KiB.

- [ ] **Step 5: Run (PASS)** — same command.

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/funcs/ internal/domain/functions.go
git commit -m "feat(funcs): per-request log capture via AsyncLocalStorage + NDJSON->slog"
```

---

## Task 10: Worker pool, concurrency, timeout (504), crash (502), saturation (503)

**Files:**
- Modify: `internal/adapter/funcs/runtime.go` (pool of N, round-robin, health watchdog, restart), `internal/adapter/http/functions_handler.go` (map timeout→504, conn error→502, saturation→503)
- Test: integration tests for concurrency, timeout, crash

- [ ] **Step 1: Failing tests** — (a) 20 concurrent invocations of a 50ms-sleeping handler complete in well under 20×50ms (proves concurrency); (b) a handler that sleeps past a 100ms `deadlineMs` returns a context-cancellation error surfaced as 504; (c) a handler that calls `process.exit(1)` yields a 502 and the next invocation succeeds (worker restarted).

- [ ] **Step 2: Run (FAIL)** — `go test -tags=integration ./internal/adapter/funcs/ -run 'Concurren|Timeout|Crash' -v`

- [ ] **Step 3: Implement the pool**

Generalize `Runtime` to hold `[]*worker` (each = cmd+sock+client+healthy flag). `New` spawns `Options.PoolSize` (default `min(4, GOMAXPROCS)`; on Lambda set 1 via an option). `Invoke` picks the next healthy worker round-robin (atomic counter); on a transport error marks the worker unhealthy and a background goroutine restarts it (re-write shim, re-spawn, re-healthz) and removes/re-adds it. A bounded semaphore (`Options.MaxInFlight`) gates total concurrency; exhaustion returns a typed `ErrSaturated`.

- [ ] **Step 4: Map errors to HTTP in the handler**

In `functions_handler.go`, set per-request timeout from the function's `Timeout` (default 30s) via `context.WithTimeout`; translate: `ctx.Err()==DeadlineExceeded` → 504; `errors.Is(err, funcs.ErrSaturated)` → 503; other invoke error → 502. The shim aborts the in-flight handler via an `AbortController` wired to request-close (Node fires `req.on("close")`).

- [ ] **Step 5: Run (PASS)** — same command.

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/funcs/ internal/adapter/http/functions_handler.go
git commit -m "feat(funcs): worker pool with concurrency, timeout(504), crash(502), saturation(503)"
```

---

## Task 11: Build + ship the bundle in `ultra deploy`

**Files:**
- Create: `internal/cli/bundle.go` (`BuildBundle(dir string) (path string, err error)` — npm ci + tar.zst of source+node_modules+manifest)
- Modify: `internal/cli/deploy.go` (after config upload: build bundle, write to object storage, record pointer)
- Modify: `internal/domain/schema.go` (add `Config.FunctionsBundle string` — the bundle object URI/pointer the deployed config carries)
- Test: `internal/cli/bundle_test.go` (build a bundle from a fixture dir; assert tar contains the function + node_modules + manifest)

- [ ] **Step 1: Failing test for `BuildBundle`**

```go
func TestBuildBundle(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "functions"), 0o755)
	os.WriteFile(filepath.Join(dir, "functions", "a.js"), []byte("export default async()=>({status:200})"), 0o644)
	os.WriteFile(filepath.Join(dir, "functions", "package.json"), []byte(`{"name":"fns","private":true}`), 0o644)
	out, err := cli.BuildBundle(dir)
	if err != nil { t.Fatal(err) }
	names := tarEntryNames(t, out) // helper: list paths in the tar(.zst)
	assertContains(t, names, "functions/a.js")
	assertContains(t, names, "manifest.json")
}
```

- [ ] **Step 2: Run (FAIL)** — `go test ./internal/cli/ -run TestBuildBundle -v`

- [ ] **Step 3: Implement `BuildBundle`**

`BuildBundle`: run `npm ci` in `<dir>/functions` (skip cleanly if no `package.json`), then write a tarball (`archive/tar` + `klauspost/compress/zstd` — check `go.mod`; if zstd isn't a dep, use gzip `.tar.gz` to avoid a new dependency) containing `functions/**` (source + `node_modules`) and a generated `manifest.json` (`{ functions: {name: {file, runtime}}, builtAt }`). Return the temp tarball path. Fail loudly if `npm ci` fails (so deploy aborts — the running deployment is untouched).

- [ ] **Step 4: Run (PASS)** — same command.

- [ ] **Step 5: Wire into `deploy`**

In `runDeploy`, after `c.UploadYAML(...)`: if `cfg.Functions` is non-empty, call `BuildBundle`, then upload the tarball to the **same object-storage location as the config** (for `s3://` configs, derive bucket/prefix and use the existing s3 adapter / a `Source.Write`-style put; for the cloud path, note that instancez's bundle handoff is out of repo and gate it behind the cloud client when available), and set the deployed config's `FunctionsBundle` pointer (object key + version). Print the bundle size + version. Add a `deploy` unit test with a fake uploader verifying the pointer is recorded.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/bundle.go internal/cli/bundle_test.go internal/cli/deploy.go internal/domain/schema.go
git commit -m "feat(cli): ultra deploy builds + ships the functions bundle (vendored)"
```

---

## Task 12: `serve` consumes the bundle (fetch + atomic extract + drain-swap on change); `dev` builds on the fly

**Files:**
- Create: `internal/app/funcbundle.go` (`FetchAndExtract(ctx, bundleURI) (dir string, err error)`)
- Modify: `internal/app/engine.go` (construct `FunctionRuntime`: dev → from local `functions/` dir after `npm ci`; serve → from extracted bundle; never build in serve), wire into HTTP server deps, start/stop, and re-extract+swap on `Watch` bundle-version change
- Modify: `internal/cli/dev.go` (ensure `npm ci` runs in `functions/` on boot when manifest changed; reuse `BuildBundle`'s npm step or a `vendorDeps` helper)
- Test: integration — serve from an extracted bundle dir; dev from a local dir; a bundle-version change recycles the pool

- [ ] **Step 1: Failing test for extract** — `FetchAndExtract` of a tarball (produced by `BuildBundle`) yields a dir containing the function files; partial/failed extract leaves no dir mounted (atomic temp→rename).

- [ ] **Step 2: Run (FAIL)** — `go test ./internal/app/ -run TestFetchAndExtract -v`

- [ ] **Step 3: Implement `FetchAndExtract`** — read bytes from the bundle source (local path or via the s3 adapter), un-tar into a temp dir, `os.Rename` into the final dir (atomic swap); remove a prior dir after swap.

- [ ] **Step 4: Wire the runtime into the engine**

In `engine.go` `Start`, after migrate/seed and before/with the HTTP server:
- If `cfg.Functions` is empty → no runtime (handler stays 501-nil-safe).
- Dev mode (FileSource): run `npm ci` in `<configDir>/functions` if `package.json` changed, then `funcs.New(Options{Dir: <configDir>, Functions: cfg.Functions, EnvMap: ultraEnv, LoopbackURL: "http://127.0.0.1:<port>", AnonKey: …, MintService: …, Logger: …})`.
- Serve mode with a bundle pointer: `FetchAndExtract(cfg.FunctionsBundle)` → `funcs.New(Options{Dir: extracted, …})`. **Never run npm ci in serve.**
- Pass the runtime into `ServerDeps.FunctionRuntime`.
- On engine shutdown call `runtime.Close()`.
- In the existing `Watch`/reload path, when the new config's `FunctionsBundle` version differs: `FetchAndExtract` the new bundle, build a new `funcs.Runtime`, atomically swap it into the handler, then `Close()` the old (drain by letting in-flight finish — close after a short grace).

- [ ] **Step 5: Run integration (PASS)** — boot serve against an extracted bundle, invoke a function end-to-end; flip the bundle version and assert new code serves.

- [ ] **Step 6: Commit**

```bash
git add internal/app/funcbundle.go internal/app/engine.go internal/cli/dev.go
git commit -m "feat(app): serve consumes function bundle (atomic extract + drain-swap); dev builds on the fly"
```

---

## Task 13: supabase-js compatibility (`functions.invoke`)

**Files:**
- Modify: `test/integration/supabase-js/run.mjs` (add a `functions.invoke` scenario)
- Modify: `internal/adapter/http/supabase_integration_test.go` (provision a function for the harness)
- Test: the existing `TestSupabaseJSCompat`

- [ ] **Step 1: Add a function fixture + a failing harness assertion**

In the Go test that drives `run.mjs`, configure one code function (e.g. `echo` returning `{ status: 200, body: { echoed: req.body } }` and one path returning a non-2xx). In `run.mjs`, add:
```js
{
  const { data, error } = await supabase.functions.invoke("echo", { body: { x: 1 } });
  assert(!error, "echo should succeed");
  assert.deepEqual(data, { echoed: { x: 1 } });
}
{
  const { data, error } = await supabase.functions.invoke("boom", { body: {} });
  assert(error && error.name === "FunctionsHttpError", "non-2xx -> FunctionsHttpError");
}
```

- [ ] **Step 2: Run (FAIL until the runtime is wired into the harness server)**

Run: `go test -run TestSupabaseJSCompat -tags=integration -race ./internal/adapter/http/...`
Expected: FAIL initially.

- [ ] **Step 3: Wire the runtime into the harness server**

Ensure the harness boots the server with a `FunctionRuntime` over a fixture `functions/` dir (vendoring `@supabase/supabase-js` once in setup). Confirm `Content-Type: application/json` so supabase-js parses `data` as JSON; non-2xx returns a JSON body so `FunctionsHttpError` carries context.

- [ ] **Step 4: Run (PASS)** — same command.

- [ ] **Step 5: Commit**

```bash
git add test/integration/supabase-js/run.mjs internal/adapter/http/supabase_integration_test.go
git commit -m "test: supabase-js functions.invoke compat (success + FunctionsHttpError)"
```

---

## Task 14: Bake Node into the runtime images

`serve` spawns `node` workers, so the shipped images must contain a Node ≥22
runtime — today they ship only the Go binary.

**Files:**
- Modify: `Dockerfile` (main image), `Dockerfile.lambda`

- [ ] **Step 1: Add Node to the final stage of `Dockerfile.lambda`**

In the final `FROM alpine:3.21` stage, install Node:
```dockerfile
RUN apk add --no-cache ca-certificates nodejs npm
```
(npm is needed only if a path ever vendors at runtime — for `serve` it is not, but it is harmless and small; if image size matters, install `nodejs` only and drop `npm`, since `serve` never builds.)

- [ ] **Step 2: Mirror the change in the main `Dockerfile`**

Add the same `nodejs` package to the main `Dockerfile`'s runtime stage.

- [ ] **Step 3: Verify Node is present and a worker boots in-image**

Build the image locally and run `docker run --rm <image> node --version`.
Expected: prints `v22.x` (or the installed LTS ≥ 22).

- [ ] **Step 4: Commit**

```bash
git add Dockerfile Dockerfile.lambda
git commit -m "build: bake Node >=22 into runtime images for code functions"
```

---

## Task 15: Docs, example config, full feedback loop

**Files:**
- Modify: `ultrabase.yaml` / `docs/example-ultrabase.yaml` (add a `functions:` example + a `functions/` sample)
- Modify: `CLAUDE.md` (note the new `/functions/v1` surface + `rpc:` rename) and any `README`/docs that listed CLI/config surfaces

- [ ] **Step 1: Add a runnable example** — a `functions/hello.js` + a `functions:` entry in `docs/example-ultrabase.yaml`, plus a one-paragraph docs section describing authoring, `env:`/`${ULTRA_ENV_*}`, and the deploy→serve lifecycle.

- [ ] **Step 2: Run the FULL local feedback loop**

```bash
go build ./...
go test -race ./...
go test -tags=integration -race ./internal/adapter/funcs/... ./internal/app/... ./internal/cli/... ./internal/config/... ./internal/adapter/http/...
```
Expected: all PASS (integration needs Docker + node/npm). Fix anything red before proceeding.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "docs: document code functions; example config + functions/ sample"
```

---

## Task 16: Squash to a single revertable commit

- [ ] **Step 1: Confirm green** — re-run the full feedback loop from Task 15 Step 2; all PASS.

- [ ] **Step 2: Squash the branch into one commit**

From `design/custom-code-functions`, with `main` as the base:
```bash
git reset --soft $(git merge-base HEAD main)
git commit -m "feat: custom code functions (JS HTTP handlers at /functions/v1)

Adds JavaScript HTTP-handler functions served at /functions/v1/<name>,
executed by a pool of Node worker processes (each a tiny http server over a
Unix socket), wire-compatible with supabase.functions.invoke(). Data access is
loopback to /rest/v1 via an injected supabase-js client (RLS as caller; minted
short-lived service_role JWT for escalation). Code + vendored node_modules ship
as a tarball built by 'ultra deploy' and consumed (never built) by serve; dev
builds on the fly. Function env: secrets resolve from an in-memory ULTRA_ENV_
map at invoke time; workers run with a scrubbed environment. Renames the
Postgres-function block 'functions:' -> 'rpc:'.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```
(This includes the spec/plan doc commits already on the branch; that's fine — they are part of the feature. If you want code-only, reset to the first design-doc commit instead of `merge-base`.)

- [ ] **Step 3: Final verification** — `git log --oneline main..HEAD` shows exactly one commit; `go build ./... && go test -race ./...` PASS.

---

## Self-review notes (gaps to watch during execution)

- **`domain.ValidationError` field names** (Task 2) and **`JWTKey` key-id field** (Task 6) are referenced from memory — verify against the actual structs and adjust before writing the code, not after.
- **zstd vs gzip** (Task 11): use whatever compressor is already in `go.mod`; do not add a dependency just for the bundle. `.tar.gz` via stdlib is an acceptable fallback.
- **`FunctionsBundle` pointer + self-host upload target** (Task 11) is the one place the deploy↔serve handoff is newly built: deploy writes the bundle next to the `s3://` config and records the pointer; the cloud/instancez bundle handoff is explicitly out of this repo.
- **Claims context key** (Task 3 Step 5): reuse the exact key the existing JWT middleware sets; do not introduce a new one.
- **Caller token forwarding** (Task 6): the HTTP handler must copy the inbound `Authorization` bearer into `FunctionRequest.CallerToken`.
