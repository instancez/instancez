# Strict unknown-key config validation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reject any unrecognized key in an instancez config (YAML or the dashboard JSON edit path) with a readable error, instead of silently dropping it.

**Architecture:** Swap the lenient `yaml.Unmarshal` in the three `ParseBytes*` loaders for a `yaml.Decoder` with `KnownFields(true)`, and add a strict `UnmarshalConfigJSON` (`json.Decoder` + `DisallowUnknownFields`) for the JSON paths. A small translator turns the library errors into readable messages. Map-keyed sections keep arbitrary keys.

**Tech Stack:** Go 1.25, `gopkg.in/yaml.v3`, `encoding/json`, gin.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-06-20-strict-config-validation-design.md`.
- No schema or wire-format change. Valid configs must parse exactly as before; only unknown keys newly error.
- Map-keyed sections (`tables`, `storage`, `rpc`, `functions`, `auth.oauth`, `auth.email.templates`) must keep accepting arbitrary user keys. `KnownFields(true)` / `DisallowUnknownFields()` already do this — do not hand-roll a node walker.
- `${VAR}` interpolation still runs before decode (unchanged order in the loaders).
- Feedback loop green before each commit: `go build ./...`, `go test -race ./...`, and `go test -tags=integration -race ./...` for touched packages (incl. `TestSupabaseJSCompat`).
- Branch policy: stay on `main`, no branches. Each `git add` lists only that task's files (tree has unrelated dirty files); never `git add -A`. No co-author trailer.

---

### Task 1: unknown-key error translator

**Files:**
- Create: `internal/config/strict.go`
- Test: `internal/config/strict_test.go`

**Interfaces:**
- Produces:
  - `func strictUnmarshalYAML(data []byte, cfg *domain.Config) error`
  - `func unmarshalConfigJSONStrict(data []byte) (*domain.Config, error)` (exported wrapper added in Task 3; here we build the shared translator it uses)
  - `func translateUnknownKeyErr(err error) error` — turns a `*yaml.TypeError` into a readable, joined message; passes other errors through.

- [ ] **Step 1: Write the failing test**

Create `internal/config/strict_test.go`:

```go
package config

import (
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestStrictUnmarshalYAML_RejectsUnknownTopLevelKey(t *testing.T) {
	var cfg domain.Config
	err := strictUnmarshalYAML([]byte("version: 1\ntabels: {}\n"), &cfg)
	if err == nil {
		t.Fatal("expected error for unknown key 'tabels'")
	}
	if !strings.Contains(err.Error(), `"tabels"`) {
		t.Errorf("message should name the bad key, got: %s", err.Error())
	}
}

func TestStrictUnmarshalYAML_RejectsUnknownNestedKey(t *testing.T) {
	var cfg domain.Config
	// google was removed from auth (now auth.oauth.<name>).
	err := strictUnmarshalYAML([]byte("auth:\n  google:\n    client_id: x\n"), &cfg)
	if err == nil {
		t.Fatal("expected error for removed key 'auth.google'")
	}
	if !strings.Contains(err.Error(), `"google"`) || !strings.Contains(err.Error(), "auth") {
		t.Errorf("message should name 'google' and the auth section, got: %s", err.Error())
	}
}

func TestStrictUnmarshalYAML_AllowsMapKeys(t *testing.T) {
	var cfg domain.Config
	yaml := "tables:\n  todos:\n    fields: []\nauth:\n  oauth:\n    google:\n      client_id: x\n      client_secret: y\n"
	if err := strictUnmarshalYAML([]byte(yaml), &cfg); err != nil {
		t.Fatalf("map keys (table name, oauth provider name) must be allowed, got: %v", err)
	}
}

func TestStrictUnmarshalYAML_ReportsMultipleUnknownKeys(t *testing.T) {
	var cfg domain.Config
	err := strictUnmarshalYAML([]byte("foo: 1\nbar: 2\n"), &cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `"foo"`) || !strings.Contains(err.Error(), `"bar"`) {
		t.Errorf("both unknown keys should be reported, got: %s", err.Error())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestStrictUnmarshalYAML`
Expected: FAIL — `undefined: strictUnmarshalYAML`.

- [ ] **Step 3: Implement `strict.go`**

```go
package config

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/instancez/instancez/internal/domain"
	"gopkg.in/yaml.v3"
)

// strictUnmarshalYAML decodes YAML into cfg and rejects any key that does not
// map to a struct field. Map-keyed sections (tables, storage, rpc, functions,
// auth.oauth, auth.email.templates) still accept arbitrary keys because
// KnownFields only constrains struct fields.
func strictUnmarshalYAML(data []byte, cfg *domain.Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil {
		return translateUnknownKeyErr(err)
	}
	return nil
}

// yamlUnknownFieldRe matches yaml.v3's KnownFields error sub-message, e.g.
// `line 48: field google not found in type domain.Auth`.
var yamlUnknownFieldRe = regexp.MustCompile(`^line (\d+): field (\S+) not found in type (\S+)$`)

// yamlTypeToSection maps a Go struct type name (as yaml.v3 reports it) to the
// config path the user wrote it under, so the message reads in their terms.
// domain.Config is the top level and intentionally absent (no "under" suffix).
var yamlTypeToSection = map[string]string{
	"domain.Auth":            "auth",
	"domain.AuthEmail":       "auth.email",
	"domain.EmailTemplate":   "auth.email.templates.<name>",
	"domain.OAuthProvider":   "auth.oauth.<name>",
	"domain.Server":          "server",
	"domain.Project":         "project",
	"domain.Providers":       "providers",
	"domain.EmailProvider":   "providers.email",
	"domain.StorageProvider": "providers.storage",
	"domain.DatabaseConfig":  "database",
	"domain.Table":           "tables.<name>",
	"domain.Field":           "tables.<name>.fields[]",
	"domain.Index":           "tables.<name>.indexes[]",
	"domain.RLSPolicy":       "tables.<name>.rls[]",
	"domain.Function":        "rpc.<name>",
	"domain.CodeFunction":    "functions.<name>",
	"domain.Bucket":          "storage.<name>",
}

// translateUnknownKeyErr converts a yaml.v3 TypeError into a readable message
// listing every unknown key. Non-TypeErrors pass through unchanged.
func translateUnknownKeyErr(err error) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return err
	}
	msgs := make([]string, 0, len(te.Errors))
	for _, sub := range te.Errors {
		msgs = append(msgs, friendlyYAMLFieldErr(sub))
	}
	return errors.New(strings.Join(msgs, "; "))
}

func friendlyYAMLFieldErr(sub string) string {
	m := yamlUnknownFieldRe.FindStringSubmatch(sub)
	if m == nil {
		return sub // some other type error; show it verbatim
	}
	line, field, typ := m[1], m[2], m[3]
	if section, ok := yamlTypeToSection[typ]; ok {
		return fmt.Sprintf("line %s: unknown key %q under %s", line, field, section)
	}
	return fmt.Sprintf("line %s: unknown key %q", line, field)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestStrictUnmarshalYAML -v`
Expected: PASS. If a message assertion fails because yaml.v3's wording differs from the regex, adjust `yamlUnknownFieldRe` to the actual string printed in the failure and re-run.

- [ ] **Step 5: Commit**

```bash
git add internal/config/strict.go internal/config/strict_test.go
git commit -m "feat(config): add strict YAML decoder + unknown-key error translator"
```

---

### Task 2: wire strict YAML into the loaders + fix exposed fixtures

**Files:**
- Modify: `internal/config/loader.go` (`ParseBytes:35`, `ParseBytesLenient:97`, `ParseBytesRaw:109`)
- Possibly modify: any test fixture or config the new strictness exposes (unknown until the suite runs).

**Interfaces:**
- Consumes: `strictUnmarshalYAML` (Task 1).

- [ ] **Step 1: Write the failing test**

Add to `internal/config/strict_test.go`:

```go
func TestParseBytes_RejectsUnknownKey(t *testing.T) {
	_, err := ParseBytes([]byte("version: 1\nbogus: true\n"), "test.yaml")
	if err == nil || !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("ParseBytes should reject unknown key, got: %v", err)
	}
}

func TestParseBytesRaw_RejectsUnknownKey(t *testing.T) {
	_, err := ParseBytesRaw([]byte("version: 1\nbogus: true\n"), "test.yaml")
	if err == nil || !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("ParseBytesRaw should reject unknown key, got: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestParseBytes.*UnknownKey'`
Expected: FAIL (the key is currently dropped silently, no error).

- [ ] **Step 3: Swap the three unmarshals**

In `loader.go`, replace each `if err := yaml.Unmarshal([]byte(interpolated), &cfg); err != nil {` (and the raw `yaml.Unmarshal(data, &cfg)`) with the strict variant:

`ParseBytes`:
```go
	var cfg domain.Config
	if err := strictUnmarshalYAML([]byte(interpolated), &cfg); err != nil {
		return nil, &domain.ConfigError{Path: origin, Message: "invalid YAML", Err: err}
	}
```

`ParseBytesLenient`: same, on `interpolated`.

`ParseBytesRaw`:
```go
	var cfg domain.Config
	if err := strictUnmarshalYAML(data, &cfg); err != nil {
		return nil, &domain.ConfigError{Path: origin, Message: "invalid YAML", Err: err}
	}
```

If `yaml` becomes an unused import in `loader.go` after this, remove it; it likely stays (used elsewhere). Run `go build ./internal/config/` to check.

- [ ] **Step 4: Run the config package tests**

Run: `go test -race ./internal/config/`
Expected: PASS for the new tests. **If existing config tests now fail**, each failure is a fixture with a key not in the schema. For each: open the fixture, confirm the key is genuinely bogus (typo/stale) vs a real field missing from the struct. Fix the fixture (correct/remove the key) — do not weaken the decoder. Re-run until green.

- [ ] **Step 5: Run the FULL suite to surface exposed configs**

Run: `go build ./... && go test -race ./...`
Expected: PASS. Any failure outside `internal/config` is another place a stray key was being dropped (e.g. an HTTP or app test fixture, or the repo's `instancez.yaml` loaded by a test). Fix each fixture/config the same way. Also run `go build -o /tmp/inz ./cmd/inz && /tmp/inz validate` and confirm the repo's own `instancez.yaml` passes.

- [ ] **Step 6: Commit**

```bash
git add internal/config/loader.go internal/config/strict_test.go
# plus any fixture files you had to correct — list them explicitly
git commit -m "feat(config): reject unknown keys on all YAML decode paths"
```

---

### Task 3: strict JSON decode for the dashboard edit path

**Files:**
- Modify: `internal/config/strict.go` (add exported `UnmarshalConfigJSON`)
- Modify: `pkg/configvalidate/configvalidate.go:55`
- Modify: `internal/adapter/http/admin_handler.go` (`handlePutConfig:326`, `handlePreviewConfig:487`)
- Test: `internal/config/strict_test.go`, and the existing admin handler tests cover the wiring.

**Interfaces:**
- Produces: `func UnmarshalConfigJSON(data []byte) (*domain.Config, error)` — strict JSON decode (no defaults applied; callers apply defaults as they already do). Rejects unknown fields with a readable message.
- Consumes: `translateJSONUnknownKeyErr` (added here).

- [ ] **Step 1: Write the failing test**

Add to `internal/config/strict_test.go`:

```go
func TestUnmarshalConfigJSON_RejectsUnknownField(t *testing.T) {
	_, err := UnmarshalConfigJSON([]byte(`{"version":1,"bogus":true}`))
	if err == nil || !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("expected unknown-field error, got: %v", err)
	}
}

func TestUnmarshalConfigJSON_AcceptsValid(t *testing.T) {
	cfg, err := UnmarshalConfigJSON([]byte(`{"version":1,"tables":{"todos":{"fields":[]}}}`))
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("version = %d", cfg.Version)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestUnmarshalConfigJSON`
Expected: FAIL — `undefined: UnmarshalConfigJSON`.

- [ ] **Step 3: Implement `UnmarshalConfigJSON`**

Add to `internal/config/strict.go` (and `encoding/json` to its imports):

```go
// UnmarshalConfigJSON decodes a JSON config body, rejecting unknown fields. It
// does not apply defaults; callers that need them call ApplyDefaults afterward,
// matching the previous json.Unmarshal behavior.
func UnmarshalConfigJSON(data []byte) (*domain.Config, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var cfg domain.Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, translateJSONUnknownKeyErr(err)
	}
	return &cfg, nil
}

var jsonUnknownFieldRe = regexp.MustCompile(`^json: unknown field "(.+)"$`)

func translateJSONUnknownKeyErr(err error) error {
	if m := jsonUnknownFieldRe.FindStringSubmatch(err.Error()); m != nil {
		return fmt.Errorf("unknown key %q", m[1])
	}
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestUnmarshalConfigJSON -v`
Expected: PASS.

- [ ] **Step 5: Route the JSON call sites through it**

`pkg/configvalidate/configvalidate.go` — replace the decode in `MarshalYAML`:
```go
	cfg, err := config.UnmarshalConfigJSON(jsonBytes)
	if err != nil {
		return nil, nil, err
	}
	config.ApplyDefaults(cfg)
	if ves := config.Validate(cfg); len(ves) > 0 {
		// ...unchanged, using cfg instead of &cfg...
```
(Update the rest of the function to use `cfg` as a pointer; it already passes `&cfg` to `Validate`/`yaml.Marshal`, so switch those to `cfg`.)

`internal/adapter/http/admin_handler.go` — in both `handlePutConfig` and `handlePreviewConfig`, replace:
```go
	var newCfg domain.Config
	if err := c.ShouldBindJSON(&newCfg); err != nil {
		problemJSON(c, 400, "invalid_body", "Invalid JSON body")
		return
	}
```
with:
```go
	body, readErr := io.ReadAll(c.Request.Body)
	if readErr != nil {
		problemJSON(c, 400, "invalid_body", "Invalid JSON body")
		return
	}
	newCfgPtr, bindErr := config.UnmarshalConfigJSON(body)
	if bindErr != nil {
		problemJSON(c, 400, "invalid_body", bindErr.Error())
		return
	}
	newCfg := *newCfgPtr
```
Ensure `io` is imported in `admin_handler.go` (it is not currently — add it). The downstream `config.Validate(&newCfg)` stays unchanged.

- [ ] **Step 6: Build + test the touched packages**

Run: `go build ./... && go test -race ./internal/config/ ./pkg/configvalidate/ ./internal/adapter/http/`
Expected: PASS. If an admin handler test sent a body with an unknown field expecting success, it was relying on the silent drop — fix the test body. A test that posts a valid config must still get 200.

- [ ] **Step 7: Commit**

```bash
git add internal/config/strict.go internal/config/strict_test.go \
  pkg/configvalidate/configvalidate.go internal/adapter/http/admin_handler.go
git commit -m "feat(config): reject unknown fields on the JSON config edit path"
```

---

### Task 4: full verification

**Files:** none (verification only).

- [ ] **Step 1: Full unit + integration suite**

Run:
```bash
go build ./...
go test -race ./...
go test -tags=integration -race ./...
```
Expected: all PASS, including `TestSupabaseJSCompat`.

- [ ] **Step 2: Validate the repo config end-to-end**

Run: `go build -o /tmp/inz ./cmd/inz && /tmp/inz validate`
Expected: `✓ Schema valid`. Then sanity-check the new behavior: `printf 'version: 1\nbogus: true\n' > /tmp/bad.yaml && /tmp/inz validate --help >/dev/null` and confirm a bogus key is rejected via a quick `ParseBytes` unit assertion (already covered in Task 2).

- [ ] **Step 3: Platform smoke**

Run (from `../instancez-platform/main`): the `data` module build + `go test ./pkg/server/` (it consumes `pkg/configvalidate`). Expected: PASS — the platform's config validation now surfaces unknown-key messages too.

---

## Self-Review

**Spec coverage:** Mechanism (YAML `KnownFields`, JSON `DisallowUnknownFields`) → Tasks 1–3. Readable errors (translator, type→section map, multi-key) → Task 1. JSON paths (configvalidate + 2 admin handlers) → Task 3. Invariants (interpolation order, map keys) → preserved in Task 2 wiring + asserted by `TestStrictUnmarshalYAML_AllowsMapKeys`. Regression risk (exposed fixtures) → Task 2 Step 4–5, Task 4. All covered.

**Placeholder scan:** No TBD/TODO. The one unavoidable unknown — which existing fixtures the new strictness will expose — is handled by an explicit "run the suite, fix each exposed fixture" procedure with a stated rule (fix the fixture, never weaken the decoder), not a vague placeholder.

**Type consistency:** `strictUnmarshalYAML(data []byte, cfg *domain.Config) error`, `translateUnknownKeyErr(err) error`, and `UnmarshalConfigJSON(data []byte) (*domain.Config, error)` are named and signed identically across the tasks that define and consume them. The admin handlers use `*newCfgPtr` then `&newCfg` consistently with the existing `config.Validate(&newCfg)` call.

## Notes for the executor

- The biggest unknown is Task 2 Step 5: enabling strictness may light up fixtures across packages. Treat each as a real exposed bug; fix the data, keep the decoder strict.
- If yaml.v3's unknown-field wording doesn't match `yamlUnknownFieldRe`, the Task 1 test will fail loudly — adjust the regex to the observed string. The fallback already degrades to the raw message, so nothing silently misreports.
