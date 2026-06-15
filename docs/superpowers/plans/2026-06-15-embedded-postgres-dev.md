# Embedded Postgres for `inz dev` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `--embedded-pg` to `inz dev` so a developer can run a full instancez stack with no external Postgres, and simultaneously remove the dead `--use-cloud` flag.

**Architecture:** `startEmbeddedPostgres` (new helper in `internal/cli/embedded_postgres.go`) starts a Postgres 16 process via `fergusstrange/embedded-postgres`, sets `INSTANCEZ_DATABASE_URL` in-process, then `runDev` proceeds through the existing `ensureRoles` → preflight → migrate path unchanged. `DevDBSourceCloud` and `--use-cloud` are deleted entirely; the source model becomes DSN (default) vs Embedded.

**Tech Stack:** `github.com/fergusstrange/embedded-postgres v1.34.0`, Go stdlib `net` for free-port lookup.

---

### Task 1: Remove `--use-cloud` and add the new dependency

**Files:**
- Modify: `internal/cli/flags.go` (remove `DevDBSourceCloud`, `useCloud`, flag reg, cloud mapping)
- Modify: `internal/cli/dev.go` (remove `DevDBSourceCloud` switch case)
- Modify: `internal/cli/dev_test.go` (remove cloud test, add `--use-cloud` to removed-flags test)
- Modify: `go.mod` / `go.sum` (add embedded-postgres dep)

- [ ] **Step 1: Write the failing test — `--use-cloud` must now be unknown**

In `internal/cli/dev_test.go`, replace `TestParseDevFlagsUseCloud` with an entry in `TestParseDevFlagsRemovedFlagsUnknown`:

```go
// TestParseDevFlagsUseCloud — DELETE this entire function:
// func TestParseDevFlagsUseCloud(t *testing.T) { ... }

// TestParseDevFlagsRemovedFlagsUnknown — update the flag list:
func TestParseDevFlagsRemovedFlagsUnknown(t *testing.T) {
	for _, flag := range []string{"--use-docker", "--use-cloud-ephemeral", "--use-cloud"} {
		_, err := parseDevFlags([]string{flag}, func(string) string { return "" })
		if err == nil {
			t.Errorf("%s: expected parse error for removed flag, got nil", flag)
			continue
		}
		if !strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("%s: error %q should mention 'unknown flag'", flag, err.Error())
		}
	}
}
```

- [ ] **Step 2: Run the test — expect it to fail**

```bash
go test -run TestParseDevFlagsRemovedFlagsUnknown ./internal/cli/...
```

Expected: FAIL — `--use-cloud` is still registered and won't return "unknown flag".

- [ ] **Step 3: Remove `DevDBSourceCloud` and `--use-cloud` from flags.go**

In `internal/cli/flags.go`:

Replace the `DevDBSource` const block:
```go
const (
	DevDBSourceUnset DevDBSource = iota
	DevDBSourceDSN
)
```

Remove `useCloud bool` from `devFlagSet` struct.

Remove from `newDevFlagSet()`:
```go
// DELETE this line:
fs.flags.BoolVar(&fs.useCloud, "use-cloud", false, "run against the cloud project's draft database (requires `inz init --with-cloud`)")
```

Replace the `dbSrc` resolution block in `resolveDevFlags`:
```go
// was:
dbSrc := DevDBSourceDSN
if fs.useCloud {
    dbSrc = DevDBSourceCloud
}

// becomes:
dbSrc := DevDBSourceDSN
```

- [ ] **Step 4: Remove `DevDBSourceCloud` switch case from dev.go**

In `internal/cli/dev.go`, replace the switch block:

```go
// was:
switch opts.dbSrc {
case DevDBSourceCloud:
    return fmt.Errorf("--use-cloud is not yet implemented in this build; omit it to use the DSN")
case DevDBSourceDSN:
    // default path — env-var DSN
default:
    return fmt.Errorf("internal: dev data source unset")
}

// becomes:
// (delete the entire switch — DSN is the only path until Task 3 adds Embedded)
```

- [ ] **Step 5: Add the embedded-postgres dependency**

```bash
cd /home/saedx1/repos/instancez/main
go get github.com/fergusstrange/embedded-postgres@v1.34.0
go mod tidy
```

- [ ] **Step 6: Verify tests pass**

```bash
go test -race ./internal/cli/...
```

Expected: all pass, including `TestParseDevFlagsRemovedFlagsUnknown` (now covers `--use-cloud`).

- [ ] **Step 7: Build check**

```bash
go build ./...
```

Expected: succeeds.

- [ ] **Step 8: Commit**

```bash
git add internal/cli/flags.go internal/cli/dev.go internal/cli/dev_test.go go.mod go.sum
git commit -m "feat(dev): remove dead --use-cloud flag, add embedded-postgres dep"
```

---

### Task 2: Add `--embedded-pg` / `--reset-pg` flags with tests

**Files:**
- Modify: `internal/cli/flags.go` (new source constant, new fields, new flags, pgDataDir, validation)
- Modify: `internal/cli/dev_test.go` (tests for new flags)

- [ ] **Step 1: Write failing tests for the new flags**

Add to `internal/cli/dev_test.go`:

```go
// TestParseDevFlagsEmbeddedPG verifies --embedded-pg sets DevDBSourceEmbedded.
func TestParseDevFlagsEmbeddedPG(t *testing.T) {
	got, err := parseDevFlags([]string{"--embedded-pg"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.dbSrc != DevDBSourceEmbedded {
		t.Fatalf("dbSrc = %v, want DevDBSourceEmbedded", got.dbSrc)
	}
	if got.resetPG {
		t.Fatal("resetPG should be false when --reset-pg not passed")
	}
}

// TestParseDevFlagsResetPGRequiresEmbedded verifies --reset-pg without --embedded-pg is an error.
func TestParseDevFlagsResetPGRequiresEmbedded(t *testing.T) {
	_, err := parseDevFlags([]string{"--reset-pg"}, func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error for --reset-pg without --embedded-pg")
	}
	if !strings.Contains(err.Error(), "--reset-pg requires --embedded-pg") {
		t.Fatalf("error %q should mention --reset-pg requires --embedded-pg", err.Error())
	}
}

// TestParseDevFlagsEmbeddedPGWithReset verifies --embedded-pg --reset-pg is accepted.
func TestParseDevFlagsEmbeddedPGWithReset(t *testing.T) {
	got, err := parseDevFlags([]string{"--embedded-pg", "--reset-pg"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.dbSrc != DevDBSourceEmbedded {
		t.Fatalf("dbSrc = %v, want DevDBSourceEmbedded", got.dbSrc)
	}
	if !got.resetPG {
		t.Fatal("resetPG should be true when --reset-pg is passed")
	}
}

// TestParseDevFlagsPGDataDir verifies pgDataDir is derived from configPath.
func TestParseDevFlagsPGDataDir(t *testing.T) {
	got, err := parseDevFlags(
		[]string{"--embedded-pg", "--config", "/tmp/myproject/instancez.yaml"},
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := "/tmp/myproject/pgdata"
	if got.pgDataDir != want {
		t.Fatalf("pgDataDir = %q, want %q", got.pgDataDir, want)
	}
}
```

- [ ] **Step 2: Run tests — expect them to fail**

```bash
go test -run "TestParseDevFlagsEmbeddedPG|TestParseDevFlagsResetPG|TestParseDevFlagsPGDataDir" ./internal/cli/...
```

Expected: FAIL — `DevDBSourceEmbedded` not defined, `--embedded-pg` unknown flag.

- [ ] **Step 3: Add `DevDBSourceEmbedded` and new fields to flags.go**

In `internal/cli/flags.go`, update the const block:

```go
const (
	DevDBSourceUnset    DevDBSource = iota
	DevDBSourceDSN
	DevDBSourceEmbedded
)
```

Add `pgDataDir string` and `resetPG bool` to `devOptions`:

```go
type devOptions struct {
	serveOptions
	noWatch   bool
	verbose   bool
	dbSrc     DevDBSource
	pgDataDir string
	resetPG   bool
}
```

Add `embeddedPG bool` and `resetPG bool` to `devFlagSet`:

```go
type devFlagSet struct {
	flags          *pflag.FlagSet
	port           int
	configPath     string
	noWatch        bool
	watch          bool
	watchInterval  time.Duration
	dashboard      string
	verbose        bool
	dotenvWritable bool
	dotenvPath     string

	useDSN     bool
	embeddedPG bool
	resetPG    bool
}
```

Add flag registrations in `newDevFlagSet()` after the `use-dsn` block:

```go
fs.flags.BoolVar(&fs.embeddedPG, "embedded-pg", false, "start an embedded Postgres 16 (data at ./pgdata/); no external DB needed")
fs.flags.BoolVar(&fs.resetPG, "reset-pg", false, "wipe ./pgdata/ before starting (requires --embedded-pg)")
```

Add `reset-pg` to `devNoEnvBinding` (destructive; must not be set from env):

```go
var devNoEnvBinding = map[string][]string{
	"no-watch": {},
	"use-dsn":  {},
	"reset-pg": {},
}
```

Update `resolveDevFlags` — replace the dbSrc block and add pgDataDir + validation:

```go
dbSrc := DevDBSourceDSN
if fs.embeddedPG {
    dbSrc = DevDBSourceEmbedded
}

if fs.resetPG && !fs.embeddedPG {
    return devOptions{}, fmt.Errorf("--reset-pg requires --embedded-pg")
}
```

Add `pgDataDir` to the returned `devOptions` (use `filepath` which is already imported):

```go
return devOptions{
    serveOptions: serveOptions{
        port:           fs.port,
        configPath:     fs.configPath,
        migrate:        true,
        watch:          watch,
        watchInterval:  fs.watchInterval,
        dashboard:      mode,
        dotenvWritable: fs.dotenvWritable,
        dotenvPath:     fs.dotenvPath,
    },
    noWatch:   fs.noWatch,
    verbose:   fs.verbose,
    dbSrc:     dbSrc,
    pgDataDir: filepath.Join(filepath.Dir(fs.configPath), "pgdata"),
    resetPG:   fs.resetPG,
}, nil
```

Note: `filepath` is not yet imported in `flags.go` — add it:

```go
import (
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "time"

    instancezhttp "github.com/instancez/instancez/internal/adapter/http"
    "github.com/spf13/pflag"
)
```

- [ ] **Step 4: Run tests — expect them to pass**

```bash
go test -run "TestParseDevFlagsEmbeddedPG|TestParseDevFlagsResetPG|TestParseDevFlagsPGDataDir" ./internal/cli/...
```

Expected: all PASS.

- [ ] **Step 5: Run the full CLI test suite**

```bash
go test -race ./internal/cli/...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/flags.go internal/cli/dev_test.go
git commit -m "feat(dev): add --embedded-pg and --reset-pg flags"
```

---

### Task 3: Implement `startEmbeddedPostgres` and wire into `runDev`

**Files:**
- Create: `internal/cli/embedded_postgres.go`
- Modify: `internal/cli/dev.go`

- [ ] **Step 1: Create `internal/cli/embedded_postgres.go`**

```go
package cli

import (
	"fmt"
	"net"
	"os"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

// startEmbeddedPostgres starts an embedded Postgres 16 process, using
// opts.pgDataDir as the persistent data directory. If opts.resetPG is true,
// the data directory is wiped first. Returns a stop func (call on shutdown)
// and the superuser DSN to inject as INSTANCEZ_DATABASE_URL.
func startEmbeddedPostgres(opts devOptions) (stop func(), dsn string, err error) {
	if opts.resetPG {
		if err := os.RemoveAll(opts.pgDataDir); err != nil {
			return nil, "", fmt.Errorf("reset pgdata: %w", err)
		}
	}

	port, err := freePort()
	if err != nil {
		return nil, "", fmt.Errorf("find free port for embedded Postgres: %w", err)
	}

	pg := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Version(embeddedpostgres.V16).
			DataPath(opts.pgDataDir).
			Port(port),
	)

	if err := pg.Start(); err != nil {
		return nil, "", fmt.Errorf("start embedded Postgres: %w\nhint: if this is a platform support error, use INSTANCEZ_DATABASE_URL with a full Postgres installation instead", err)
	}

	superuserDSN := fmt.Sprintf("postgres://postgres:postgres@localhost:%d/postgres?sslmode=disable", port)
	stopFn := func() { _ = pg.Stop() }
	return stopFn, superuserDSN, nil
}

// freePort returns an available TCP port on localhost.
func freePort() (uint32, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return uint32(l.Addr().(*net.TCPAddr).Port), nil
}
```

- [ ] **Step 2: Build check (no tests yet — embedded PG start is integration-level)**

```bash
go build ./internal/cli/...
```

Expected: succeeds.

- [ ] **Step 3: Wire embedded PG start into `runDev` in `dev.go`**

In `runDev`, after `requireLocalConfig` and before `config.LoadDotenv`, add the embedded PG block. The full updated beginning of `runDev` looks like this:

```go
func runDev(opts devOptions) error {
	// dev runs against a local file + local DB; reject remote config sources
	// before any preflight work so the failure is clear and immediate.
	if err := requireLocalConfig(opts.configPath); err != nil {
		return err
	}

	// Embedded Postgres: start before LoadDotenv so that INSTANCEZ_DATABASE_URL
	// is already set when loadDotenv runs (loadDotenv skips keys already in env).
	if opts.dbSrc == DevDBSourceEmbedded {
		fmt.Println("  Starting embedded Postgres 16...")
		stop, superuserDSN, err := startEmbeddedPostgres(opts)
		if err != nil {
			return err
		}
		defer stop()
		if err := os.Setenv("INSTANCEZ_DATABASE_URL", superuserDSN); err != nil {
			return fmt.Errorf("set INSTANCEZ_DATABASE_URL: %w", err)
		}
		fmt.Println("  ✓ Embedded Postgres ready")
	}

	// Preflight: load dev dotenv first so DSN env vars are visible, then run
	// fail-fast checks before any expensive work.  We include connectivity
	// and role-layout checks because dev needs a working, fully-bootstrapped
	// database to start successfully.
	_ = config.LoadDotenv(".development.env")

	// Bootstrap the role layout from a superuser DSN when the two role DSNs are
	// not already present. This runs BEFORE preflight: on a fresh superuser-only
	// database the DSN/connect/role checks would otherwise all fail. ensureRoles
	// sets the derived DSNs into the env (so the checks below read them) and
	// persists them to .development.env for the next run.
	if res, err := ensureRoles(context.Background(), os.Getenv("INSTANCEZ_DATABASE_URL"), ".development.env"); err != nil {
		return fmt.Errorf("bootstrap roles: %w", err)
	} else if res.Ran {
		fmt.Println("  ✓ Provisioned roles from INSTANCEZ_DATABASE_URL (instancez_owner + authenticator + anon/authenticated/service_role)")
		fmt.Printf("  ✓ Wrote derived owner + authenticator DSNs to %s\n", res.EnvFile)
		if res.AdminKey != "" {
			fmt.Printf("  ✓ Generated a random admin key for dashboard login (see INSTANCEZ_ADMIN_KEY in %s)\n", res.EnvFile)
		}
	}

	// ... rest of runDev unchanged from here
```

- [ ] **Step 4: Build check**

```bash
go build ./...
```

Expected: succeeds.

- [ ] **Step 5: Run unit tests**

```bash
go test -race ./internal/cli/...
```

Expected: all pass.

- [ ] **Step 6: Smoke test — embedded PG cold start**

In a temp directory with an `instancez.yaml`:

```bash
mkdir /tmp/embpg-test && cd /tmp/embpg-test
cat > instancez.yaml <<'EOF'
project:
  name: embpg-test

auth:
  enabled: false
EOF
go run /home/saedx1/repos/instancez/main/cmd/inz dev --embedded-pg
```

Expected output includes:
```
  Starting embedded Postgres 16...
  ✓ Embedded Postgres ready
  ✓ Provisioned roles from INSTANCEZ_DATABASE_URL ...
  ✓ Connected to PostgreSQL (owner + authenticator)
  API: http://localhost:8080
```

A `pgdata/` directory should appear in `/tmp/embpg-test/`.

- [ ] **Step 7: Smoke test — reset flag wipes data**

```bash
ls /tmp/embpg-test/pgdata   # should exist from previous run
go run /home/saedx1/repos/instancez/main/cmd/inz dev --embedded-pg --reset-pg
# Ctrl-C once server starts
ls /tmp/embpg-test/pgdata   # should exist again (re-initialized)
```

- [ ] **Step 8: Smoke test — `--reset-pg` without `--embedded-pg` errors**

```bash
go run /home/saedx1/repos/instancez/main/cmd/inz dev --reset-pg 2>&1
```

Expected: `Error: --reset-pg requires --embedded-pg`

- [ ] **Step 9: Run full test suite**

```bash
go test -race ./...
```

Expected: all pass.

- [ ] **Step 10: Commit**

```bash
git add internal/cli/embedded_postgres.go internal/cli/dev.go
git commit -m "feat(dev): add --embedded-pg for zero-setup local development"
```
