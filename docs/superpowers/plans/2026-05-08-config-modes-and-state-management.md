# Config modes and state management — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire operator-controlled `--config` / `--watch` / `--watch-interval` / `--dashboard` levers, atomic transactional migrations with last-known-good fallback, an admin status endpoint exposing drift, S3-backed config with HEAD-polling watch, and dashboard UI surfaces for drift and live edits.

**Architecture:** Hexagonal Go backend already has a `config.Source` port (file + s3 implementations) and a `domain.Database` port with `Begin/Exec/Commit` tx primitives. The plan extends both: `Source` gets `Read`/`Write`/`Watch`, the migrator switches from `ExecDDL` (autocommit-per-statement) to a tx-wrapped statement loop, and the engine gains a `DriftState` tracker that survives migration failure. The HTTP layer adds a tri-state `--dashboard` gate, a `GET /_admin/config/status` endpoint, and changes `PUT /_admin/config` to migrate-first / write-source-second so DB and backend stay consistent. The dashboard SPA polls status and renders drift / edit-mode banners.

**Tech Stack:** Go 1.22+, pgx/v5, gin, fsnotify, AWS SDK v2 for S3, React 19 + TypeScript + Vite, vitest, testcontainers-go (integration tests).

---

## Spec reference

Authoritative spec: `docs/superpowers/specs/2026-05-08-config-modes-and-state-management-design.md`. Re-read before starting work; any deviation requires updating the spec first.

## File map

**New:**
- `internal/config/source_test.go` — file-source unit tests
- `internal/config/source_s3_integration_test.go` — s3-source integration tests (uses minio container)
- `internal/app/drift.go` — `DriftState` type + thread-safe tracker
- `internal/app/drift_test.go`
- `internal/adapter/http/admin_status_handler.go` — `GET /_admin/config/status`
- `internal/adapter/http/admin_status_handler_test.go`
- `dashboard/src/components/DriftBanner.tsx`
- `dashboard/src/components/DriftBanner.test.tsx`
- `dashboard/src/components/EditModeBanner.tsx`
- `dashboard/src/components/EditModeBanner.test.tsx`
- `dashboard/src/hooks/useConfigStatus.ts`
- `dashboard/src/hooks/useConfigStatus.test.ts`
- `dashboard/src/lib/downloadYaml.ts`
- `dashboard/src/lib/downloadYaml.test.ts`

**Modified:**
- `internal/config/source.go` — add `Read`, `Write`, `Watch` methods; both implementations
- `internal/cli/serve.go` — register `--watch`, `--watch-interval`, `--dashboard` + env vars + validation
- `internal/cli/dev.go` — same flags as serve, different defaults
- `internal/app/engine.go` — drift state, fallback path, dashboard mode, source wiring
- `internal/app/migrate.go` — `PlanStatements` returns `[]string`; `Apply` runs in a tx
- `internal/app/migrate_config_diff_test.go` — tests for `[]string` output
- `internal/app/migrate_test.go` — tx-rollback tests
- `internal/app/watcher.go` — uses `Source.Watch`; reload via engine fallback path
- `internal/adapter/http/admin_handler.go` — gate PUT on dashboard mode; write through Source; migrate-first
- `internal/adapter/http/dashboard.go` — accept `DashboardMode`; route gating
- `internal/adapter/http/server.go` — propagate `DashboardMode` and `Source` in `ServerDeps`
- `internal/adapter/http/middleware.go` (only if shared error helpers needed)
- `internal/domain/database.go` — no change required (Begin/Tx already exist)
- `dashboard/src/api/client.ts` — add `getConfigStatus`
- `dashboard/src/lib/types.ts` — `ConfigStatus` type
- `dashboard/src/App.tsx` — mount banners

**Untouched but referenced:**
- `internal/adapter/postgres/pool.go` — `ExecDDL` stays as-is (still used by `dbsetup.go` for role provisioning, which intentionally runs outside a tx)
- `internal/domain/database.go` `Database`, `Tx` interfaces — already have what we need

---

## Test conventions in this repo

- Unit tests: `go test -race ./<package>`, files named `*_test.go`, `package <pkg>` (not `_test`).
- Integration tests: `//go:build integration` build tag at top of file; testcontainers spins up Postgres (and minio for S3 tests). Run with `go test -tags=integration -race ./...`.
- Single test: `go test -run TestNameRegex -race ./internal/...`.
- Dashboard: `cd dashboard && npm test` (vitest run). Single test: `npm test -- src/components/DriftBanner.test.tsx`.
- Local feedback loop (must pass before commit per CLAUDE.md): `go build ./... && go test -race ./... && go test -tags=integration -race ./<changed-pkg> && (cd dashboard && npm test)`.

---

# Phase 1 — Transactional migrations and Source extension

## Task 1: Migrator returns statement slice and applies in a transaction

**Files:**
- Modify: `internal/app/migrate.go:34-39` (`Plan`) and `internal/app/migrate.go:200-246` (`Apply`)
- Modify: `internal/app/migrate_config_diff.go:43-126` and `:131-195` — `planFromScratch` and `planUpdate` return `[]string`
- Modify: `internal/app/migrate_test.go` and `internal/app/migrate_config_diff_test.go` — adjust expectations
- Test: `internal/app/migrate_test.go` (add a new test for tx rollback)

- [ ] **Step 1: Write the failing tx-rollback test**

Append to `internal/app/migrate_test.go`:

```go
func TestApplyRollsBackOnFailure(t *testing.T) {
	db := newFakeDB(t)
	m := NewMigrator(db)

	// First config applies cleanly: one table.
	cfg1 := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
	}
	if err := m.Apply(context.Background(), cfg1); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// Second config: add table "b" with a column type the fake DB rejects on
	// the second statement to simulate a mid-migration failure.
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS b"
	cfg2 := &domain.Config{
		Tables: map[string]domain.Table{
			"a": cfg1.Tables["a"],
			"b": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
	}
	err := m.Apply(context.Background(), cfg2)
	if err == nil {
		t.Fatalf("expected migration to fail")
	}

	// Critical: nothing from the failing migration should have committed.
	if db.committedStatements != db.committedStatementsAfterFirst {
		t.Fatalf("expected rollback, but %d new statements committed",
			db.committedStatements-db.committedStatementsAfterFirst)
	}
	// And the migration history must NOT have been updated.
	last, _ := db.GetLastMigration(context.Background())
	if last == nil || last.ConfigJSON == "" {
		t.Fatalf("history wiped; expected first migration to survive")
	}
	var lastCfg domain.Config
	if err := json.Unmarshal([]byte(last.ConfigJSON), &lastCfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, hasB := lastCfg.Tables["b"]; hasB {
		t.Fatalf("history shows b table; should have rolled back")
	}
}
```

(`newFakeDB` is the existing test helper in this file. It needs three new fields: `failOnStatementContaining string`, `committedStatements int`, `committedStatementsAfterFirst int`. The fake's `Begin` returns a fake `Tx` whose `Exec` checks the failure condition and increments a per-tx counter; `Commit` adds the per-tx counter to `committedStatements`. Add those fields and methods alongside the existing fake.)

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestApplyRollsBackOnFailure -race ./internal/app/...
```

Expected: FAIL — either compile error (`failOnStatementContaining` undefined on the fake) or "expected rollback, but N new statements committed" because today's `ExecDDL` is autocommit-per-statement.

- [ ] **Step 3: Add `PlanStatements` to Migrator and refactor `planFromScratch` / `planUpdate`**

In `internal/app/migrate.go`, add after `Plan`:

```go
// PlanStatements is like Plan but returns statements as a slice, so callers
// can run them inside a single transaction. Returns nil when there are no
// changes to apply.
func (m *Migrator) PlanStatements(ctx context.Context, oldCfg, newCfg *domain.Config) ([]string, error) {
	if oldCfg == nil {
		return planFromScratchStatements(newCfg, m.roles), nil
	}
	return planUpdateStatements(oldCfg, newCfg, m.roles), nil
}
```

Refactor `planFromScratch` to a private `planFromScratchStatements(cfg, roles) []string` returning the `ddl` slice directly (drop the `strings.Join`). Have the existing `planFromScratch` delegate:

```go
func planFromScratch(cfg *domain.Config, roles domain.Roles) string {
	stmts := planFromScratchStatements(cfg, roles)
	if len(stmts) == 0 {
		return ""
	}
	return strings.Join(stmts, "\n\n")
}
```

Mirror the same pattern for `planUpdate` → `planUpdateStatements`.

- [ ] **Step 4: Rewrite `Apply` to use a transaction**

Replace the body of `Apply` (`internal/app/migrate.go:200-246`) with:

```go
func (m *Migrator) Apply(ctx context.Context, cfg *domain.Config) error {
	if err := m.db.EnsureMigrationsTable(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("migrate: marshal config: %w", err)
	}
	configChecksum := fmt.Sprintf("%x", sha256.Sum256(configJSON))

	last, err := m.db.GetLastMigration(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if last != nil && last.Checksum == configChecksum {
		return nil // config unchanged
	}

	var oldCfg *domain.Config
	if last != nil && last.ConfigJSON != "" && last.ConfigJSON != "{}" {
		oldCfg = &domain.Config{}
		if err := json.Unmarshal([]byte(last.ConfigJSON), oldCfg); err != nil {
			oldCfg = nil
		}
	}

	stmts, err := m.PlanStatements(ctx, oldCfg, cfg)
	if err != nil {
		return fmt.Errorf("migrate plan: %w", err)
	}
	if len(stmts) == 0 {
		return nil
	}

	tx, err := m.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate begin: %w", err)
	}
	// Safe to defer: tx.Rollback after a successful Commit is a no-op error
	// we ignore. Calling Rollback before return guarantees no leak on panic.
	defer func() { _ = tx.Rollback(ctx) }()

	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migrate exec: %w", err)
		}
	}

	planSQL := strings.Join(stmts, "\n\n")
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate commit: %w", err)
	}

	// Record AFTER commit. If RecordMigration fails post-commit the DB schema
	// is correct but the history is missing the row — next Apply will diff
	// from the older lastGood and try to re-emit idempotent statements,
	// which is safe (CREATE OR REPLACE / IF NOT EXISTS / DROP+CREATE
	// patterns), so we accept that risk.
	if err := m.db.RecordMigration(ctx, configChecksum, planSQL, string(configJSON)); err != nil {
		return fmt.Errorf("migrate record: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Update the fake DB in the test file to support the new tx path**

In `internal/app/migrate_test.go`, extend `newFakeDB` so that `Begin` returns a `*fakeTx` whose `Exec` checks `failOnStatementContaining` and tracks per-tx statements; `Commit` adds them to `committedStatements`; `Rollback` discards them. Update existing tests that previously asserted on `ExecDDL` calls — they now need to inspect `committedStatements` instead. Add the `committedStatementsAfterFirst` snapshot to `TestApplyRollsBackOnFailure`'s setup:

```go
db.committedStatementsAfterFirst = db.committedStatements
```

right after `m.Apply(context.Background(), cfg1)` returns success.

- [ ] **Step 6: Run the test to verify it passes**

```sh
go test -run TestApplyRollsBackOnFailure -race ./internal/app/...
```

Expected: PASS.

- [ ] **Step 7: Run the full unit suite to check we didn't regress**

```sh
go test -race ./internal/app/... ./internal/config/...
```

Expected: PASS. If `migrate_test.go` had assertions on `ExecDDL` call counts that no longer fire, update them to count via `committedStatements`.

- [ ] **Step 8: Run integration tests for the migrate package**

```sh
go test -tags=integration -race -run TestMigrate ./internal/app/...
```

Expected: PASS. Real Postgres should still apply and rollback correctly.

- [ ] **Step 9: Commit**

```sh
git add internal/app/migrate.go internal/app/migrate_config_diff.go internal/app/migrate_test.go internal/app/migrate_config_diff_test.go
git commit -m "Migrator: apply DDL inside a transaction"
```

---

## Task 2: Extend the Source interface with Read and Write

**Files:**
- Modify: `internal/config/source.go` (interface + `FileSource` + `S3Source`)
- Modify: `internal/config/source_test.go` (create) — file-source unit tests
- Test: `internal/config/source_test.go`

The existing `Source.Load` returns a parsed `*domain.Config`. We need raw bytes plus a version token (mtime+size for files, ETag for S3) so `PUT /_admin/config` can do optimistic-concurrency writes and the watcher can detect changes without parsing.

- [ ] **Step 1: Write the failing FileSource Read/Write test**

Create `internal/config/source_test.go`:

```go
package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ultrabase.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestFileSourceReadWrite(t *testing.T) {
	path := writeTemp(t, "version: 1\n")
	src := &FileSource{Path: path}
	ctx := context.Background()

	data, ver1, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "version: 1") {
		t.Fatalf("unexpected content: %q", string(data))
	}
	if ver1 == "" {
		t.Fatalf("empty version token")
	}

	// Write with the matching version succeeds.
	ver2, err := src.Write(ctx, []byte("version: 1\nproject:\n  name: x\n"), ver1)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if ver2 == ver1 {
		t.Fatalf("version did not change after write")
	}

	// Writing with a stale version returns ErrConfigVersionMismatch.
	if _, err := src.Write(ctx, []byte("version: 1\n"), ver1); err != ErrConfigVersionMismatch {
		t.Fatalf("expected ErrConfigVersionMismatch, got %v", err)
	}

	// Writing with empty version (no concurrency check) always succeeds.
	if _, err := src.Write(ctx, []byte("version: 1\n"), ""); err != nil {
		t.Fatalf("write without version: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestFileSourceReadWrite -race ./internal/config/...
```

Expected: FAIL with "Read undefined" / "Write undefined" / "ErrConfigVersionMismatch undefined".

- [ ] **Step 3: Extend the Source interface**

In `internal/config/source.go`, add:

```go
import (
	// keep existing imports; add:
	"errors"
	"path/filepath"
	"time"
)

// ErrConfigVersionMismatch is returned by Source.Write when the supplied
// expected version does not match the backend's current version. Callers
// should re-Read and retry. An empty expected version skips the check.
var ErrConfigVersionMismatch = errors.New("config: version mismatch")

// Source is the abstraction over a config storage backend (local file, S3
// object, ...). All methods are safe to call concurrently.
type Source interface {
	// Load fetches, parses, and validates the config. Convenience wrapper
	// around Read + parseBytes.
	Load(ctx context.Context) (*domain.Config, error)

	// Read returns the raw bytes plus an opaque version token (mtime+size
	// for files, ETag for S3). The version token is what callers pass back
	// to Write for optimistic concurrency.
	Read(ctx context.Context) ([]byte, string, error)

	// Write writes the supplied bytes to the backend, returning the new
	// version token. If expectedVersion is non-empty and does not match the
	// backend's current version, returns ErrConfigVersionMismatch and does
	// not modify the backend.
	Write(ctx context.Context, data []byte, expectedVersion string) (string, error)

	// Describe returns a human-readable identifier for logs and errors.
	Describe() string
}
```

- [ ] **Step 4: Implement Read/Write on FileSource**

Add methods on `FileSource`:

```go
func (s *FileSource) Read(ctx context.Context) ([]byte, string, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, "", &domain.ConfigError{Path: s.Path, Message: "cannot read file", Err: err}
	}
	info, err := os.Stat(s.Path)
	if err != nil {
		return nil, "", &domain.ConfigError{Path: s.Path, Message: "cannot stat file", Err: err}
	}
	return data, fileVersionToken(info), nil
}

func (s *FileSource) Write(ctx context.Context, data []byte, expectedVersion string) (string, error) {
	if expectedVersion != "" {
		info, err := os.Stat(s.Path)
		if err != nil && !os.IsNotExist(err) {
			return "", &domain.ConfigError{Path: s.Path, Message: "cannot stat file", Err: err}
		}
		current := ""
		if err == nil {
			current = fileVersionToken(info)
		}
		if current != expectedVersion {
			return "", ErrConfigVersionMismatch
		}
	}

	dir := filepath.Dir(s.Path)
	tmp, err := os.CreateTemp(dir, ".ultrabase-config-*")
	if err != nil {
		return "", &domain.ConfigError{Path: s.Path, Message: "create temp file", Err: err}
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if rename fails.
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", &domain.ConfigError{Path: s.Path, Message: "write temp file", Err: err}
	}
	if err := tmp.Close(); err != nil {
		return "", &domain.ConfigError{Path: s.Path, Message: "close temp file", Err: err}
	}
	if err := os.Rename(tmpPath, s.Path); err != nil {
		return "", &domain.ConfigError{Path: s.Path, Message: "rename temp file", Err: err}
	}

	info, err := os.Stat(s.Path)
	if err != nil {
		return "", &domain.ConfigError{Path: s.Path, Message: "post-write stat", Err: err}
	}
	return fileVersionToken(info), nil
}

func fileVersionToken(info os.FileInfo) string {
	return fmt.Sprintf("%d-%d", info.ModTime().UnixNano(), info.Size())
}
```

(The atomic write via temp+rename ensures partial writes don't corrupt the file, and matches what `dev` expects when the watcher is enabled.)

Also rewrite the existing `Load` to delegate:

```go
func (s *FileSource) Load(ctx context.Context) (*domain.Config, error) {
	data, _, err := s.Read(ctx)
	if err != nil {
		return nil, err
	}
	return parseBytes(data, s.Path)
}
```

- [ ] **Step 5: Add a placeholder `time` import if unused** and run the unit test

```sh
go test -run TestFileSourceReadWrite -race ./internal/config/...
```

Expected: PASS. If `time` was added but not used, drop the import.

- [ ] **Step 6: Add `Read` and `Write` stubs on `S3Source` returning a clear "not implemented" error**

So that the build still passes — Task 4 fills these in:

```go
func (s *S3Source) Read(ctx context.Context) ([]byte, string, error) {
	return nil, "", fmt.Errorf("s3 source: Read not yet implemented")
}

func (s *S3Source) Write(ctx context.Context, data []byte, expectedVersion string) (string, error) {
	return "", fmt.Errorf("s3 source: Write not yet implemented")
}
```

(Keep the existing `Load` as-is; it'll be refactored in Task 4.)

- [ ] **Step 7: Build the whole project to verify the interface change didn't break anything**

```sh
go build ./...
```

Expected: success. Any code in the repo that references `config.Source` only by interface (e.g. `internal/cli/serve.go:48`) should still work because we added methods, not removed any.

- [ ] **Step 8: Commit**

```sh
git add internal/config/source.go internal/config/source_test.go
git commit -m "config.Source: add Read/Write with optimistic concurrency"
```

---

## Task 3: Implement Read/Write on S3Source with ETag-based concurrency

**Files:**
- Modify: `internal/config/source.go` (S3Source methods)
- Test: `internal/config/source_s3_integration_test.go` (new, integration build tag)

S3 supports `If-Match` header for conditional PUT and returns ETag on every response. Use that for optimistic concurrency.

- [ ] **Step 1: Write the integration test**

Create `internal/config/source_s3_integration_test.go`:

```go
//go:build integration

package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startMinIO(t *testing.T) (endpoint, accessKey, secretKey string) {
	t.Helper()
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "minio/minio:RELEASE.2024-09-13T20-26-02Z",
		ExposedPorts: []string{"9000/tcp"},
		Env: map[string]string{
			"MINIO_ROOT_USER":     "minioadmin",
			"MINIO_ROOT_PASSWORD": "minioadmin",
		},
		Cmd:        []string{"server", "/data"},
		WaitingFor: wait.ForListeningPort("9000/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	return fmt.Sprintf("http://%s:%s", host, port.Port()), "minioadmin", "minioadmin"
}

func newTestS3Source(t *testing.T, bucket, key, endpoint, ak, sk string) *S3Source {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(ak, sk, "")),
	)
	if err != nil {
		t.Fatalf("aws cfg: %v", err)
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		o.BaseEndpoint = aws.String(endpoint)
	})
	// Create bucket (idempotent).
	_, _ = client.CreateBucket(context.Background(), &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	return &S3Source{Bucket: bucket, Key: key, client: client}
}

func TestS3SourceReadWriteOptimistic(t *testing.T) {
	endpoint, ak, sk := startMinIO(t)
	src := newTestS3Source(t, "ub-test", "ultrabase.yaml", endpoint, ak, sk)
	ctx := context.Background()

	// Initial write (no expected version) seeds the object.
	ver1, err := src.Write(ctx, []byte("version: 1\n"), "")
	if err != nil {
		t.Fatalf("seed write: %v", err)
	}
	if ver1 == "" {
		t.Fatalf("empty etag")
	}

	data, ver, err := src.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(data, []byte("version: 1\n")) {
		t.Fatalf("content mismatch: %q", data)
	}
	if ver != ver1 {
		t.Fatalf("version mismatch: %q != %q", ver, ver1)
	}

	// Conditional write with the right etag succeeds.
	ver2, err := src.Write(ctx, []byte("version: 1\nproject:\n  name: x\n"), ver1)
	if err != nil {
		t.Fatalf("conditional write: %v", err)
	}
	if ver2 == ver1 {
		t.Fatalf("etag did not change")
	}

	// Conditional write with stale etag fails.
	_, err = src.Write(ctx, []byte("version: 1\n"), ver1)
	if !errors.Is(err, ErrConfigVersionMismatch) {
		t.Fatalf("expected ErrConfigVersionMismatch, got %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -tags=integration -run TestS3SourceReadWriteOptimistic -race ./internal/config/...
```

Expected: FAIL — Read returns "not yet implemented".

- [ ] **Step 3: Implement Read on S3Source**

Replace the placeholder `Read` in `internal/config/source.go`:

```go
func (s *S3Source) Read(ctx context.Context) ([]byte, string, error) {
	if err := s.ensureClient(ctx); err != nil {
		return nil, "", err
	}
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.Bucket),
		Key:    aws.String(s.Key),
	})
	if err != nil {
		return nil, "", fmt.Errorf("s3 config %s: get object: %w", s.Describe(), err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", fmt.Errorf("s3 config %s: read body: %w", s.Describe(), err)
	}
	return data, aws.ToString(out.ETag), nil
}
```

And add the `ensureClient` helper (extract from existing `Load`):

```go
func (s *S3Source) ensureClient(ctx context.Context) error {
	if s.client != nil {
		return nil
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return fmt.Errorf("s3 config %s: load aws config: %w", s.Describe(), err)
	}
	s.client = s3.NewFromConfig(awsCfg)
	return nil
}
```

Refactor existing `Load` to delegate:

```go
func (s *S3Source) Load(ctx context.Context) (*domain.Config, error) {
	data, _, err := s.Read(ctx)
	if err != nil {
		return nil, err
	}
	return parseBytes(data, s.Describe())
}
```

- [ ] **Step 4: Implement Write on S3Source with conditional PUT**

Replace the placeholder `Write`:

```go
func (s *S3Source) Write(ctx context.Context, data []byte, expectedVersion string) (string, error) {
	if err := s.ensureClient(ctx); err != nil {
		return "", err
	}
	in := &s3.PutObjectInput{
		Bucket:      aws.String(s.Bucket),
		Key:         aws.String(s.Key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/yaml"),
	}
	if expectedVersion != "" {
		in.IfMatch = aws.String(expectedVersion)
	}
	out, err := s.client.PutObject(ctx, in)
	if err != nil {
		// AWS SDK v2 returns a smithy/http error with status code 412
		// when the conditional fails. We surface it as our sentinel.
		if isPreconditionFailed(err) {
			return "", ErrConfigVersionMismatch
		}
		return "", fmt.Errorf("s3 config %s: put object: %w", s.Describe(), err)
	}
	return aws.ToString(out.ETag), nil
}

func isPreconditionFailed(err error) bool {
	var rerr *awshttp.ResponseError
	if errors.As(err, &rerr) {
		return rerr.HTTPStatusCode() == 412
	}
	// Also catch the s3.PreconditionFailed API error.
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "PreconditionFailed", "ConditionalRequestConflict":
			return true
		}
	}
	return false
}
```

Add imports as needed:

```go
import (
	"bytes"
	"errors"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/smithy-go"
)
```

(If `awshttp` and `smithy` are not already in `go.sum`, run `go mod tidy` after this step. AWS SDK v2 vendors them transitively.)

- [ ] **Step 5: Run the integration test to verify it passes**

```sh
go test -tags=integration -run TestS3SourceReadWriteOptimistic -race ./internal/config/...
```

Expected: PASS. If MinIO doesn't enforce `If-Match` semantics on a particular release, swap to a more recent image tag — MinIO has supported conditional writes since 2024-08.

- [ ] **Step 6: Build and run the whole unit suite**

```sh
go build ./... && go test -race ./...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/config/source.go internal/config/source_s3_integration_test.go go.sum go.mod
git commit -m "S3Source: ETag-based optimistic concurrency on Read/Write"
```

---

# Phase 2 — CLI flags and env vars

## Task 4: Add `--watch`, `--watch-interval`, `--dashboard` flags to `serve`

**Files:**
- Modify: `internal/cli/serve.go`
- Test: `internal/cli/serve_test.go` (create if missing)

The `--config` flag already exists and accepts s3 URIs. We need:
- `--watch` / `--no-watch` (env: `ULTRABASE_CONFIG_WATCH`, default off)
- `--watch-interval` (env: `ULTRABASE_CONFIG_WATCH_INTERVAL`, default `60s`, min `10s`)
- `--dashboard` (env: `ULTRABASE_DASHBOARD`, default `disabled`, enum)

Plus startup validation per spec.

- [ ] **Step 1: Write the failing flag-parsing test**

Create `internal/cli/serve_test.go` (or append):

```go
package cli

import (
	"strings"
	"testing"
	"time"
)

func TestParseServeFlagsDefaults(t *testing.T) {
	got, err := parseServeFlags([]string{}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.watch != false {
		t.Fatalf("default watch should be false")
	}
	if got.watchInterval != 60*time.Second {
		t.Fatalf("default watch interval should be 60s, got %v", got.watchInterval)
	}
	if got.dashboard != DashboardDisabled {
		t.Fatalf("default dashboard should be disabled, got %v", got.dashboard)
	}
}

func TestParseServeFlagsEnvFallbacks(t *testing.T) {
	env := map[string]string{
		"ULTRABASE_CONFIG_WATCH":          "true",
		"ULTRABASE_CONFIG_WATCH_INTERVAL": "30s",
		"ULTRABASE_DASHBOARD":             "readwrite",
	}
	got, err := parseServeFlags([]string{}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.watch || got.watchInterval != 30*time.Second || got.dashboard != DashboardReadwrite {
		t.Fatalf("env fallbacks ignored: %+v", got)
	}
}

func TestParseServeFlagsValidation(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"interval too low", []string{"--watch-interval", "5s"}, "must be at least 10s"},
		{"unknown dashboard mode", []string{"--dashboard", "true"}, "must be one of"},
		{"unknown URI scheme", []string{"--config", "ftp://example/file"}, "unsupported config backend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseServeFlags(tc.args, func(string) string { return "" })
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestParseServeFlags -race ./internal/cli/...
```

Expected: FAIL — `parseServeFlags`, `DashboardDisabled`, etc. are undefined.

- [ ] **Step 3: Add the dashboard mode type**

In a new file `internal/cli/flags.go`:

```go
package cli

import (
	"fmt"
	"strings"
	"time"
)

// DashboardMode controls whether and how the dashboard SPA + config-mutation
// endpoints are served.
type DashboardMode int

const (
	DashboardDisabled DashboardMode = iota
	DashboardReadonly
	DashboardReadwrite
)

func (m DashboardMode) String() string {
	switch m {
	case DashboardReadonly:
		return "readonly"
	case DashboardReadwrite:
		return "readwrite"
	default:
		return "disabled"
	}
}

func parseDashboardMode(s string) (DashboardMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "disabled":
		return DashboardDisabled, nil
	case "readonly":
		return DashboardReadonly, nil
	case "readwrite":
		return DashboardReadwrite, nil
	default:
		return DashboardDisabled, fmt.Errorf("--dashboard must be one of: disabled, readonly, readwrite (got %q)", s)
	}
}

const minWatchInterval = 10 * time.Second
```

- [ ] **Step 4: Add the parser used by both serve and dev**

Append to `internal/cli/flags.go`:

```go
// serveOptions holds the parsed values that runServe needs.
type serveOptions struct {
	port             int
	configPath       string
	loadData         bool
	migrate          bool
	allowDestructive bool

	watch         bool
	watchInterval time.Duration
	dashboard     DashboardMode
}

// parseServeFlags is split out for testability. envLookup is normally
// os.Getenv; tests pass a map-backed function.
func parseServeFlags(args []string, envLookup func(string) string) (serveOptions, error) {
	fs := newServeFlagSet()
	if err := fs.flags.Parse(args); err != nil {
		return serveOptions{}, err
	}

	opts := serveOptions{
		port:             fs.port,
		configPath:       fs.configPath,
		loadData:         fs.loadData,
		migrate:          fs.migrate,
		allowDestructive: fs.allowDestructive,
		watchInterval:    60 * time.Second,
		dashboard:        DashboardDisabled,
	}

	// Resolve --watch / env. Cobra leaves bool flags as their default (false)
	// when not passed; we only fall back to env if the flag itself wasn't set.
	if fs.flags.Changed("watch") {
		opts.watch = fs.watch
	} else if v := envLookup("ULTRABASE_CONFIG_WATCH"); v != "" {
		b, err := parseBool(v)
		if err != nil {
			return opts, fmt.Errorf("ULTRABASE_CONFIG_WATCH: %w", err)
		}
		opts.watch = b
	}

	if fs.flags.Changed("watch-interval") {
		opts.watchInterval = fs.watchInterval
	} else if v := envLookup("ULTRABASE_CONFIG_WATCH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return opts, fmt.Errorf("ULTRABASE_CONFIG_WATCH_INTERVAL: %w", err)
		}
		opts.watchInterval = d
	}
	if opts.watchInterval < minWatchInterval {
		return opts, fmt.Errorf("--watch-interval must be at least %s", minWatchInterval)
	}

	if fs.flags.Changed("dashboard") {
		m, err := parseDashboardMode(fs.dashboard)
		if err != nil {
			return opts, err
		}
		opts.dashboard = m
	} else if v := envLookup("ULTRABASE_DASHBOARD"); v != "" {
		m, err := parseDashboardMode(v)
		if err != nil {
			return opts, fmt.Errorf("ULTRABASE_DASHBOARD: %w", err)
		}
		opts.dashboard = m
	}

	// Validate URI scheme: file path or s3:// only.
	if !isFileSpec(opts.configPath) && !strings.HasPrefix(opts.configPath, "s3://") {
		return opts, fmt.Errorf("unsupported config backend: %s (only file paths and s3:// URIs are supported)", opts.configPath)
	}

	return opts, nil
}

func isFileSpec(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	return true
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "yes", "on":
		return true, nil
	case "0", "f", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean: %q", s)
	}
}
```

(`newServeFlagSet` is added next.)

- [ ] **Step 5: Add the flag set helper**

Append to `internal/cli/flags.go`:

```go
import (
	// add:
	"github.com/spf13/pflag"
)

type serveFlagSet struct {
	flags            *pflag.FlagSet
	port             int
	configPath       string
	loadData         bool
	migrate          bool
	allowDestructive bool
	watch            bool
	watchInterval    time.Duration
	dashboard        string
}

func newServeFlagSet() *serveFlagSet {
	fs := &serveFlagSet{flags: pflag.NewFlagSet("serve", pflag.ContinueOnError)}
	fs.flags.IntVar(&fs.port, "port", 0, "")
	fs.flags.StringVar(&fs.configPath, "config", "ultrabase.yaml", "")
	fs.flags.BoolVar(&fs.loadData, "data", false, "")
	fs.flags.BoolVar(&fs.migrate, "migrate", false, "")
	fs.flags.BoolVar(&fs.allowDestructive, "allow-destructive", false, "")
	fs.flags.BoolVar(&fs.watch, "watch", false, "")
	fs.flags.DurationVar(&fs.watchInterval, "watch-interval", 60*time.Second, "")
	fs.flags.StringVar(&fs.dashboard, "dashboard", "disabled", "")
	fs.flags.SetOutput(io.Discard)
	return fs
}
```

(Add `import "io"` to the file.)

- [ ] **Step 6: Run the test to verify it passes**

```sh
go test -run TestParseServeFlags -race ./internal/cli/...
```

Expected: PASS.

- [ ] **Step 7: Wire `parseServeFlags` into the cobra command**

In `internal/cli/serve.go`, replace `newServeCmd` with:

```go
func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start production server",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := parseServeFlags(extractServeArgs(cmd), os.Getenv)
			if err != nil {
				return err
			}
			return runServe(opts)
		},
	}

	cmd.Flags().Int("port", 0, "server port (default: from config or 8080)")
	cmd.Flags().String("config", "ultrabase.yaml", "config source (path or s3://bucket/key)")
	cmd.Flags().Bool("data", false, "apply CSV data imports on startup")
	cmd.Flags().Bool("migrate", false, "run pending migrations on startup")
	cmd.Flags().Bool("allow-destructive", false, "permit DROP TABLE/COLUMN in migrations")
	cmd.Flags().Bool("watch", false, "watch the config source for changes (env: ULTRABASE_CONFIG_WATCH)")
	cmd.Flags().Duration("watch-interval", 60*time.Second, "S3-watch poll interval; min 10s (env: ULTRABASE_CONFIG_WATCH_INTERVAL)")
	cmd.Flags().String("dashboard", "disabled", "dashboard mode: disabled | readonly | readwrite (env: ULTRABASE_DASHBOARD)")
	return cmd
}

// extractServeArgs reproduces the args slice that pflag uses internally so
// parseServeFlags can run on the same input. cobra has already parsed the
// flags into cmd.Flags(), but parseServeFlags needs the raw args; we build
// them back from the flag set.
func extractServeArgs(cmd *cobra.Command) []string {
	var out []string
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if f.Value.Type() == "bool" {
			if f.Value.String() == "true" {
				out = append(out, "--"+f.Name)
			} else {
				out = append(out, "--"+f.Name+"=false")
			}
			return
		}
		out = append(out, "--"+f.Name, f.Value.String())
	})
	return out
}
```

Update `runServe` signature to accept `serveOptions`:

```go
func runServe(opts serveOptions) error {
	// existing body — replace local vars (port, configPath, etc.) with opts.X
	// and add new wiring (watch, watchInterval, dashboard) — those will be
	// consumed in Phase 3.
	// ...
}
```

- [ ] **Step 8: Build and run the full test suite**

```sh
go build ./... && go test -race ./internal/cli/...
```

Expected: PASS.

- [ ] **Step 9: Commit**

```sh
git add internal/cli/flags.go internal/cli/serve.go internal/cli/serve_test.go
git commit -m "serve: add --watch, --watch-interval, --dashboard flags + env fallbacks"
```

---

## Task 5: Mirror flags on `dev` with dev-friendly defaults

**Files:**
- Modify: `internal/cli/dev.go`
- Test: `internal/cli/dev_test.go` (create if missing)

The spec mandates that `dev` exposes the same flag surface as `serve`. The defaults differ: watch on, dashboard `readwrite`. Non-flag behaviors (`.env` autoload, lenient CORS, pretty logs, interactive destructive prompts) stay tied to the command itself, not the flags.

- [ ] **Step 1: Write the failing dev flag-defaults test**

Create `internal/cli/dev_test.go`:

```go
package cli

import (
	"testing"
	"time"
)

func TestParseDevFlagsDefaults(t *testing.T) {
	got, err := parseDevFlags([]string{}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.watch != true {
		t.Fatalf("default watch in dev should be true")
	}
	if got.watchInterval != 60*time.Second {
		t.Fatalf("default watch interval should be 60s")
	}
	if got.dashboard != DashboardReadwrite {
		t.Fatalf("default dashboard in dev should be readwrite")
	}
}

func TestParseDevFlagsOverrides(t *testing.T) {
	got, err := parseDevFlags(
		[]string{"--no-watch", "--dashboard", "disabled"},
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.watch != false {
		t.Fatalf("--no-watch should turn watch off")
	}
	if got.dashboard != DashboardDisabled {
		t.Fatalf("--dashboard disabled should be honored in dev")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestParseDevFlags -race ./internal/cli/...
```

Expected: FAIL — `parseDevFlags` undefined.

- [ ] **Step 3: Add `parseDevFlags` and `devOptions` in `internal/cli/flags.go`**

Append:

```go
// devOptions are the parsed values for runDev. Same as serveOptions but
// with dev-friendly defaults already filled in.
type devOptions struct {
	serveOptions
	noWatch bool
	verbose bool
}

func parseDevFlags(args []string, envLookup func(string) string) (devOptions, error) {
	fs := newDevFlagSet()
	if err := fs.flags.Parse(args); err != nil {
		return devOptions{}, err
	}

	opts := devOptions{
		serveOptions: serveOptions{
			port:          fs.port,
			configPath:    fs.configPath,
			migrate:       true,  // dev always migrates
			loadData:      true,  // dev always seeds
			watch:         true,  // dev default
			watchInterval: 60 * time.Second,
			dashboard:     DashboardReadwrite, // dev default
		},
		noWatch: fs.noWatch,
		verbose: fs.verbose,
	}

	// --no-watch overrides the default-on watch.
	if fs.flags.Changed("no-watch") && fs.noWatch {
		opts.watch = false
	} else if fs.flags.Changed("watch") {
		opts.watch = fs.watch
	} else if v := envLookup("ULTRABASE_CONFIG_WATCH"); v != "" {
		b, err := parseBool(v)
		if err != nil {
			return opts, fmt.Errorf("ULTRABASE_CONFIG_WATCH: %w", err)
		}
		opts.watch = b
	}

	if fs.flags.Changed("watch-interval") {
		opts.watchInterval = fs.watchInterval
	} else if v := envLookup("ULTRABASE_CONFIG_WATCH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return opts, fmt.Errorf("ULTRABASE_CONFIG_WATCH_INTERVAL: %w", err)
		}
		opts.watchInterval = d
	}
	if opts.watchInterval < minWatchInterval {
		return opts, fmt.Errorf("--watch-interval must be at least %s", minWatchInterval)
	}

	if fs.flags.Changed("dashboard") {
		m, err := parseDashboardMode(fs.dashboard)
		if err != nil {
			return opts, err
		}
		opts.dashboard = m
	} else if v := envLookup("ULTRABASE_DASHBOARD"); v != "" {
		m, err := parseDashboardMode(v)
		if err != nil {
			return opts, fmt.Errorf("ULTRABASE_DASHBOARD: %w", err)
		}
		opts.dashboard = m
	}

	if !isFileSpec(opts.configPath) && !strings.HasPrefix(opts.configPath, "s3://") {
		return opts, fmt.Errorf("unsupported config backend: %s", opts.configPath)
	}

	return opts, nil
}

type devFlagSet struct {
	flags         *pflag.FlagSet
	port          int
	configPath    string
	noWatch       bool
	watch         bool
	watchInterval time.Duration
	dashboard     string
	verbose       bool
}

func newDevFlagSet() *devFlagSet {
	fs := &devFlagSet{flags: pflag.NewFlagSet("dev", pflag.ContinueOnError)}
	fs.flags.IntVar(&fs.port, "port", 0, "")
	fs.flags.StringVar(&fs.configPath, "config", "ultrabase.yaml", "")
	fs.flags.BoolVar(&fs.noWatch, "no-watch", false, "")
	fs.flags.BoolVar(&fs.watch, "watch", true, "")
	fs.flags.DurationVar(&fs.watchInterval, "watch-interval", 60*time.Second, "")
	fs.flags.StringVar(&fs.dashboard, "dashboard", "readwrite", "")
	fs.flags.BoolVar(&fs.verbose, "verbose", false, "")
	fs.flags.SetOutput(io.Discard)
	return fs
}
```

- [ ] **Step 4: Run the test to verify it passes**

```sh
go test -run TestParseDevFlags -race ./internal/cli/...
```

Expected: PASS.

- [ ] **Step 5: Wire into the cobra command**

Replace `newDevCmd` and `runDev` in `internal/cli/dev.go`:

```go
func newDevCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Start local development server with hot-reload",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := parseDevFlags(extractServeArgs(cmd), os.Getenv)
			if err != nil {
				return err
			}
			return runDev(opts)
		},
	}

	cmd.Flags().Int("port", 0, "server port (default: from config or 8080)")
	cmd.Flags().String("config", "ultrabase.yaml", "config source (path or s3://bucket/key)")
	cmd.Flags().Bool("no-watch", false, "disable hot-reload (alias for --watch=false)")
	cmd.Flags().Bool("watch", true, "watch the config source for changes")
	cmd.Flags().Duration("watch-interval", 60*time.Second, "S3-watch poll interval; min 10s")
	cmd.Flags().String("dashboard", "readwrite", "dashboard mode: disabled | readonly | readwrite")
	cmd.Flags().Bool("verbose", false, "debug logging")
	return cmd
}

func runDev(opts devOptions) error {
	// existing body, but read all values from opts.* instead of locals.
	// ...
}
```

- [ ] **Step 6: Build and run the cli tests**

```sh
go build ./... && go test -race ./internal/cli/...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/cli/flags.go internal/cli/dev.go internal/cli/dev_test.go
git commit -m "dev: same flag surface as serve, with dev-friendly defaults"
```

---

# Phase 3 — Drift state and engine wiring

## Task 6: `DriftState` type and thread-safe tracker

**Files:**
- Create: `internal/app/drift.go`
- Test: `internal/app/drift_test.go`

- [ ] **Step 1: Write the failing drift state test**

Create `internal/app/drift_test.go`:

```go
package app

import (
	"testing"
	"time"
)

func TestDriftStateMarkOK(t *testing.T) {
	d := NewDriftTracker("file://x")
	d.MarkOK("checksum-1", time.Now())
	got := d.Snapshot()
	if got.Status != DriftStatusOK {
		t.Fatalf("status = %q", got.Status)
	}
	if got.RunningChecksum != "checksum-1" {
		t.Fatalf("running checksum = %q", got.RunningChecksum)
	}
	if got.LastError != "" {
		t.Fatalf("expected empty last error, got %q", got.LastError)
	}
}

func TestDriftStateMarkDrift(t *testing.T) {
	d := NewDriftTracker("file://x")
	d.MarkOK("good", time.Now())
	d.MarkDrift("bad", "ERROR: column \"foo\" cannot be cast to type bar", time.Now())
	got := d.Snapshot()
	if got.Status != DriftStatusDrift {
		t.Fatalf("status = %q", got.Status)
	}
	if got.RunningChecksum != "good" {
		t.Fatalf("running checksum should still be 'good', got %q", got.RunningChecksum)
	}
	if got.SourceChecksum != "bad" {
		t.Fatalf("source checksum = %q", got.SourceChecksum)
	}
	if got.LastError == "" {
		t.Fatalf("last error should be set")
	}
}

func TestDriftStateClearedOnSuccess(t *testing.T) {
	d := NewDriftTracker("file://x")
	d.MarkOK("v1", time.Now())
	d.MarkDrift("v2", "boom", time.Now())
	d.MarkOK("v3", time.Now())
	got := d.Snapshot()
	if got.Status != DriftStatusOK || got.LastError != "" || got.SourceChecksum != "" {
		t.Fatalf("drift not cleared: %+v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestDriftState -race ./internal/app/...
```

Expected: FAIL — types undefined.

- [ ] **Step 3: Implement `DriftState` and `DriftTracker`**

Create `internal/app/drift.go`:

```go
package app

import (
	"sync"
	"time"
)

const (
	DriftStatusOK    = "ok"
	DriftStatusDrift = "drift"
)

// DriftState is the snapshot returned to the admin status endpoint and the
// dashboard.
type DriftState struct {
	Status           string
	ConfigSource     string
	RunningAppliedAt time.Time
	RunningChecksum  string
	SourceChecksum   string
	SourceLastSeenAt time.Time
	LastError        string
}

// DriftTracker is thread-safe.
type DriftTracker struct {
	mu     sync.RWMutex
	source string
	state  DriftState
}

func NewDriftTracker(source string) *DriftTracker {
	return &DriftTracker{
		source: source,
		state: DriftState{
			Status:       DriftStatusOK,
			ConfigSource: source,
		},
	}
}

// MarkOK records that the given config has been applied successfully and
// is now the running config. Clears any drift state.
func (t *DriftTracker) MarkOK(checksum string, appliedAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = DriftState{
		Status:           DriftStatusOK,
		ConfigSource:     t.source,
		RunningChecksum:  checksum,
		RunningAppliedAt: appliedAt,
	}
}

// MarkDrift records that the source has a new checksum that failed to apply.
// The existing running checksum/appliedAt are preserved so the snapshot
// shows what's actually live.
func (t *DriftTracker) MarkDrift(sourceChecksum, lastError string, seenAt time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Status = DriftStatusDrift
	t.state.SourceChecksum = sourceChecksum
	t.state.SourceLastSeenAt = seenAt
	t.state.LastError = lastError
}

// Snapshot returns a copy of the current state.
func (t *DriftTracker) Snapshot() DriftState {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}
```

- [ ] **Step 4: Run the test to verify it passes**

```sh
go test -run TestDriftState -race ./internal/app/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/app/drift.go internal/app/drift_test.go
git commit -m "app: DriftTracker for config-source / running-config drift"
```

---

## Task 7: Boot-time fallback to last-known-good config

**Files:**
- Modify: `internal/app/engine.go`
- Test: `internal/app/engine_test.go` (existing — extend) and an integration test `internal/app/engine_fallback_integration_test.go`

When `migrator.Apply` fails at boot, today's engine returns an error and the process exits. New behavior: load `lastGood.ConfigJSON`, run with that, set drift state. First boot with no `lastGood` still returns the error.

- [ ] **Step 1: Write the failing fallback unit test**

Append to `internal/app/engine_test.go` (or create if missing):

```go
func TestEngineFallsBackOnMigrationFailure(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	authDB := newFakeRequestDB(t)

	good := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
		Server: domain.Server{Port: 8080},
	}

	// Seed history with the good config.
	migrator := NewMigrator(db, roles)
	if err := migrator.Apply(context.Background(), good); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Now build a "bad" config that the fake DB rejects mid-migration.
	bad := &domain.Config{
		Tables: map[string]domain.Table{
			"a": good.Tables["a"],
			"b": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
		Server: domain.Server{Port: 8080},
	}
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS b"

	engine := NewEngine(bad, db, authDB, roles, WithMode(ModeProd), WithMigrate(true))
	tracker, err := engine.applyMigrationsWithFallback(context.Background())
	if err != nil {
		t.Fatalf("engine should not error on a recoverable failure: %v", err)
	}
	state := tracker.Snapshot()
	if state.Status != DriftStatusDrift {
		t.Fatalf("expected drift status, got %q", state.Status)
	}
	if state.LastError == "" {
		t.Fatalf("expected last error to be populated")
	}

	// Engine.cfg must now be the good one (with table a only).
	if _, hasB := engine.cfg.Tables["b"]; hasB {
		t.Fatalf("engine.cfg should have fallen back to good config without table b")
	}
}

func TestEngineFailsHardOnFirstBootMigrationFailure(t *testing.T) {
	db := newFakeDB(t)
	roles := domain.DefaultRoles()
	authDB := newFakeRequestDB(t)

	bad := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
	}
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS a"

	engine := NewEngine(bad, db, authDB, roles, WithMode(ModeProd), WithMigrate(true))
	if _, err := engine.applyMigrationsWithFallback(context.Background()); err == nil {
		t.Fatalf("expected hard error on first-boot failure")
	}
}
```

(`newFakeRequestDB` is whatever the existing file uses for the auth DB; if no such helper exists, pass `nil` and skip dependencies that aren't exercised in this code path.)

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestEngineFallsBack -race ./internal/app/...
go test -run TestEngineFailsHardOnFirstBoot -race ./internal/app/...
```

Expected: FAIL — `applyMigrationsWithFallback` undefined.

- [ ] **Step 3: Add `applyMigrationsWithFallback` to engine**

In `internal/app/engine.go`, add a new field to `Engine`:

```go
type Engine struct {
	// ... existing fields ...
	drift *DriftTracker
	source config.Source // populated via WithConfigSource
}
```

Add an option:

```go
func WithConfigSource(s config.Source) EngineOption {
	return func(e *Engine) { e.source = s }
}
```

Add the method:

```go
// applyMigrationsWithFallback runs migrations against e.cfg. On success,
// returns a DriftTracker in OK state. On failure, attempts to load the
// last-known-good config from _ultrabase_migrations.config_json and
// continues with it, returning a DriftTracker in drift state. If no
// last-known-good exists (first boot), returns the error.
func (e *Engine) applyMigrationsWithFallback(ctx context.Context) (*DriftTracker, error) {
	sourceDescr := "unknown"
	if e.source != nil {
		sourceDescr = e.source.Describe()
	}
	tracker := NewDriftTracker(sourceDescr)

	configJSON, _ := json.Marshal(e.cfg)
	checksum := fmt.Sprintf("%x", sha256.Sum256(configJSON))

	if err := e.migrator.Apply(ctx, e.cfg); err == nil {
		tracker.MarkOK(checksum, time.Now())
		return tracker, nil
	} else {
		applyErr := err

		last, lastErr := e.ownerDB.GetLastMigration(ctx)
		if lastErr != nil {
			return nil, fmt.Errorf("migration failed (%w) and could not load last-known-good: %v", applyErr, lastErr)
		}
		if last == nil || last.ConfigJSON == "" || last.ConfigJSON == "{}" {
			return nil, fmt.Errorf("first-boot migration failed: %w", applyErr)
		}

		var goodCfg domain.Config
		if err := json.Unmarshal([]byte(last.ConfigJSON), &goodCfg); err != nil {
			return nil, fmt.Errorf("migration failed and last-known-good is unparseable: %v (apply error: %w)", err, applyErr)
		}

		e.logger.Error("config drift: source has unapplied changes",
			"source", sourceDescr,
			"reason", applyErr.Error(),
			"running_applied_at", last.AppliedAt,
		)
		e.cfg = &goodCfg
		tracker.MarkOK(last.Checksum, last.AppliedAt)
		tracker.MarkDrift(checksum, applyErr.Error(), time.Now())
		return tracker, nil
	}
}
```

(Add imports for `crypto/sha256`, `encoding/json`, `fmt`, `time` if not already present — they are per the existing file.)

- [ ] **Step 4: Wire it into `Engine.Start`**

Replace the migration block (`engine.go:92-124`) with:

```go
	// 1. Migrate (with last-known-good fallback)
	t := time.Now()
	if e.mode == ModeDev || e.migrate {
		tracker, err := e.applyMigrationsWithFallback(ctx)
		if err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
		e.drift = tracker
		e.logger.Info("migrations applied", "duration", time.Since(t).Round(time.Millisecond))
	} else {
		// migrate=false on serve: just check, log warning if drift exists
		if err := e.ownerDB.EnsureMigrationsTable(ctx); err != nil {
			return fmt.Errorf("migration check: %w", err)
		}
		last, err := e.ownerDB.GetLastMigration(ctx)
		if err != nil {
			return fmt.Errorf("migration check: %w", err)
		}
		if last == nil {
			tracker, err := e.applyMigrationsWithFallback(ctx)
			if err != nil {
				return fmt.Errorf("initial migration failed: %w", err)
			}
			e.drift = tracker
			e.logger.Info("initial migration applied", "duration", time.Since(t).Round(time.Millisecond))
		} else {
			configJSON, _ := json.Marshal(e.cfg)
			checksum := fmt.Sprintf("%x", sha256.Sum256(configJSON))
			tracker := NewDriftTracker(e.sourceDescription())
			if last.Checksum == checksum {
				tracker.MarkOK(last.Checksum, last.AppliedAt)
			} else {
				e.logger.Warn("config has changed since last migration; run with --migrate to apply pending changes")
				tracker.MarkOK(last.Checksum, last.AppliedAt)
				tracker.MarkDrift(checksum, "config changed but --migrate not set", time.Now())
			}
			e.drift = tracker
		}
	}
```

Add the helper near the bottom of `engine.go`:

```go
func (e *Engine) sourceDescription() string {
	if e.source != nil {
		return e.source.Describe()
	}
	if e.configPath != "" {
		return e.configPath
	}
	return "unknown"
}

// Drift returns the engine's drift tracker (or nil before Start has run).
func (e *Engine) Drift() *DriftTracker {
	return e.drift
}
```

- [ ] **Step 5: Run the unit tests to verify they pass**

```sh
go test -run "TestEngineFallsBack|TestEngineFailsHardOnFirstBoot" -race ./internal/app/...
```

Expected: PASS.

- [ ] **Step 6: Run the existing engine integration tests**

```sh
go test -tags=integration -run TestEngine -race ./internal/app/...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/app/engine.go internal/app/engine_test.go
git commit -m "Engine: fall back to last-known-good config on migration failure"
```

---

## Task 8: Heartbeat logging while drifted

**Files:**
- Modify: `internal/app/engine.go`
- Test: `internal/app/engine_test.go`

The spec calls for an error-level log line every N minutes (default 10) while drift persists, so the issue doesn't get buried in log volume.

- [ ] **Step 1: Write the failing heartbeat test**

Append to `internal/app/engine_test.go`:

```go
func TestDriftHeartbeatLogsPeriodically(t *testing.T) {
	tracker := NewDriftTracker("test://x")
	tracker.MarkOK("good", time.Now())
	tracker.MarkDrift("bad", "boom", time.Now())

	var logged int32
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))
	// We can't easily intercept slog; instead, run the heartbeat with a tiny
	// interval and assert the goroutine ticks more than once.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tickCh := make(chan struct{}, 4)
	go runDriftHeartbeat(ctx, tracker, logger, 20*time.Millisecond, func() {
		atomic.AddInt32(&logged, 1)
		select {
		case tickCh <- struct{}{}:
		default:
		}
	})

	for i := 0; i < 3; i++ {
		select {
		case <-tickCh:
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("heartbeat did not tick %d times (got %d)", 3, atomic.LoadInt32(&logged))
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestDriftHeartbeat -race ./internal/app/...
```

Expected: FAIL — `runDriftHeartbeat` undefined.

- [ ] **Step 3: Implement the heartbeat**

In `internal/app/engine.go`, append:

```go
// runDriftHeartbeat logs a loud error periodically while the tracker shows
// drift, so the failure mode doesn't get buried in log volume. Returns when
// ctx is cancelled. The onTick callback is for tests; nil in production.
func runDriftHeartbeat(ctx context.Context, tracker *DriftTracker, logger *slog.Logger, interval time.Duration, onTick func()) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state := tracker.Snapshot()
			if state.Status != DriftStatusDrift {
				continue
			}
			logger.Error("config drift",
				"source", state.ConfigSource,
				"reason", state.LastError,
				"running_applied_at", state.RunningAppliedAt,
				"source_seen_at", state.SourceLastSeenAt,
			)
			if onTick != nil {
				onTick()
			}
		}
	}
}
```

Wire it into `Engine.Start` after the migration block, before HTTP starts:

```go
	// 1b. Drift heartbeat (only meaningful when in drift)
	if e.drift != nil {
		go runDriftHeartbeat(ctx, e.drift, e.logger, 10*time.Minute, nil)
	}
```

- [ ] **Step 4: Run the test to verify it passes**

```sh
go test -run TestDriftHeartbeat -race ./internal/app/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/app/engine.go internal/app/engine_test.go
git commit -m "Engine: heartbeat log every 10m while config is drifted"
```

---

## Task 9: Wire `Source` and `DashboardMode` through serve and dev into the engine and HTTP server

**Files:**
- Modify: `internal/cli/serve.go` and `internal/cli/dev.go`
- Modify: `internal/adapter/http/server.go` (extend `ServerDeps`)

So that subsequent tasks (admin handler gating, watcher) can read these from the engine and HTTP layer.

- [ ] **Step 1: Extend `ServerDeps`**

Open `internal/adapter/http/server.go` (the file containing `ServerDeps`). Add:

```go
type ServerDeps struct {
	// ... existing fields ...
	DashboardMode  cli.DashboardMode  // NOTE: avoid the import cycle — use a local int alias instead, see below
	ConfigSource   config.Source
	Drift          *app.DriftTracker
}
```

Importing `cli` from `http` would create a cycle. Define a parallel enum locally:

```go
// internal/adapter/http/dashboard_mode.go (new file)
package http

type DashboardMode int

const (
	DashboardDisabled DashboardMode = iota
	DashboardReadonly
	DashboardReadwrite
)
```

And in `cli/flags.go`, add a translation helper:

```go
import ultrahttp "github.com/saedx1/ultrabase/internal/adapter/http"

func (m DashboardMode) HTTP() ultrahttp.DashboardMode {
	switch m {
	case DashboardReadonly:
		return ultrahttp.DashboardReadonly
	case DashboardReadwrite:
		return ultrahttp.DashboardReadwrite
	default:
		return ultrahttp.DashboardDisabled
	}
}
```

- [ ] **Step 2: Update `runServe` to build the source and pass it through**

In `internal/cli/serve.go`, after parsing opts:

```go
source, err := config.NewSource(opts.configPath)
if err != nil {
	return err
}

cfg, err := source.Load(ctx)
if err != nil {
	return err
}

// ... existing validation ...

// pass to HTTP server deps and engine
httpServer := ultrahttp.NewServer(ultrahttp.ServerDeps{
	Config:         cfg,
	DB:             authDB,
	Logger:         logger,
	DevMode:        false,
	DashboardMode:  opts.dashboard.HTTP(),
	ConfigSource:   source,
	// Drift is set after engine.Start; for now leave nil and patch in
	// the same call site after engine.Drift() is populated, OR create
	// the tracker eagerly here. See the simpler approach below.
	// ...
})

engine := app.NewEngine(cfg, ownerDB, authDB, roles,
	app.WithMode(app.ModeProd),
	app.WithMigrate(opts.migrate),
	app.WithSeed(opts.loadData),
	app.WithAllowDestructive(opts.allowDestructive),
	app.WithWatch(opts.watch),
	app.WithLogger(logger),
	app.WithHTTPServer(httpServer),
	app.WithConfigSource(source),
)
```

Because `engine.Drift()` is only available after `engine.Start` returns its initial migration phase, the cleanest way to wire it into HTTP is for the HTTP admin handler to ask the engine, not the deps struct. Change `ServerDeps.Drift *app.DriftTracker` to `DriftFn func() *app.DriftTracker` (a closure):

```go
DriftFn: engine.Drift,  // returns nil before Start, populated after
```

Update the admin status handler (Task 11) to call `deps.DriftFn()` and handle nil gracefully (`{"status":"unknown"}`).

- [ ] **Step 3: Mirror the wiring in `runDev`**

Same changes in `internal/cli/dev.go`, except always `app.ModeDev` and the dev-friendly flag defaults are already in `opts`.

- [ ] **Step 4: Build to verify the wiring compiles**

```sh
go build ./...
```

Expected: success. If there's an import cycle, double-check that `cli` imports `http`, not the other way around, and that the `DashboardMode` translation is in `cli`.

- [ ] **Step 5: Commit**

```sh
git add internal/cli/serve.go internal/cli/dev.go internal/cli/flags.go internal/adapter/http/server.go internal/adapter/http/dashboard_mode.go
git commit -m "Wire ConfigSource + DashboardMode + DriftFn into HTTP server deps"
```

---

# Phase 4 — HTTP gating, PUT changes, and status endpoint

## Task 10: Gate dashboard SPA routes on dashboard mode

**Files:**
- Modify: `internal/adapter/http/dashboard.go`
- Modify: caller in `internal/adapter/http/server.go`
- Test: `internal/adapter/http/dashboard_test.go` (create or extend)

- [ ] **Step 1: Write the failing route-gating test**

Create `internal/adapter/http/dashboard_test.go`:

```go
package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDashboardDisabledReturns404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	MountDashboard(r, nil, false, DashboardDisabled)

	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when dashboard disabled, got %d", w.Code)
	}
}

func TestDashboardReadonlyServesSPA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	MountDashboard(r, nil, true, DashboardReadonly)

	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 in readonly mode (dev placeholder), got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestDashboard -race ./internal/adapter/http/...
```

Expected: FAIL — `MountDashboard` signature mismatch.

- [ ] **Step 3: Update `MountDashboard` to accept `DashboardMode`**

In `internal/adapter/http/dashboard.go`:

```go
func MountDashboard(r *gin.Engine, assets fs.FS, devMode bool, mode DashboardMode) {
	if mode == DashboardDisabled {
		// Spec: routes return 404 when disabled. Don't even register them.
		return
	}

	// existing body unchanged from here on
	if assets == nil && devMode {
		// ... existing dev placeholder ...
	}
	// ...
}
```

- [ ] **Step 4: Update the call site**

Find the call in `internal/adapter/http/server.go` (search for `MountDashboard`) and update it to pass `deps.DashboardMode`. Default to `DashboardReadwrite` in dev (already the default in the dev flag parser).

- [ ] **Step 5: Run the test to verify it passes**

```sh
go test -run TestDashboard -race ./internal/adapter/http/...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```sh
git add internal/adapter/http/dashboard.go internal/adapter/http/dashboard_test.go internal/adapter/http/server.go
git commit -m "Dashboard SPA: 404 when --dashboard=disabled"
```

---

## Task 11: Gate `PUT /api/_admin/config` on `--dashboard readwrite` and write through Source

**Files:**
- Modify: `internal/adapter/http/admin_handler.go` (`handlePutConfig`)
- Modify: `internal/adapter/http/server.go` (NewAdminHandler wiring)
- Test: `internal/adapter/http/admin_handler_test.go` (extend)

The current handler:
1. Returns 501 if `h.configPath == ""` (S3 case unsupported).
2. Returns 403 if not in dev mode.
3. Validates JSON body, marshals to YAML, writes to local file.

After this task:
1. Return `403 dashboard_disabled` if `mode == disabled`.
2. Return `403 dashboard_readonly` if `mode == readonly`.
3. When `mode == readwrite`: validate, run migration in tx (vs `lastGood`), then write via `Source.Write` with optimistic concurrency.

- [ ] **Step 1: Write the failing gating + tx-order tests**

Add to `internal/adapter/http/admin_handler_test.go`:

```go
func TestPutConfigForbidWhenDisabled(t *testing.T) {
	deps := newTestServerDeps(t)
	deps.DashboardMode = DashboardDisabled
	r := setupAdminRouter(t, deps)

	req := httptest.NewRequest("PUT", "/api/_admin/config", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 403 {
		t.Fatalf("expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "dashboard_disabled") {
		t.Fatalf("expected error code dashboard_disabled, got %s", w.Body.String())
	}
}

func TestPutConfigForbidWhenReadonly(t *testing.T) {
	deps := newTestServerDeps(t)
	deps.DashboardMode = DashboardReadonly
	r := setupAdminRouter(t, deps)

	req := httptest.NewRequest("PUT", "/api/_admin/config", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 403 || !strings.Contains(w.Body.String(), "dashboard_readonly") {
		t.Fatalf("expected dashboard_readonly 403, got %d %s", w.Code, w.Body.String())
	}
}

func TestPutConfigReadwriteRunsMigrationFirst(t *testing.T) {
	// This test asserts the order: a migration failure must NOT result in
	// the source being written. We use a fake source that records writes,
	// and a fake DB that fails on a known DDL statement.
	deps := newTestServerDeps(t)
	deps.DashboardMode = DashboardReadwrite
	src := &recordingSource{}
	deps.ConfigSource = src
	deps.DB.(*fakeRequestDB).failOnStatementContaining = "ALTER TABLE foo"
	r := setupAdminRouter(t, deps)

	body := `{"version":1,"tables":{"foo":{"fields":[{"name":"id","type":"BIGINT","primary_key":true}]}}}`
	req := httptest.NewRequest("PUT", "/api/_admin/config", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code < 400 {
		t.Fatalf("expected 4xx on migration failure, got %d", w.Code)
	}
	if src.writes != 0 {
		t.Fatalf("expected 0 writes to source on migration failure, got %d", src.writes)
	}
}
```

(`newTestServerDeps`, `setupAdminRouter`, `testAdminKey`, `recordingSource`, `fakeRequestDB` are test helpers — define them in this file or in a shared `_test.go` helper file. The fake source records `Write` calls; the fake DB lets us inject migration failure.)

- [ ] **Step 2: Run the tests to verify they fail**

```sh
go test -run TestPutConfig -race ./internal/adapter/http/...
```

Expected: FAIL — handler doesn't yet inspect `DashboardMode` or run migrations.

- [ ] **Step 3: Update `AdminHandler` struct and constructor**

In `internal/adapter/http/admin_handler.go`:

```go
type AdminHandler struct {
	cfg            *domain.Config
	configFn       func() *domain.Config // returns the LIVE engine config (lastGood when drifted)
	db             domain.Database
	logger         *slog.Logger
	configSource   config.Source
	dashboardMode  DashboardMode
	driftFn        func() *app.DriftTracker
	devMode        bool
}

func NewAdminHandler(deps ServerDeps) *AdminHandler {
	return &AdminHandler{
		cfg:           deps.Config,
		configFn:      deps.ConfigFn,
		db:            deps.DB.Database,
		logger:        deps.Logger,
		configSource:  deps.ConfigSource,
		dashboardMode: deps.DashboardMode,
		driftFn:       deps.DriftFn,
		devMode:       deps.DevMode,
	}
}

// liveConfig returns the engine's current running config. Falls back to the
// boot-time cfg if no closure was supplied (test paths).
func (h *AdminHandler) liveConfig() *domain.Config {
	if h.configFn != nil {
		if c := h.configFn(); c != nil {
			return c
		}
	}
	return h.cfg
}
```

(Drop the existing `configPath` field; it's now derived from `configSource.Describe()` for display purposes. Add `ConfigFn func() *domain.Config` to `ServerDeps` in `server.go` alongside the existing fields, and have `runServe` / `runDev` populate it via a closure that returns `engine.Config()` once that getter exists. Add `Config() *domain.Config { return e.cfg }` to `Engine` in `internal/app/engine.go`.)

- [ ] **Step 3b: Update `handleGetConfig` to use the live engine config and to work with any source**

The existing `handleGetConfig` reads from `h.configPath` which we just removed. Replace with:

```go
func (h *AdminHandler) handleGetConfig(c *gin.Context) {
	cfg := h.liveConfig()
	jsonData, err := json.Marshal(cfg)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to serialize config")
		return
	}
	var result map[string]any
	if err := json.Unmarshal(jsonData, &result); err != nil {
		problemJSON(c, 500, "internal", "Failed to round-trip config")
		return
	}

	// Surface the source's current version token as `_checksum` so PUT can
	// pass it back via If-Match. When no source is wired (test path) the
	// field is omitted.
	if h.configSource != nil {
		if _, ver, err := h.configSource.Read(c.Request.Context()); err == nil {
			result["_checksum"] = ver
		}
	}
	c.JSON(200, result)
}
```

This serializes the engine's running config (which is `lastGood` whenever the server is in drift) — directly satisfying the spec's "edit the running config, not the failing source" requirement. The `_checksum` field still uses the source's version token so the existing dashboard PUT flow (`If-Match`) keeps working.

- [ ] **Step 4: Rewrite `handlePutConfig`**

```go
func (h *AdminHandler) handlePutConfig(c *gin.Context) {
	switch h.dashboardMode {
	case DashboardDisabled:
		c.JSON(403, gin.H{
			"error":         "dashboard_disabled",
			"message":       "The dashboard is disabled. To change the configuration, update the source and restart.",
			"config_source": h.sourceDescribe(),
		})
		return
	case DashboardReadonly:
		c.JSON(403, gin.H{
			"error":         "dashboard_readonly",
			"message":       "This deployment is GitOps-managed. To change the configuration, update the source and redeploy.",
			"config_source": h.sourceDescribe(),
		})
		return
	}

	if h.configSource == nil {
		problemJSON(c, 501, "not_implemented", "Config source not available")
		return
	}

	// Read current bytes + version (for optimistic concurrency).
	currentBytes, currentVersion, err := h.configSource.Read(c.Request.Context())
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to read current config: "+err.Error())
		return
	}

	// If-Match check (optional — clients can send the version they're editing).
	if ifMatch := c.GetHeader("If-Match"); ifMatch != "" && ifMatch != currentVersion {
		c.JSON(409, gin.H{
			"error":            "conflict",
			"current_version":  currentVersion,
			"current_checksum": fmt.Sprintf("sha256:%x", sha256.Sum256(currentBytes)),
		})
		return
	}

	// Parse + validate the proposed config.
	var newCfg domain.Config
	if err := c.ShouldBindJSON(&newCfg); err != nil {
		problemJSON(c, 400, "invalid_body", "Invalid JSON body")
		return
	}
	if errs := config.Validate(&newCfg); errs != nil {
		var errList []gin.H
		for _, e := range errs {
			item := gin.H{"path": e.Path, "message": e.Message}
			if e.Suggestion != "" {
				item["suggestion"] = e.Suggestion
			}
			errList = append(errList, item)
		}
		c.JSON(400, gin.H{"errors": errList})
		return
	}

	// Migration first: run in a tx via the migrator. If it fails, leave
	// the backend untouched.
	migrator := app.NewMigrator(h.db)
	if err := migrator.Apply(c.Request.Context(), &newCfg); err != nil {
		problemJSON(c, 400, "migration_failed", err.Error())
		return
	}

	// Migration committed; now write the YAML to the backend.
	yamlData, err := yaml.Marshal(&newCfg)
	if err != nil {
		problemJSON(c, 500, "internal", "Failed to serialize config to YAML")
		return
	}
	newVersion, err := h.configSource.Write(c.Request.Context(), yamlData, currentVersion)
	if err != nil {
		// The DB has been migrated but the source write failed. Log loudly;
		// the next boot will re-read the (stale) source and migrate forward
		// again, which is idempotent for the patterns we generate.
		h.logger.Error("config source write failed after successful migration",
			"source", h.sourceDescribe(),
			"error", err.Error())
		problemJSON(c, 500, "source_write_failed", err.Error())
		return
	}

	c.JSON(200, gin.H{
		"message":       "Config saved",
		"config_source": h.sourceDescribe(),
		"new_version":   newVersion,
	})
}

func (h *AdminHandler) sourceDescribe() string {
	if h.configSource == nil {
		return ""
	}
	return h.configSource.Describe()
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```sh
go test -run TestPutConfig -race ./internal/adapter/http/...
```

Expected: PASS.

- [ ] **Step 6: Run the supabase-js compat suite to confirm no regression**

```sh
go test -run TestSupabaseJSCompat -tags=integration -race ./internal/adapter/http/...
```

Expected: PASS. This test is the load-bearing one called out in CLAUDE.md.

- [ ] **Step 7: Commit**

```sh
git add internal/adapter/http/admin_handler.go internal/adapter/http/admin_handler_test.go
git commit -m "PUT /api/_admin/config: migrate-first, then write through Source"
```

---

## Task 12: Add `GET /api/_admin/config/status`

**Files:**
- Create: `internal/adapter/http/admin_status_handler.go`
- Modify: `internal/adapter/http/admin_handler.go` (register route)
- Test: `internal/adapter/http/admin_status_handler_test.go`

- [ ] **Step 1: Write the failing status-endpoint test**

Create `internal/adapter/http/admin_status_handler_test.go`:

```go
package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/saedx1/ultrabase/internal/app"
)

func TestConfigStatusOK(t *testing.T) {
	tracker := app.NewDriftTracker("file://./ultrabase.yaml")
	tracker.MarkOK("abcd1234", time.Now())

	deps := newTestServerDeps(t)
	deps.DriftFn = func() *app.DriftTracker { return tracker }
	r := setupAdminRouter(t, deps)

	req := httptest.NewRequest("GET", "/api/_admin/config/status", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d (body %s)", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "ok" {
		t.Fatalf("status field = %v", got["status"])
	}
	running, _ := got["running"].(map[string]any)
	if running["checksum"] != "abcd1234" {
		t.Fatalf("running.checksum = %v", running["checksum"])
	}
}

func TestConfigStatusDrift(t *testing.T) {
	tracker := app.NewDriftTracker("s3://bucket/key")
	tracker.MarkOK("good", time.Now())
	tracker.MarkDrift("bad", `ERROR: column "foo" cannot be cast`, time.Now())

	deps := newTestServerDeps(t)
	deps.DriftFn = func() *app.DriftTracker { return tracker }
	r := setupAdminRouter(t, deps)

	req := httptest.NewRequest("GET", "/api/_admin/config/status", nil)
	req.Header.Set("Authorization", "Bearer "+testAdminKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "drift" {
		t.Fatalf("status = %v", got["status"])
	}
	if got["last_error"] == nil || got["last_error"] == "" {
		t.Fatalf("last_error must be set")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestConfigStatus -race ./internal/adapter/http/...
```

Expected: FAIL — route not registered.

- [ ] **Step 3: Implement the handler**

Create `internal/adapter/http/admin_status_handler.go`:

```go
package http

import (
	"github.com/gin-gonic/gin"
)

func (h *AdminHandler) handleConfigStatus(c *gin.Context) {
	if h.driftFn == nil {
		c.JSON(200, gin.H{"status": "unknown"})
		return
	}
	tracker := h.driftFn()
	if tracker == nil {
		c.JSON(200, gin.H{"status": "unknown"})
		return
	}
	state := tracker.Snapshot()
	c.JSON(200, gin.H{
		"status":        state.Status,
		"config_source": state.ConfigSource,
		"running": gin.H{
			"applied_at": state.RunningAppliedAt,
			"checksum":   state.RunningChecksum,
		},
		"source": gin.H{
			"checksum":      state.SourceChecksum,
			"last_seen_at":  state.SourceLastSeenAt,
		},
		"last_error": state.LastError,
	})
}
```

In `internal/adapter/http/admin_handler.go`'s `Mount`, register the route:

```go
admin.GET("/config/status", h.handleConfigStatus)
```

- [ ] **Step 4: Run the test to verify it passes**

```sh
go test -run TestConfigStatus -race ./internal/adapter/http/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/adapter/http/admin_status_handler.go internal/adapter/http/admin_status_handler_test.go internal/adapter/http/admin_handler.go
git commit -m "Add GET /api/_admin/config/status endpoint"
```

---

# Phase 5 — Watcher: file fsnotify and S3 HEAD-poll

## Task 13: Add `Watch` method to `Source`

**Files:**
- Modify: `internal/config/source.go`
- Test: `internal/config/source_test.go` (extend)

`Watch` is a method that returns a channel of `WatchEvent` and an error. Implementations: `FileSource` uses fsnotify; `S3Source` uses HEAD polling at the configured interval.

- [ ] **Step 1: Write the failing FileSource Watch test**

Append to `internal/config/source_test.go`:

```go
import (
	// add:
	"time"
)

func TestFileSourceWatchFiresOnChange(t *testing.T) {
	path := writeTemp(t, "version: 1\n")
	src := &FileSource{Path: path}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := src.Watch(ctx, 0) // interval ignored for files
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	// Edit the file.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte("version: 1\nproject:\n  name: x\n"), 0644)
	}()

	select {
	case ev := <-ch:
		if ev.Err != nil {
			t.Fatalf("watch event err: %v", ev.Err)
		}
		if !strings.Contains(string(ev.Data), "name: x") {
			t.Fatalf("unexpected data: %q", ev.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("watcher did not fire")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestFileSourceWatch -race ./internal/config/...
```

Expected: FAIL — `Watch` undefined.

- [ ] **Step 3: Add `Watch` to the Source interface**

In `internal/config/source.go`, append to the interface:

```go
// WatchEvent is delivered when the watcher detects a change. Either Data is
// populated with the new bytes (and Version with the new token), or Err is
// set (transient errors do NOT close the channel — the watcher keeps going).
type WatchEvent struct {
	Data    []byte
	Version string
	Err     error
}

type Source interface {
	// ... existing methods ...

	// Watch starts a background watcher that emits a WatchEvent each time
	// the source changes. The channel is closed when ctx is cancelled.
	// For S3 sources, interval controls the HEAD-poll cadence; for file
	// sources, interval is ignored (event-driven via fsnotify).
	Watch(ctx context.Context, interval time.Duration) (<-chan WatchEvent, error)
}
```

- [ ] **Step 4: Implement `Watch` on FileSource via fsnotify**

```go
import "github.com/fsnotify/fsnotify"

func (s *FileSource) Watch(ctx context.Context, _ time.Duration) (<-chan WatchEvent, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("file watch: %w", err)
	}
	if err := w.Add(s.Path); err != nil {
		w.Close()
		return nil, fmt.Errorf("file watch %s: %w", s.Path, err)
	}

	out := make(chan WatchEvent, 1)
	go func() {
		defer close(out)
		defer w.Close()

		var debounce *time.Timer
		emit := func() {
			data, ver, err := s.Read(ctx)
			select {
			case out <- WatchEvent{Data: data, Version: ver, Err: err}:
			case <-ctx.Done():
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(500*time.Millisecond, emit)
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				select {
				case out <- WatchEvent{Err: err}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}
```

- [ ] **Step 5: Implement `Watch` on S3Source via HEAD poll**

```go
func (s *S3Source) Watch(ctx context.Context, interval time.Duration) (<-chan WatchEvent, error) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	if err := s.ensureClient(ctx); err != nil {
		return nil, err
	}

	out := make(chan WatchEvent, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		// Seed: don't emit until we see a change. Capture the current ETag.
		lastVer := ""
		if head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(s.Bucket), Key: aws.String(s.Key),
		}); err == nil {
			lastVer = aws.ToString(head.ETag)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
					Bucket: aws.String(s.Bucket), Key: aws.String(s.Key),
				})
				if err != nil {
					// Spec: poll failures are warn-logged at the call site;
					// here we surface them as Err so callers can log. Don't
					// flip drift state on transient errors.
					select {
					case out <- WatchEvent{Err: fmt.Errorf("s3 head: %w", err)}:
					case <-ctx.Done():
						return
					}
					continue
				}
				ver := aws.ToString(head.ETag)
				if ver == lastVer {
					continue
				}
				data, newVer, err := s.Read(ctx)
				if err != nil {
					select {
					case out <- WatchEvent{Err: err}:
					case <-ctx.Done():
						return
					}
					continue
				}
				lastVer = newVer
				select {
				case out <- WatchEvent{Data: data, Version: newVer}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}
```

- [ ] **Step 6: Run the test to verify it passes**

```sh
go test -run TestFileSourceWatch -race ./internal/config/...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add internal/config/source.go internal/config/source_test.go
git commit -m "config.Source: Watch method (fsnotify for files, HEAD poll for s3)"
```

---

## Task 14: Engine uses `Source.Watch` and routes reloads through fallback path

**Files:**
- Modify: `internal/app/watcher.go`
- Modify: `internal/app/engine.go` (replace direct `NewConfigWatcher` call with source.Watch + reload via `applyMigrationsWithFallback`)
- Test: `internal/app/watcher_test.go` (extend)

The current `ConfigWatcher` (in `app/watcher.go`) wraps fsnotify directly and reloads via `config.Load(path)` — file-only. Now we route through `Source.Watch`, and the reload path uses the same fallback as boot.

- [ ] **Step 1: Write the failing test for source-driven reload**

Append to `internal/app/watcher_test.go`:

```go
func TestEngineReloadsFromSourceWatchAndFallsBackOnFailure(t *testing.T) {
	// Build an in-memory fake source that we can poke programmatically.
	src := &fakeSource{describe: "test://x"}
	db := newFakeDB(t)
	authDB := newFakeRequestDB(t)

	good := &domain.Config{
		Tables: map[string]domain.Table{
			"a": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
		},
		Server: domain.Server{Port: 8080},
	}
	src.cfg = good
	src.bytes, _ = yaml.Marshal(good)
	src.version = "v1"

	migrator := NewMigrator(db, domain.DefaultRoles())
	if err := migrator.Apply(context.Background(), good); err != nil {
		t.Fatalf("seed: %v", err)
	}

	engine := NewEngine(good, db, authDB, domain.DefaultRoles(),
		WithConfigSource(src), WithWatch(true), WithMode(ModeProd), WithMigrate(true))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Boot the engine far enough to set up the watcher.
	tracker, err := engine.applyMigrationsWithFallback(ctx)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	engine.drift = tracker

	// Push a "bad" config through the watch channel that fails migration.
	bad := *good
	bad.Tables = map[string]domain.Table{
		"a": good.Tables["a"],
		"b": {Fields: []domain.Field{{Name: "id", Type: "BIGINT", PrimaryKey: true}}},
	}
	badBytes, _ := yaml.Marshal(&bad)
	db.failOnStatementContaining = "CREATE TABLE IF NOT EXISTS b"

	go engine.runWatcher(ctx, 0)
	src.fire(WatchEvent{Data: badBytes, Version: "v2"})

	// Give the watcher a moment to process.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := engine.drift.Snapshot()
		if state.Status == DriftStatusDrift {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	state := engine.drift.Snapshot()
	if state.Status != DriftStatusDrift {
		t.Fatalf("expected drift after bad reload, got %+v", state)
	}
}
```

(`fakeSource` is a test helper: implements `config.Source`; `fire` injects a `WatchEvent` onto its channel. Define it alongside.)

- [ ] **Step 2: Run the test to verify it fails**

```sh
go test -run TestEngineReloadsFromSourceWatch -race ./internal/app/...
```

Expected: FAIL — `runWatcher` undefined and the source-based watcher path doesn't exist yet.

- [ ] **Step 3: Add `runWatcher` to engine**

In `internal/app/engine.go`, replace the existing `if e.watch && e.configPath != "" { ... }` block with:

```go
	// 5. Start config source watcher
	if e.watch && e.source != nil {
		go e.runWatcher(ctx, e.watchInterval)
	}
```

(Add a new field `watchInterval time.Duration` and option `WithWatchInterval(d time.Duration)`.)

Add:

```go
func WithWatchInterval(d time.Duration) EngineOption {
	return func(e *Engine) { e.watchInterval = d }
}

// runWatcher subscribes to source change events and reloads the engine on
// each one, going through the same applyMigrationsWithFallback path as boot
// so failures degrade to drift instead of crashing.
func (e *Engine) runWatcher(ctx context.Context, interval time.Duration) {
	ch, err := e.source.Watch(ctx, interval)
	if err != nil {
		e.logger.Error("config watcher failed to start", "error", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Err != nil {
				e.logger.Warn("config watch transient error", "error", ev.Err)
				continue
			}
			cfg, err := config.ParseBytes(ev.Data, e.source.Describe())
			if err != nil {
				e.logger.Error("config reload: parse failed", "error", err)
				if e.drift != nil {
					e.drift.MarkDrift(versionChecksum(ev.Data), err.Error(), time.Now())
				}
				continue
			}
			if errs := config.Validate(cfg); errs != nil {
				e.logger.Error("config reload: validation failed", "count", len(errs))
				if e.drift != nil {
					msg := errs[0].Path + ": " + errs[0].Message
					e.drift.MarkDrift(versionChecksum(ev.Data), msg, time.Now())
				}
				continue
			}
			e.cfg = cfg
			tracker, err := e.applyMigrationsWithFallback(ctx)
			if err != nil {
				e.logger.Error("config reload: migration unrecoverable", "error", err)
				continue
			}
			// Replace tracker, preserving description.
			e.drift = tracker
			e.logger.Info("config reloaded successfully", "tables", len(cfg.Tables))
		}
	}
}

func versionChecksum(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
```

`config.ParseBytes` is currently private (`parseBytes`); export it (rename → `ParseBytes`) and update internal callers.

- [ ] **Step 4: Add `WithWatchInterval` plumbing in `serve.go` / `dev.go`**

```go
engine := app.NewEngine(cfg, ownerDB, authDB, roles,
	// ... existing options ...
	app.WithWatch(opts.watch),
	app.WithWatchInterval(opts.watchInterval),
	app.WithConfigSource(source),
)
```

- [ ] **Step 5: Run the test to verify it passes**

```sh
go test -run TestEngineReloadsFromSourceWatch -race ./internal/app/...
```

Expected: PASS.

- [ ] **Step 6: Run the full unit suite to catch any regression in old `ConfigWatcher` users**

```sh
go test -race ./...
```

Expected: PASS. The old `ConfigWatcher` struct in `watcher.go` is now unused — leave it for one commit, then delete in a follow-up if desired (out of scope here).

- [ ] **Step 7: Commit**

```sh
git add internal/app/engine.go internal/app/watcher.go internal/app/watcher_test.go internal/config/source.go internal/cli/serve.go internal/cli/dev.go
git commit -m "Engine: route config reloads through Source.Watch + fallback path"
```

---

# Phase 6 — Dashboard UI

## Task 15: API client + types for `/config/status`

**Files:**
- Modify: `dashboard/src/api/client.ts`
- Modify: `dashboard/src/lib/types.ts`
- Test: `dashboard/src/api/client.test.ts` (extend)

- [ ] **Step 1: Write the failing client test**

Append to `dashboard/src/api/client.test.ts`:

```ts
import { describe, expect, it, vi, beforeEach } from "vitest";
import { getConfigStatus } from "./client";

describe("getConfigStatus", () => {
  beforeEach(() => {
    sessionStorage.setItem("ultrabase_admin_key", "test-key");
  });

  it("fetches and returns the status payload", async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        status: "drift",
        config_source: "s3://bucket/key",
        running: { checksum: "abc", applied_at: "2026-05-08T12:00:00Z" },
        source: { checksum: "def", last_seen_at: "2026-05-08T12:01:00Z" },
        last_error: "boom",
      }),
    });
    vi.stubGlobal("fetch", mockFetch);

    const got = await getConfigStatus();
    expect(got.status).toBe("drift");
    expect(got.last_error).toBe("boom");
    expect(mockFetch).toHaveBeenCalledWith(
      "/api/_admin/config/status",
      expect.objectContaining({ headers: expect.objectContaining({ Authorization: "Bearer test-key" }) }),
    );
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
cd dashboard && npm test -- src/api/client.test.ts
```

Expected: FAIL — `getConfigStatus` is undefined.

- [ ] **Step 3: Add the type and the function**

In `dashboard/src/lib/types.ts`, append:

```ts
export type ConfigStatus = {
  status: "ok" | "drift" | "unknown";
  config_source: string;
  running: { checksum: string; applied_at: string };
  source: { checksum: string; last_seen_at: string };
  last_error: string | null;
};
```

In `dashboard/src/api/client.ts`, append:

```ts
import type { ConfigStatus } from "../lib/types";

export async function getConfigStatus(): Promise<ConfigStatus> {
  return request<ConfigStatus>("/config/status");
}
```

- [ ] **Step 4: Run the test to verify it passes**

```sh
cd dashboard && npm test -- src/api/client.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add dashboard/src/api/client.ts dashboard/src/lib/types.ts dashboard/src/api/client.test.ts
git commit -m "dashboard api: getConfigStatus + ConfigStatus type"
```

---

## Task 16: `useConfigStatus` polling hook

**Files:**
- Create: `dashboard/src/hooks/useConfigStatus.ts`
- Test: `dashboard/src/hooks/useConfigStatus.test.ts`

- [ ] **Step 1: Write the failing hook test**

Create `dashboard/src/hooks/useConfigStatus.test.ts`:

```ts
import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { useConfigStatus } from "./useConfigStatus";
import * as api from "../api/client";

describe("useConfigStatus", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  it("returns the current status and re-polls", async () => {
    const get = vi.spyOn(api, "getConfigStatus")
      .mockResolvedValueOnce({
        status: "ok", config_source: "f", running: { checksum: "a", applied_at: "" },
        source: { checksum: "", last_seen_at: "" }, last_error: null,
      })
      .mockResolvedValueOnce({
        status: "drift", config_source: "f", running: { checksum: "a", applied_at: "" },
        source: { checksum: "b", last_seen_at: "" }, last_error: "boom",
      });

    const { result } = renderHook(() => useConfigStatus(50));
    await waitFor(() => expect(result.current.data?.status).toBe("ok"));

    vi.advanceTimersByTime(60);
    await waitFor(() => expect(result.current.data?.status).toBe("drift"));
    expect(get).toHaveBeenCalledTimes(2);

    vi.useRealTimers();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
cd dashboard && npm test -- src/hooks/useConfigStatus.test.ts
```

Expected: FAIL.

- [ ] **Step 3: Implement the hook**

Create `dashboard/src/hooks/useConfigStatus.ts`:

```ts
import { useEffect, useState } from "react";
import { getConfigStatus } from "../api/client";
import type { ConfigStatus } from "../lib/types";

type State = {
  data: ConfigStatus | null;
  error: Error | null;
};

export function useConfigStatus(intervalMs: number = 5000): State {
  const [state, setState] = useState<State>({ data: null, error: null });

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;

    const tick = async () => {
      try {
        const data = await getConfigStatus();
        if (!cancelled) setState({ data, error: null });
      } catch (err) {
        if (!cancelled) setState((prev) => ({ data: prev.data, error: err as Error }));
      }
      if (!cancelled) {
        timer = setTimeout(tick, intervalMs);
      }
    };
    tick();

    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [intervalMs]);

  return state;
}
```

- [ ] **Step 4: Run the test to verify it passes**

```sh
cd dashboard && npm test -- src/hooks/useConfigStatus.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add dashboard/src/hooks/useConfigStatus.ts dashboard/src/hooks/useConfigStatus.test.ts
git commit -m "dashboard: useConfigStatus polling hook"
```

---

## Task 17: `DriftBanner` component

**Files:**
- Create: `dashboard/src/components/DriftBanner.tsx`
- Test: `dashboard/src/components/DriftBanner.test.tsx`

- [ ] **Step 1: Write the failing component test**

Create `dashboard/src/components/DriftBanner.test.tsx`:

```tsx
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { DriftBanner } from "./DriftBanner";

describe("DriftBanner", () => {
  it("renders nothing when status is ok", () => {
    const { container } = render(
      <DriftBanner status={{
        status: "ok", config_source: "f",
        running: { checksum: "a", applied_at: "" },
        source: { checksum: "", last_seen_at: "" },
        last_error: null,
      }} />
    );
    expect(container.firstChild).toBeNull();
  });

  it("shows the source and error when drifted", () => {
    render(
      <DriftBanner status={{
        status: "drift",
        config_source: "s3://bucket/ultrabase.yaml",
        running: { checksum: "a", applied_at: "2026-05-08T12:00:00Z" },
        source: { checksum: "b", last_seen_at: "2026-05-08T12:01:00Z" },
        last_error: "ERROR: column \"foo\" cannot be cast",
      }} />
    );
    expect(screen.getByText(/Configuration drift/i)).toBeTruthy();
    expect(screen.getByText(/s3:\/\/bucket\/ultrabase.yaml/)).toBeTruthy();
    expect(screen.getByText(/cannot be cast/)).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
cd dashboard && npm test -- src/components/DriftBanner.test.tsx
```

Expected: FAIL.

- [ ] **Step 3: Implement the component**

Create `dashboard/src/components/DriftBanner.tsx`:

```tsx
import type { ConfigStatus } from "../lib/types";

type Props = { status: ConfigStatus | null };

export function DriftBanner({ status }: Props) {
  if (!status || status.status !== "drift") return null;
  return (
    <div
      role="alert"
      className="bg-amber-100 border-b border-amber-300 text-amber-900 px-4 py-3 text-sm"
    >
      <strong>⚠️ Configuration drift.</strong>{" "}
      The source <code>{status.config_source}</code> has changes that failed to apply:{" "}
      <code>{status.last_error}</code>. The server is running on the last successful
      config from{" "}
      <time dateTime={status.running.applied_at}>{status.running.applied_at}</time>.{" "}
      Fix the source and restart, or revert the failing change.
    </div>
  );
}
```

- [ ] **Step 4: Run the test to verify it passes**

```sh
cd dashboard && npm test -- src/components/DriftBanner.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add dashboard/src/components/DriftBanner.tsx dashboard/src/components/DriftBanner.test.tsx
git commit -m "dashboard: DriftBanner component"
```

---

## Task 18: `EditModeBanner` component

**Files:**
- Create: `dashboard/src/components/EditModeBanner.tsx`
- Test: `dashboard/src/components/EditModeBanner.test.tsx`

The banner appears whenever the dashboard is in `readwrite` mode (per spec). The component receives a flag derived from server config; for now, infer it from "the PUT endpoint returned 2xx at least once" or just from a backend hint. Simplest path: hit `GET /api/_admin/config` and check whether the dashboard mode is exposed there. To keep this task self-contained, **add a dashboard-mode field to the existing status endpoint response** and surface it to the UI. Update the spec with this if not already there.

- [ ] **Step 1: Add `dashboard_mode` to the status payload**

Modify `internal/adapter/http/admin_status_handler.go` to also include the dashboard mode. In `handleConfigStatus`:

```go
c.JSON(200, gin.H{
	// ... existing fields ...
	"dashboard_mode": h.dashboardMode.String(),
})
```

Add `String()` to `internal/adapter/http/dashboard_mode.go`:

```go
func (m DashboardMode) String() string {
	switch m {
	case DashboardReadonly:
		return "readonly"
	case DashboardReadwrite:
		return "readwrite"
	default:
		return "disabled"
	}
}
```

Update `dashboard/src/lib/types.ts`:

```ts
export type ConfigStatus = {
  // ... existing ...
  dashboard_mode: "disabled" | "readonly" | "readwrite";
};
```

- [ ] **Step 2: Write the failing component test**

Create `dashboard/src/components/EditModeBanner.test.tsx`:

```tsx
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { EditModeBanner } from "./EditModeBanner";

const base = {
  status: "ok" as const,
  config_source: "s3://bucket/key",
  running: { checksum: "a", applied_at: "" },
  source: { checksum: "", last_seen_at: "" },
  last_error: null,
};

describe("EditModeBanner", () => {
  it("renders nothing when readonly", () => {
    const { container } = render(
      <EditModeBanner status={{ ...base, dashboard_mode: "readonly" }} />
    );
    expect(container.firstChild).toBeNull();
  });

  it("shows live edit warning when readwrite", () => {
    render(<EditModeBanner status={{ ...base, dashboard_mode: "readwrite" }} />);
    expect(screen.getByText(/Live edit mode/i)).toBeTruthy();
    expect(screen.getByText(/s3:\/\/bucket\/key/)).toBeTruthy();
  });
});
```

- [ ] **Step 3: Run the test to verify it fails**

```sh
cd dashboard && npm test -- src/components/EditModeBanner.test.tsx
```

Expected: FAIL.

- [ ] **Step 4: Implement the component**

Create `dashboard/src/components/EditModeBanner.tsx`:

```tsx
import type { ConfigStatus } from "../lib/types";

type Props = { status: ConfigStatus | null };

export function EditModeBanner({ status }: Props) {
  if (!status || status.dashboard_mode !== "readwrite") return null;
  return (
    <div
      role="status"
      className="bg-blue-50 border-b border-blue-200 text-blue-900 px-4 py-3 text-sm"
    >
      <strong>Live edit mode.</strong>{" "}
      Changes you make here are written directly to <code>{status.config_source}</code>{" "}
      and applied to the database. If your team manages <code>ultrabase.yaml</code>{" "}
      in git, mirror these changes there — anything written here will be overwritten the
      next time the source is updated outside the dashboard.
    </div>
  );
}
```

- [ ] **Step 5: Run the test to verify it passes**

```sh
cd dashboard && npm test -- src/components/EditModeBanner.test.tsx
```

Expected: PASS.

- [ ] **Step 6: Commit**

```sh
git add dashboard/src/components/EditModeBanner.tsx dashboard/src/components/EditModeBanner.test.tsx dashboard/src/lib/types.ts internal/adapter/http/admin_status_handler.go internal/adapter/http/dashboard_mode.go
git commit -m "dashboard: EditModeBanner + dashboard_mode in status payload"
```

---

## Task 19: Mount banners in `App.tsx`

**Files:**
- Modify: `dashboard/src/App.tsx`
- Test: `dashboard/src/App.test.tsx` if it exists, else skip

- [ ] **Step 1: Mount `DriftBanner` and `EditModeBanner` above `<Layout>`**

In `dashboard/src/App.tsx`, inside the existing `<Layout>` component or in the surrounding wrapper, add at the top:

```tsx
import { DriftBanner } from "./components/DriftBanner";
import { EditModeBanner } from "./components/EditModeBanner";
import { useConfigStatus } from "./hooks/useConfigStatus";

// Inside the rendered JSX, near the top of the layout:
function StatusBanners() {
  const { data } = useConfigStatus();
  return (
    <>
      <DriftBanner status={data} />
      <EditModeBanner status={data} />
    </>
  );
}

// Mount once when authenticated:
{hasKey && <StatusBanners />}
```

- [ ] **Step 2: Build the dashboard to verify it compiles**

```sh
cd dashboard && npm run build
```

Expected: success.

- [ ] **Step 3: Run the full dashboard test suite**

```sh
cd dashboard && npm test
```

Expected: PASS.

- [ ] **Step 4: Commit**

```sh
git add dashboard/src/App.tsx
git commit -m "dashboard: mount drift + edit-mode banners"
```

---

## Task 19b: Save-confirmation toast after successful PUT

**Files:**
- Create: `dashboard/src/components/SaveToast.tsx`
- Create: `dashboard/src/components/SaveToast.test.tsx`
- Modify: `dashboard/src/components/SaveBar.tsx` (existing) to invoke the toast on a successful save

The spec calls for a toast message on every successful save when the dashboard is in `readwrite` mode, including the source URI and a reminder to mirror changes to git.

- [ ] **Step 1: Write the failing toast test**

Create `dashboard/src/components/SaveToast.test.tsx`:

```tsx
import { describe, expect, it, vi } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { SaveToast, showSaveToast } from "./SaveToast";

describe("SaveToast", () => {
  it("renders when triggered and disappears after the timeout", () => {
    vi.useFakeTimers();
    render(<SaveToast />);
    act(() => {
      showSaveToast({ source: "s3://bucket/key", statementCount: 3 });
    });
    expect(screen.getByText(/Saved to/)).toBeTruthy();
    expect(screen.getByText(/s3:\/\/bucket\/key/)).toBeTruthy();
    expect(screen.getByText(/3 statement/)).toBeTruthy();
    expect(screen.getByText(/update your git source/i)).toBeTruthy();

    act(() => { vi.advanceTimersByTime(8000); });
    expect(screen.queryByText(/Saved to/)).toBeNull();
    vi.useRealTimers();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
cd dashboard && npm test -- src/components/SaveToast.test.tsx
```

Expected: FAIL.

- [ ] **Step 3: Implement the toast**

Create `dashboard/src/components/SaveToast.tsx`:

```tsx
import { useEffect, useState } from "react";

type ToastState = {
  visible: boolean;
  source: string;
  statementCount: number;
};

let setStateFn: ((s: ToastState) => void) | null = null;

export function showSaveToast(opts: { source: string; statementCount: number }) {
  if (!setStateFn) return;
  setStateFn({ visible: true, source: opts.source, statementCount: opts.statementCount });
}

export function SaveToast() {
  const [state, setState] = useState<ToastState>({
    visible: false, source: "", statementCount: 0,
  });

  useEffect(() => {
    setStateFn = setState;
    return () => { setStateFn = null; };
  }, []);

  useEffect(() => {
    if (!state.visible) return;
    const t = setTimeout(() => setState((s) => ({ ...s, visible: false })), 8000);
    return () => clearTimeout(t);
  }, [state.visible]);

  if (!state.visible) return null;

  return (
    <div
      role="status"
      className="fixed bottom-4 right-4 max-w-md rounded-md bg-emerald-50 border border-emerald-200 text-emerald-900 px-4 py-3 shadow-md text-sm z-50"
    >
      <div>
        Saved to <code>{state.source}</code>. Migrations applied:{" "}
        <strong>{state.statementCount} statement(s)</strong>.
      </div>
      <div className="mt-1 text-xs text-emerald-700">
        Reminder: update your git source to match, or your next external update will revert this.
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Wire the toast into `SaveBar` (existing component) on a successful save**

Modify `dashboard/src/components/SaveBar.tsx` so that the existing save handler — after the API call returns 2xx — calls `showSaveToast`. The PUT response from Task 11 includes `config_source`; pass it through. Add a helper to count statements: count the diff response from `GET /api/_admin/config/diff` if available, otherwise fall back to "config saved" without a number.

A representative patch in the existing handler:

```tsx
import { showSaveToast } from "./SaveToast";

// inside the save handler, after the successful PUT:
const resp = await putConfig(cfg, checksum);
showSaveToast({
  source: (resp as any).config_source ?? "",
  statementCount: appliedStatementCount,
});
```

(`appliedStatementCount` is whatever counter the existing SaveBar already keeps; if there isn't one, pass `0` and the toast omits the count gracefully.)

- [ ] **Step 5: Mount `<SaveToast />` once in `App.tsx`**

Add inside the authenticated layout (next to `StatusBanners` from Task 19):

```tsx
import { SaveToast } from "./components/SaveToast";

{hasKey && (
  <>
    <StatusBanners />
    <SaveToast />
  </>
)}
```

- [ ] **Step 6: Run the toast test and the full dashboard suite**

```sh
cd dashboard && npm test -- src/components/SaveToast.test.tsx
cd dashboard && npm test
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add dashboard/src/components/SaveToast.tsx dashboard/src/components/SaveToast.test.tsx dashboard/src/components/SaveBar.tsx dashboard/src/App.tsx
git commit -m "dashboard: SaveToast on successful config save"
```

---

## Task 20: Download-current-config-as-YAML helper

**Files:**
- Create: `dashboard/src/lib/downloadYaml.ts`
- Test: `dashboard/src/lib/downloadYaml.test.ts`
- Modify: a relevant page (e.g., `dashboard/src/pages/Settings.tsx`) to expose a "Download YAML" button

- [ ] **Step 1: Write the failing util test**

Create `dashboard/src/lib/downloadYaml.test.ts`:

```ts
import { describe, expect, it, vi } from "vitest";
import { downloadYamlFromConfig } from "./downloadYaml";

describe("downloadYamlFromConfig", () => {
  it("creates an anchor element and clicks it", () => {
    const click = vi.fn();
    const anchor = { click, href: "", download: "" } as unknown as HTMLAnchorElement;
    vi.spyOn(document, "createElement").mockReturnValue(anchor);
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => "blob:fake");

    downloadYamlFromConfig({ version: 1, project: { name: "x" } } as any, "ultrabase.yaml");
    expect(click).toHaveBeenCalled();
    expect(anchor.download).toBe("ultrabase.yaml");
    expect(revoke).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

```sh
cd dashboard && npm test -- src/lib/downloadYaml.test.ts
```

Expected: FAIL.

- [ ] **Step 3: Implement the util**

Create `dashboard/src/lib/downloadYaml.ts`:

```ts
import yaml from "js-yaml";
import type { Config } from "./types";

export function downloadYamlFromConfig(cfg: Config, filename: string = "ultrabase.yaml"): void {
  // Strip metadata fields the server adds (_checksum) — they're not config.
  const clean = JSON.parse(JSON.stringify(cfg));
  delete (clean as any)._checksum;

  const text = yaml.dump(clean, { lineWidth: 120, noRefs: true });
  const blob = new Blob([text], { type: "application/yaml" });
  const url = URL.createObjectURL(blob);

  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}
```

If `js-yaml` is not in package.json, run `npm install js-yaml` and `npm install -D @types/js-yaml` from inside `dashboard/`.

- [ ] **Step 4: Run the test to verify it passes**

```sh
cd dashboard && npm test -- src/lib/downloadYaml.test.ts
```

Expected: PASS.

- [ ] **Step 5: Wire a "Download YAML" button into `Settings.tsx`**

Find the Settings page header and add a button next to other actions:

```tsx
import { downloadYamlFromConfig } from "../lib/downloadYaml";
import { getConfig } from "../api/client";

async function handleDownload() {
  const cfg = await getConfig();
  downloadYamlFromConfig(cfg);
}

<button onClick={handleDownload} className="btn-secondary">
  Download current config as YAML
</button>
```

- [ ] **Step 6: Build the dashboard to verify**

```sh
cd dashboard && npm run build && npm test
```

Expected: PASS.

- [ ] **Step 7: Commit**

```sh
git add dashboard/src/lib/downloadYaml.ts dashboard/src/lib/downloadYaml.test.ts dashboard/src/pages/Settings.tsx dashboard/package.json dashboard/package-lock.json
git commit -m "dashboard: download-current-config-as-YAML"
```

---

# Phase 7 — Final wiring and the full feedback loop

## Task 21: Run the complete CLAUDE.md feedback loop and fix any regressions

**Files:** (whatever the suite reveals)

The CLAUDE.md non-negotiable: `go build && go test -race && go test -tags=integration -race && (cd dashboard && npm test)` must all be green before declaring done.

- [ ] **Step 1: Run go build**

```sh
go build ./...
```

Expected: success.

- [ ] **Step 2: Run go unit tests**

```sh
go test -race ./...
```

Expected: PASS. Investigate and fix any failures.

- [ ] **Step 3: Run go integration tests**

```sh
go test -tags=integration -race ./...
```

Expected: PASS. The S3 source test requires Docker for MinIO; the supabase-js compat test requires Node + npm. Both are documented in CLAUDE.md as requirements.

- [ ] **Step 4: Run dashboard tests**

```sh
cd dashboard && npm test
```

Expected: PASS.

- [ ] **Step 5: Manually exercise both modes locally**

- Run `./ultrabase dev` — confirm dashboard at `http://localhost:8080/dashboard` works in readwrite mode (default), banners render appropriately.
- Run `./ultrabase serve --dashboard readwrite --watch` against the same project — confirm hot-reload still works on file save and the dashboard now writes through to the file.
- Optional: build a tiny S3 setup with MinIO; run `./ultrabase serve --config s3://test/ultrabase.yaml --watch --watch-interval 15s --dashboard readwrite` and confirm a manual `aws s3 cp` triggers reload within the configured interval.

- [ ] **Step 6: Update `CLAUDE.md` if any new gotcha emerged**

If implementation surfaced a non-obvious behavior (e.g., a config caveat with S3 + watch), add a one-line note to the `<gotchas>` block.

- [ ] **Step 7: Commit any final polish**

```sh
git add -A
git commit -m "Polish + CLAUDE.md notes for config-modes-and-state-management"
```

---

## Done criteria

- All seven task families above are committed and the four-stage feedback loop is green.
- `docs/superpowers/specs/2026-05-08-config-modes-and-state-management-design.md` is unchanged from when this plan started (no spec drift).
- `GET /api/_admin/config/status` returns sensible JSON in both `ok` and `drift` cases.
- A migration that fails at boot leaves the server running on the previous good config rather than crashing.
- `--watch` works on both `file` and `s3` backends with the documented semantics.
- The dashboard renders the drift banner when in drift, the live-edit banner in `readwrite`, and offers a YAML download.
