# Helm Chart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Helm chart at `helm/instancez/` and refactor the CLI so role provisioning runs on every startup using a single superuser URL (`INSTANCEZ_DATABASE_URL`), replacing the two-URL model.

**Architecture:** `bootstrapDB` is modified to extract the superuser's password and reuse it for all provisioned roles. `dbConnections` is refactored to always call `bootstrapDB` from `INSTANCEZ_DATABASE_URL` — removing `INSTANCEZ_OWNER_DATABASE_URL` and `INSTANCEZ_AUTH_DATABASE_URL` as external inputs. The Helm chart uses a vanilla PostgreSQL StatefulSet (no init scripts) and a chart-managed Secret with auto-generated credentials.

**Tech Stack:** Go (pgx v5), Helm v3, `postgres:17-alpine`, Kubernetes `networking.k8s.io/v1` Ingress.

**Spec:** `docs/superpowers/specs/2026-06-15-helm-chart-design.md`

---

## Task 1: Modify `bootstrapDB` to use the superuser password

**Files:**
- Modify: `internal/cli/bootstrap.go`
- Test: `internal/cli/bootstrap_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/cli/bootstrap_test.go`:

```go
func TestBootstrapDBPasswordFromDSN(t *testing.T) {
	cases := []struct {
		dsn  string
		want string
	}{
		{"postgres://super:mysecret@host:5432/db", "mysecret"},
		{"postgres://user@host/db", ""},                // no password
		{"postgres://user:p%40ss@host/db", "p@ss"},     // percent-encoded
	}
	for _, tc := range cases {
		got, err := passwordFromDSN(tc.dsn)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.dsn, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.dsn, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/... -run TestBootstrapDBPasswordFromDSN
```
Expected: FAIL — `passwordFromDSN` undefined.

- [ ] **Step 3: Add `passwordFromDSN` helper and modify `bootstrapDB`**

In `internal/cli/bootstrap.go`, replace the two `randomPassword()` calls and the password variables at the top of `bootstrapDB` with password extraction. Also change the signature to accept `roles domain.Roles` so callers can pass configured role names instead of always using defaults.

Full updated `internal/cli/bootstrap.go`:

```go
package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/instancez/instancez/internal/domain"
)

const ownerRole = "instancez_owner"

// passwordFromDSN extracts the password component from a Postgres DSN URL.
// Returns an empty string (no error) when no password is present.
func passwordFromDSN(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	pass, _ := u.User.Password()
	return pass, nil
}

// bootstrapDB connects to privilegedDSN as a CREATEROLE-capable login, ensures
// the instancez role layout exists, and returns DSNs derived from privilegedDSN
// for the owner and authenticator roles. All provisioned login roles receive the
// same password as the superuser — rotation is a single-credential operation.
//
// Idempotent: CREATE ROLE IF NOT EXISTS + ALTER ROLE on every call so the
// password is always synced to whatever the superuser URL carries.
func bootstrapDB(ctx context.Context, privilegedDSN string, roles domain.Roles) (ownerDSN, authDSN string, err error) {
	sharedPass, err := passwordFromDSN(privilegedDSN)
	if err != nil {
		return "", "", err
	}

	conn, err := pgx.Connect(ctx, privilegedDSN)
	if err != nil {
		return "", "", fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var dbName string
	if err := conn.QueryRow(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		return "", "", fmt.Errorf("resolve current database: %w", err)
	}
	dbIdent := pgx.Identifier{dbName}.Sanitize()

	apiRoles := fmt.Sprintf("%s, %s, %s", roles.Anon, roles.Authenticated, roles.Service)

	stmts := []string{
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s'
					CREATEROLE CREATEDB BYPASSRLS REPLICATION;
			ELSE
				ALTER ROLE %s WITH LOGIN PASSWORD '%s';
			END IF;
		END $$;`, ownerRole, ownerRole, sharedPass, ownerRole, sharedPass),
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s LOGIN PASSWORD '%s' NOINHERIT;
			ELSE
				ALTER ROLE %s WITH LOGIN PASSWORD '%s' NOINHERIT;
			END IF;
		END $$;`, roles.Authenticator, roles.Authenticator, sharedPass, roles.Authenticator, sharedPass),
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s NOLOGIN;
			END IF;
		END $$;`, roles.Anon, roles.Anon),
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s NOLOGIN;
			END IF;
		END $$;`, roles.Authenticated, roles.Authenticated),
		fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN
				CREATE ROLE %s NOLOGIN BYPASSRLS;
			END IF;
		END $$;`, roles.Service, roles.Service),
		fmt.Sprintf(`GRANT %s TO %s;`, apiRoles, roles.Authenticator),
		fmt.Sprintf(`ALTER DATABASE %s OWNER TO %s;`, dbIdent, ownerRole),
		fmt.Sprintf(`ALTER SCHEMA public OWNER TO %s;`, ownerRole),
		fmt.Sprintf(`GRANT ALL ON SCHEMA public TO %s;`, ownerRole),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s;`, apiRoles),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %s;`, ownerRole, apiRoles),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO %s;`, ownerRole, apiRoles),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE %s IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO %s;`, ownerRole, apiRoles),
	}
	for _, s := range stmts {
		if _, err := conn.Exec(ctx, s); err != nil {
			return "", "", fmt.Errorf("bootstrap (%s): %w", firstSQLLine(s), err)
		}
	}

	ownerDSN, err = withUserPass(privilegedDSN, ownerRole, sharedPass, dbName)
	if err != nil {
		return "", "", err
	}
	authDSN, err = withUserPass(privilegedDSN, roles.Authenticator, sharedPass, dbName)
	if err != nil {
		return "", "", err
	}
	return ownerDSN, authDSN, nil
}

// randomPassword returns a 24-byte base64-url-encoded password (~32 chars).
func randomPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// withUserPass returns rawURL rewritten with the given userinfo and explicit
// /dbname path.
func withUserPass(rawURL, user, pass, dbName string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	u.User = url.UserPassword(user, pass)
	u.Path = "/" + dbName
	return u.String(), nil
}

func firstSQLLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/cli/... -run 'TestBootstrapDB|TestWithUserPass' -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/bootstrap.go internal/cli/bootstrap_test.go
git commit -m "refactor(cli): bootstrapDB uses superuser password for all roles"
```

---

## Task 2: Refactor `dbConnections` — always provision from `INSTANCEZ_DATABASE_URL`

**Files:**
- Modify: `internal/cli/dbsetup.go`
- Modify: `internal/cli/dbsetup_test.go`
- Modify: `internal/cli/dbsetup_integration_test.go`

- [ ] **Step 1: Write the failing unit test for new `dbConnections` signature**

Replace the test body of `TestShouldBootstrap` (which tests a function we're deleting) in `internal/cli/dbsetup_test.go` with a test for the missing-superuser-URL error path:

```go
func TestDBConnectionsRequiresSuperuserURL(t *testing.T) {
	t.Setenv("INSTANCEZ_DATABASE_URL", "")
	_, _, _, err := dbConnections(context.Background(), domain.PoolConfig{Max: 2, Min: 0, IdleTimeout: "300s"})
	if err == nil {
		t.Fatal("expected error when INSTANCEZ_DATABASE_URL is not set")
	}
	if !strings.Contains(err.Error(), "INSTANCEZ_DATABASE_URL") {
		t.Errorf("error should mention INSTANCEZ_DATABASE_URL, got: %v", err)
	}
}
```

Also delete the tests for functions being removed (`TestEnsureRoles*`, `TestPersistDSNs*`, `TestShouldBootstrap`) and add the import for `"context"` and `"strings"`. The final `internal/cli/dbsetup_test.go`:

```go
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/instancez/instancez/internal/domain"
)

func TestRolesFromEnv_Defaults(t *testing.T) {
	for _, k := range []string{
		"INSTANCEZ_DB_AUTHENTICATOR_ROLE",
		"INSTANCEZ_DB_ANON_ROLE",
		"INSTANCEZ_DB_AUTHENTICATED_ROLE",
		"INSTANCEZ_DB_SERVICE_ROLE",
	} {
		t.Setenv(k, "")
	}
	got := rolesFromEnv()
	if got != domain.DefaultRoles() {
		t.Fatalf("rolesFromEnv with no env = %+v, want defaults %+v", got, domain.DefaultRoles())
	}
}

func TestRolesFromEnv_Overrides(t *testing.T) {
	t.Setenv("INSTANCEZ_DB_AUTHENTICATOR_ROLE", "rest_login")
	t.Setenv("INSTANCEZ_DB_ANON_ROLE", "guest")
	t.Setenv("INSTANCEZ_DB_AUTHENTICATED_ROLE", "member")
	t.Setenv("INSTANCEZ_DB_SERVICE_ROLE", "admin_role")

	got := rolesFromEnv()
	want := domain.Roles{
		Authenticator: "rest_login",
		Anon:          "guest",
		Authenticated: "member",
		Service:       "admin_role",
	}
	if got != want {
		t.Fatalf("rolesFromEnv = %+v, want %+v", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("custom roles failed validation: %v", err)
	}
}

func TestRolesFromEnv_PartialOverride(t *testing.T) {
	t.Setenv("INSTANCEZ_DB_AUTHENTICATOR_ROLE", "")
	t.Setenv("INSTANCEZ_DB_ANON_ROLE", "guest")
	t.Setenv("INSTANCEZ_DB_AUTHENTICATED_ROLE", "")
	t.Setenv("INSTANCEZ_DB_SERVICE_ROLE", "")

	got := rolesFromEnv()
	if got.Anon != "guest" {
		t.Errorf("anon override lost: %q", got.Anon)
	}
	if got.Authenticator != "authenticator" || got.Authenticated != "authenticated" || got.Service != "service_role" {
		t.Errorf("unset overrides should keep defaults: %+v", got)
	}
}

func TestOwnerPoolConfigShrinksPool(t *testing.T) {
	got := ownerPoolConfig(domain.PoolConfig{Max: 20, Min: 5, IdleTimeout: "300s"})
	if got.Max != 2 {
		t.Errorf("Max = %d, want 2", got.Max)
	}
	if got.Min != 0 {
		t.Errorf("Min = %d, want 0", got.Min)
	}
}

func TestOwnerPoolConfigRespectsSmallerUserMax(t *testing.T) {
	got := ownerPoolConfig(domain.PoolConfig{Max: 1, Min: 1})
	if got.Max != 1 {
		t.Errorf("Max = %d, want 1", got.Max)
	}
}

func TestDBConnectionsRequiresSuperuserURL(t *testing.T) {
	t.Setenv("INSTANCEZ_DATABASE_URL", "")
	_, _, _, err := dbConnections(context.Background(), domain.PoolConfig{Max: 2, Min: 0, IdleTimeout: "300s"})
	if err == nil {
		t.Fatal("expected error when INSTANCEZ_DATABASE_URL is not set")
	}
	if !strings.Contains(err.Error(), "INSTANCEZ_DATABASE_URL") {
		t.Errorf("error should mention INSTANCEZ_DATABASE_URL, got: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/... -run TestDBConnectionsRequiresSuperuserURL
```
Expected: FAIL — compile error because `shouldBootstrap`, `ensureRoles`, `persistDSNs` are referenced in tests that no longer exist.

- [ ] **Step 3: Rewrite `internal/cli/dbsetup.go`**

```go
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/instancez/instancez/internal/adapter/postgres"
	"github.com/instancez/instancez/internal/domain"
	"golang.org/x/sync/errgroup"
)

func ownerPoolConfig(poolCfg domain.PoolConfig) domain.PoolConfig {
	poolCfg.Min = 0
	if poolCfg.Max > 2 || poolCfg.Max == 0 {
		poolCfg.Max = 2
	}
	return poolCfg
}

// dbConnections opens the owner and authenticator pools. It connects to Postgres
// as the superuser (INSTANCEZ_DATABASE_URL), provisions all required roles
// idempotently with the superuser's password, then opens two derived pools.
func dbConnections(ctx context.Context, poolCfg domain.PoolConfig) (domain.OwnerDB, domain.RequestDB, domain.Roles, error) {
	superuserURL := os.Getenv("INSTANCEZ_DATABASE_URL")
	if superuserURL == "" {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{},
			fmt.Errorf("INSTANCEZ_DATABASE_URL not set (superuser connection required for role provisioning)")
	}

	roles := rolesFromEnv()
	if err := roles.Validate(); err != nil {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{}, err
	}

	ownerURL, authURL, err := bootstrapDB(ctx, superuserURL, roles)
	if err != nil {
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{},
			fmt.Errorf("role provisioning: %w", err)
	}

	var owner domain.OwnerDB
	var auth domain.RequestDB
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		if owner, err = postgres.NewOwner(gctx, ownerURL, ownerPoolConfig(poolCfg)); err != nil {
			return fmt.Errorf("owner pool: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		var err error
		if auth, err = postgres.NewRequest(gctx, authURL, poolCfg, roles); err != nil {
			return fmt.Errorf("auth pool: %w", err)
		}
		return nil
	})
	if err := g.Wait(); err != nil {
		if owner.Database != nil {
			_ = owner.Close()
		}
		if auth.Database != nil {
			_ = auth.Close()
		}
		return domain.OwnerDB{}, domain.RequestDB{}, domain.Roles{}, err
	}
	return owner, auth, roles, nil
}

func rolesFromEnv() domain.Roles {
	r := domain.DefaultRoles()
	if v := os.Getenv("INSTANCEZ_DB_AUTHENTICATOR_ROLE"); v != "" {
		r.Authenticator = v
	}
	if v := os.Getenv("INSTANCEZ_DB_ANON_ROLE"); v != "" {
		r.Anon = v
	}
	if v := os.Getenv("INSTANCEZ_DB_AUTHENTICATED_ROLE"); v != "" {
		r.Authenticated = v
	}
	if v := os.Getenv("INSTANCEZ_DB_SERVICE_ROLE"); v != "" {
		r.Service = v
	}
	return r
}
```

- [ ] **Step 4: Rewrite the integration test `internal/cli/dbsetup_integration_test.go`**

```go
//go:build integration

package cli

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/instancez/instancez/internal/domain"
	"github.com/instancez/instancez/internal/testutil/dbboot"
)

// TestDBConnectionsProvisionRoles drives the full startup path against a raw
// superuser container. dbConnections must provision the role layout and return
// working pools.
func TestDBConnectionsProvisionRoles(t *testing.T) {
	superURL := dbboot.StartRawContainer(t)
	t.Setenv("INSTANCEZ_DATABASE_URL", superURL)

	ctx := context.Background()
	owner, auth, _, err := dbConnections(ctx, domain.PoolConfig{Max: 2, Min: 0, IdleTimeout: "300s"})
	if err != nil {
		t.Fatalf("dbConnections: %v", err)
	}
	defer func() { _ = owner.Close() }()
	defer func() { _ = auth.Close() }()

	// Connect as owner and verify all five roles exist.
	ownerURL, _, err := bootstrapDB(ctx, superURL, domain.DefaultRoles())
	// bootstrapDB is idempotent — a second call just verifies state
	if err != nil {
		t.Fatalf("second bootstrapDB call: %v", err)
	}
	conn, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		t.Fatalf("connect as owner: %v", err)
	}
	defer conn.Close(ctx)

	for _, role := range []string{"instancez_owner", "authenticator", "anon", "authenticated", "service_role"} {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
			t.Fatalf("query role %s: %v", role, err)
		}
		if !exists {
			t.Errorf("role %q was not provisioned", role)
		}
	}
}

// TestDBConnectionsPasswordSyncsOnRestart verifies that when the superuser
// password changes, the next dbConnections call re-provisions the roles
// with the new password and the pools reconnect successfully.
func TestDBConnectionsPasswordSyncsOnRestart(t *testing.T) {
	superURL := dbboot.StartRawContainer(t)
	t.Setenv("INSTANCEZ_DATABASE_URL", superURL)

	ctx := context.Background()

	// First call: provision roles.
	owner1, auth1, _, err := dbConnections(ctx, domain.PoolConfig{Max: 2, Min: 0, IdleTimeout: "300s"})
	if err != nil {
		t.Fatalf("first dbConnections: %v", err)
	}
	_ = owner1.Close()
	_ = auth1.Close()

	// Simulate password change by running the provisioning again (bootstrapDB
	// always updates passwords via ALTER ROLE, so it is itself idempotent —
	// what matters is that re-calling dbConnections works without error).
	owner2, auth2, _, err := dbConnections(ctx, domain.PoolConfig{Max: 2, Min: 0, IdleTimeout: "300s"})
	if err != nil {
		t.Fatalf("second dbConnections: %v", err)
	}
	_ = owner2.Close()
	_ = auth2.Close()
}
```

- [ ] **Step 5: Run unit tests**

```bash
go test ./internal/cli/... -run 'TestDBConnections|TestRolesFromEnv|TestOwnerPool' -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/dbsetup.go internal/cli/dbsetup_test.go internal/cli/dbsetup_integration_test.go
git commit -m "refactor(cli): dbConnections always provisions roles from INSTANCEZ_DATABASE_URL"
```

---

## Task 3: Update `dev.go` — remove `ensureRoles`, add `ensureAdminKey`

**Files:**
- Modify: `internal/cli/dev.go`
- Modify: `internal/cli/dbsetup.go` (add `ensureAdminKey`)

The `ensureRoles` call in `runDev` is removed — role provisioning now happens inside `dbConnections`. Admin key generation is extracted to a dedicated `ensureAdminKey` function.

- [ ] **Step 1: Add `ensureAdminKey` to `internal/cli/dbsetup.go`**

Append to the end of `internal/cli/dbsetup.go`:

```go
// ensureAdminKey generates a random INSTANCEZ_ADMIN_KEY and appends it to
// envFile when neither the env var nor the file already carry one.
func ensureAdminKey(envFile string) (generated bool, err error) {
	if os.Getenv("INSTANCEZ_ADMIN_KEY") != "" {
		return false, nil
	}
	key, err := randomPassword()
	if err != nil {
		return false, fmt.Errorf("generate admin key: %w", err)
	}
	f, err := os.OpenFile(envFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false, fmt.Errorf("write %s: %w", envFile, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "INSTANCEZ_ADMIN_KEY=%s\n", key); err != nil {
		return false, fmt.Errorf("write admin key: %w", err)
	}
	if err := os.Setenv("INSTANCEZ_ADMIN_KEY", key); err != nil {
		return false, fmt.Errorf("set env INSTANCEZ_ADMIN_KEY: %w", err)
	}
	return true, nil
}
```

- [ ] **Step 2: Write a test for `ensureAdminKey`**

Add to `internal/cli/dbsetup_test.go`:

```go
func TestEnsureAdminKeyGeneratesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".development.env")
	t.Setenv("INSTANCEZ_ADMIN_KEY", "")
	if err := os.Unsetenv("INSTANCEZ_ADMIN_KEY"); err != nil {
		t.Fatal(err)
	}

	generated, err := ensureAdminKey(envFile)
	if err != nil {
		t.Fatalf("ensureAdminKey: %v", err)
	}
	if !generated {
		t.Error("expected generated=true when key absent")
	}
	if os.Getenv("INSTANCEZ_ADMIN_KEY") == "" {
		t.Error("INSTANCEZ_ADMIN_KEY not set in env after generation")
	}

	// Idempotent: a second call should skip generation.
	generated2, err := ensureAdminKey(envFile)
	if err != nil {
		t.Fatalf("second ensureAdminKey: %v", err)
	}
	if generated2 {
		t.Error("second call should not regenerate when key already set")
	}
}
```

Add `"os"` and `"path/filepath"` to the imports in `dbsetup_test.go` (needed by `TestEnsureAdminKey` — both were omitted from the rewritten file in Task 2 Step 1).

- [ ] **Step 3: Run test**

```bash
go test ./internal/cli/... -run TestEnsureAdminKey -v
```
Expected: PASS.

- [ ] **Step 4: Update `runDev` in `internal/cli/dev.go`**

Remove the entire `ensureRoles` block (lines 57–65 in the current file). Replace with a call to `ensureAdminKey`. The updated section of `runDev` becomes:

```go
func runDev(opts devOptions) error {
	if err := requireLocalConfig(opts.configPath); err != nil {
		return err
	}

	_ = config.LoadDotenv(".development.env")

	// Generate a random admin key on first run if none is configured.
	if generated, err := ensureAdminKey(".development.env"); err != nil {
		return fmt.Errorf("admin key: %w", err)
	} else if generated {
		fmt.Printf("  ✓ Generated random admin key → .development.env\n")
	}

	if r, failed := preflight.RunUntilFail([]preflight.Check{
		preflight.ConfigValidCheck(opts.configPath),
		preflight.SuperuserDSNPresentCheck(os.Getenv),
	}); failed {
		fmt.Fprintf(os.Stderr, "  ✗ %s — %s\n    hint: %s\n", r.Name, r.Detail, r.FixHint)
		return errReported
	}
	// ... rest of runDev unchanged
```

The preflight checks change from 4 checks (`ConfigValidCheck`, `DSNPresentCheck`, `OwnerConnectCheck`, `AuthConnectCheck`, `RoleLayoutCheck`) to 2 (`ConfigValidCheck`, `SuperuserDSNPresentCheck`). Role provisioning and connectivity checks now happen inside `dbConnections`.

- [ ] **Step 5: Verify build**

```bash
go build ./internal/cli/...
```
Expected: Success (preflight.SuperuserDSNPresentCheck may be undefined until Task 4).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/dev.go internal/cli/dbsetup.go internal/cli/dbsetup_test.go
git commit -m "refactor(cli/dev): remove ensureRoles, add ensureAdminKey"
```

---

## Task 4: Update the preflight package

**Files:**
- Modify: `internal/cli/preflight/preflight.go`
- Modify: `internal/cli/preflight/preflight_test.go`

- [ ] **Step 1: Write the failing test for `SuperuserDSNPresentCheck`**

Add to `internal/cli/preflight/preflight_test.go`:

```go
func TestSuperuserDSNPresentCheck(t *testing.T) {
	check := preflight.SuperuserDSNPresentCheck(func(key string) string {
		if key == "INSTANCEZ_DATABASE_URL" {
			return "postgres://super:pass@host/db"
		}
		return ""
	})
	r := check()
	if !r.OK {
		t.Errorf("expected OK when INSTANCEZ_DATABASE_URL is set, got: %s", r.Detail)
	}

	missing := preflight.SuperuserDSNPresentCheck(func(string) string { return "" })
	r2 := missing()
	if r2.OK {
		t.Error("expected failure when INSTANCEZ_DATABASE_URL is missing")
	}
	if !strings.Contains(r2.FixHint, "INSTANCEZ_DATABASE_URL") {
		t.Errorf("fix hint should mention INSTANCEZ_DATABASE_URL, got: %s", r2.FixHint)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/cli/preflight/... -run TestSuperuserDSNPresentCheck
```
Expected: FAIL — `SuperuserDSNPresentCheck` undefined.

- [ ] **Step 3: Add `SuperuserDSNPresentCheck` and `EnvSuperuserDSN` to `preflight.go`**

In `internal/cli/preflight/preflight.go`, add after the existing `EnvOwnerDSN`/`EnvAuthDSN` constants:

```go
// EnvSuperuserDSN is the single database credential instancez now requires.
// The CLI provisions all roles from this connection on every startup.
const EnvSuperuserDSN = "INSTANCEZ_DATABASE_URL"
```

Add the new check function (alongside the existing ones):

```go
// SuperuserDSNPresentCheck returns a Check that verifies INSTANCEZ_DATABASE_URL
// is non-empty.
func SuperuserDSNPresentCheck(lookup func(string) string) Check {
	return func() Result {
		if lookup(EnvSuperuserDSN) != "" {
			return Result{Name: "INSTANCEZ_DATABASE_URL present", OK: true}
		}
		return Result{
			Name:    "INSTANCEZ_DATABASE_URL present",
			OK:      false,
			Detail:  "INSTANCEZ_DATABASE_URL is not set",
			FixHint: "set INSTANCEZ_DATABASE_URL to a superuser Postgres DSN, e.g. postgres://postgres:pass@localhost:5432/instancez",
		}
	}
}
```

Keep `EnvOwnerDSN`, `EnvAuthDSN`, `DSNPresentCheck`, `OwnerConnectCheck`, `AuthConnectCheck`, `RoleLayoutCheck` in place for now — they may be referenced by other code. They will be cleaned up in a follow-up if no longer used. **Do not delete them in this task.**

- [ ] **Step 4: Run tests**

```bash
go test ./internal/cli/preflight/... -v
```
Expected: All PASS.

- [ ] **Step 5: Full build and unit test**

```bash
go build ./... && go test -race ./...
```
Expected: Build succeeds, all non-integration tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/preflight/preflight.go internal/cli/preflight/preflight_test.go
git commit -m "feat(preflight): add SuperuserDSNPresentCheck for INSTANCEZ_DATABASE_URL"
```

---

## Task 5: Update `migrate_roles_test.go` comment

**Files:**
- Modify: `internal/app/migrate_roles_test.go`

- [ ] **Step 1: Update the stale comment**

In `internal/app/migrate_roles_test.go`, find the comment on `TestPlanFromScratch_NoRoleDDL` and update it. The test logic itself stays unchanged — migrations must not emit CREATE ROLE.

Find:
```go
// TestPlanFromScratch_NoRoleDDL asserts the migration does not emit
// CREATE ROLE / GRANT … TO authenticator statements. Roles are infrastructure
// (provisioned by the control plane in prod, by 01-roles.sql in dev); the
// migration must not touch them.
```

Replace with:
```go
// TestPlanFromScratch_NoRoleDDL asserts the migration does not emit
// CREATE ROLE / GRANT … TO authenticator statements. Roles are provisioned
// by the CLI startup sequence (bootstrapDB) on every startup; the migration
// planner must not touch them.
```

- [ ] **Step 2: Run tests to confirm nothing broke**

```bash
go test ./internal/app/... -run TestPlanFromScratch_NoRoleDDL -v
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/app/migrate_roles_test.go
git commit -m "docs(test): update role provisioning comment in migrate_roles_test"
```

---

## Task 6: Update `docker-compose.dev.yaml` and delete `scripts/postgres-init/`

**Files:**
- Modify: `docker-compose.dev.yaml`
- Delete: `scripts/postgres-init/` (directory and contents)

- [ ] **Step 1: Update `docker-compose.dev.yaml`**

Replace the file with:

```yaml
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: instancez
      POSTGRES_DB: instancez
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "postgres"]
      interval: 2s
      timeout: 3s
      retries: 10

  backend:
    build:
      context: .
      args:
        WITH_DASHBOARD: "false"
    ports:
      - "8080:8080"
    environment:
      INSTANCEZ_DATABASE_URL: postgres://postgres:instancez@postgres:5432/instancez?sslmode=disable
      INSTANCEZ_ADMIN_KEY: instancez-dev-key
      JWT_SECRET: dev-jwt-secret-change-me
    volumes:
      - ./instancez.yaml:/app/instancez.yaml
    depends_on:
      postgres:
        condition: service_healthy

  dashboard:
    build: ./dashboard
    ports:
      - "5173:5173"
    environment:
      API_URL: http://backend:8080
    volumes:
      - ./dashboard/src:/app/src
      - ./dashboard/index.html:/app/index.html
    depends_on:
      - backend

volumes:
  pgdata:
```

Changes: postgres `POSTGRES_USER` changed to `postgres` (superuser), removed init script volume mount, replaced `INSTANCEZ_OWNER_DATABASE_URL`/`INSTANCEZ_AUTH_DATABASE_URL` with `INSTANCEZ_DATABASE_URL`.

- [ ] **Step 2: Delete the init script**

```bash
rm -rf scripts/postgres-init
```

- [ ] **Step 3: Verify build still succeeds**

```bash
go build ./...
```
Expected: Success.

- [ ] **Step 4: Commit**

```bash
git add docker-compose.dev.yaml
git rm -r scripts/postgres-init/
git commit -m "refactor: remove postgres init script, use INSTANCEZ_DATABASE_URL in docker-compose"
```

---

## Task 7: Helm chart scaffolding — `Chart.yaml`, `values.yaml`, `.helmignore`

**Files:**
- Create: `helm/instancez/Chart.yaml`
- Create: `helm/instancez/values.yaml`
- Create: `helm/instancez/.helmignore`

- [ ] **Step 1: Create the chart directory**

```bash
mkdir -p helm/instancez/templates
```

- [ ] **Step 2: Create `helm/instancez/Chart.yaml`**

```yaml
apiVersion: v2
name: instancez
description: Kubernetes Helm chart for instancez — Supabase-compatible backend
type: application
version: 0.1.0
appVersion: "latest"
keywords:
  - instancez
  - supabase
  - postgres
  - backend
home: https://instancez.dev
sources:
  - https://github.com/instancez/instancez
```

- [ ] **Step 3: Create `helm/instancez/.helmignore`**

```
# Patterns to ignore when building packages.
.DS_Store
.git/
.gitignore
.github/
*.swp
*.bak
```

- [ ] **Step 4: Create `helm/instancez/values.yaml`**

```yaml
image:
  repository: ghcr.io/instancez/instancez
  tag: ""
  pullPolicy: IfNotPresent

replicaCount: 1

# Sensitive values — auto-generated 32-char random strings on first install if
# left empty. Preserved across helm upgrade via Secret lookup.
adminKey: ""
jwtSecret: ""

# Raw instancez.yaml content — mounted at /app/instancez.yaml inside the pod.
# Edit this block exactly as you would a local instancez.yaml file.
config: |
  version: 1
  project:
    name: instancez
  server:
    port: 8080
  database:
    pool:
      max: 10
      min: 2
      idle_timeout: 300s
  auth:
    jwt_expiry: 15m
    refresh_tokens: true
    refresh_token_expiry: 7d

service:
  type: ClusterIP
  port: 80

ingress:
  enabled: false
  className: ""
  annotations: {}
  host: ""
  tls: []

# Bundled PostgreSQL StatefulSet (postgres:17-alpine).
# Set enabled: false and provide externalPostgres.url to use your own database.
postgres:
  enabled: true
  image: postgres:17-alpine
  database: instancez
  username: postgres
  # Auto-generated 32-char random string on first install if left empty.
  password: ""
  storage:
    size: 10Gi
    storageClass: ""
  resources: {}

# Used when postgres.enabled=false. Must be a superuser Postgres DSN.
externalPostgres:
  url: ""

resources: {}
nodeSelector: {}
tolerations: []
affinity: {}
```

- [ ] **Step 5: Lint the chart structure**

```bash
helm lint helm/instancez/
```
Expected: `[INFO] Chart.yaml: icon is recommended` (acceptable), no errors.

- [ ] **Step 6: Commit**

```bash
git add helm/instancez/Chart.yaml helm/instancez/values.yaml helm/instancez/.helmignore
git commit -m "feat(helm): add chart scaffolding — Chart.yaml, values.yaml, .helmignore"
```

---

## Task 8: Helm `_helpers.tpl` and `NOTES.txt`

**Files:**
- Create: `helm/instancez/templates/_helpers.tpl`
- Create: `helm/instancez/templates/NOTES.txt`

- [ ] **Step 1: Create `helm/instancez/templates/_helpers.tpl`**

```yaml
{{/*
Expand the name of the chart.
*/}}
{{- define "instancez.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name. Truncated at 63 chars.
*/}}
{{- define "instancez.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart label value: name-version with + replaced for label safety.
*/}}
{{- define "instancez.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to every resource.
*/}}
{{- define "instancez.labels" -}}
helm.sh/chart: {{ include "instancez.chart" . }}
{{ include "instancez.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels used for Deployment/Service matching.
*/}}
{{- define "instancez.selectorLabels" -}}
app.kubernetes.io/name: {{ include "instancez.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
```

- [ ] **Step 2: Create `helm/instancez/templates/NOTES.txt`**

```
instancez is deployed.

{{- if .Values.postgres.enabled }}
Bundled PostgreSQL: enabled (single-replica StatefulSet, database: {{ .Values.postgres.database }})
For production, consider postgres.enabled=false with a managed database.
{{- else }}
External PostgreSQL: configured via externalPostgres.url
{{- end }}

{{- if .Values.ingress.enabled }}
URL: https://{{ .Values.ingress.host }}
{{- else }}
Access the API locally:
  kubectl port-forward svc/{{ include "instancez.fullname" . }} 8080:{{ .Values.service.port }}
  curl http://localhost:8080/health
{{- end }}

If adminKey or jwtSecret were auto-generated, save them now:
  kubectl get secret {{ include "instancez.fullname" . }} \
    -o jsonpath='{.data.adminKey}' | base64 -d && echo
  kubectl get secret {{ include "instancez.fullname" . }} \
    -o jsonpath='{.data.jwtSecret}' | base64 -d && echo

Roles (instancez_owner, authenticator, anon, authenticated, service_role) are
provisioned automatically on startup. No manual database setup required.
```

- [ ] **Step 3: Lint**

```bash
helm lint helm/instancez/
```
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add helm/instancez/templates/_helpers.tpl helm/instancez/templates/NOTES.txt
git commit -m "feat(helm): add _helpers.tpl and NOTES.txt"
```

---

## Task 9: Helm `secret.yaml` and `configmap.yaml`

**Files:**
- Create: `helm/instancez/templates/secret.yaml`
- Create: `helm/instancez/templates/configmap.yaml`

- [ ] **Step 1: Create `helm/instancez/templates/secret.yaml`**

```yaml
{{- $existing := lookup "v1" "Secret" .Release.Namespace (include "instancez.fullname" .) -}}
{{- $existingData := $existing.data | default dict -}}

{{- $adminKey := .Values.adminKey -}}
{{- if not $adminKey -}}
  {{- $adminKey = get $existingData "adminKey" | b64dec -}}
{{- end -}}
{{- if not $adminKey -}}
  {{- $adminKey = randAlphaNum 32 -}}
{{- end -}}

{{- $jwtSecret := .Values.jwtSecret -}}
{{- if not $jwtSecret -}}
  {{- $jwtSecret = get $existingData "jwtSecret" | b64dec -}}
{{- end -}}
{{- if not $jwtSecret -}}
  {{- $jwtSecret = randAlphaNum 32 -}}
{{- end -}}

{{- $pgPassword := .Values.postgres.password -}}
{{- if not $pgPassword -}}
  {{- $pgPassword = get $existingData "postgresPassword" | b64dec -}}
{{- end -}}
{{- if not $pgPassword -}}
  {{- $pgPassword = randAlphaNum 32 -}}
{{- end -}}

{{- $databaseUrl := "" -}}
{{- if .Values.postgres.enabled -}}
  {{- $databaseUrl = printf "postgres://%s:%s@%s-postgres:5432/%s?sslmode=disable" .Values.postgres.username $pgPassword (include "instancez.fullname" .) .Values.postgres.database -}}
{{- else -}}
  {{- $databaseUrl = .Values.externalPostgres.url -}}
{{- end -}}

apiVersion: v1
kind: Secret
metadata:
  name: {{ include "instancez.fullname" . }}
  labels:
    {{- include "instancez.labels" . | nindent 4 }}
type: Opaque
data:
  adminKey: {{ $adminKey | b64enc | quote }}
  jwtSecret: {{ $jwtSecret | b64enc | quote }}
  postgresPassword: {{ $pgPassword | b64enc | quote }}
  databaseUrl: {{ $databaseUrl | b64enc | quote }}
```

- [ ] **Step 2: Create `helm/instancez/templates/configmap.yaml`**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "instancez.fullname" . }}-config
  labels:
    {{- include "instancez.labels" . | nindent 4 }}
data:
  instancez.yaml: |
    {{- .Values.config | nindent 4 }}
```

- [ ] **Step 3: Template to verify rendering**

```bash
helm template myrelease helm/instancez/ | grep -A 20 'kind: Secret'
```
Expected: Secret with base64-encoded keys (`adminKey`, `jwtSecret`, `postgresPassword`, `databaseUrl`).

- [ ] **Step 4: Commit**

```bash
git add helm/instancez/templates/secret.yaml helm/instancez/templates/configmap.yaml
git commit -m "feat(helm): add secret.yaml with auto-generation and configmap.yaml"
```

---

## Task 10: Helm `postgres-statefulset.yaml` and `postgres-service.yaml`

**Files:**
- Create: `helm/instancez/templates/postgres-statefulset.yaml`
- Create: `helm/instancez/templates/postgres-service.yaml`

- [ ] **Step 1: Create `helm/instancez/templates/postgres-statefulset.yaml`**

```yaml
{{- if .Values.postgres.enabled }}
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: {{ include "instancez.fullname" . }}-postgres
  labels:
    {{- include "instancez.labels" . | nindent 4 }}
    app.kubernetes.io/component: postgres
spec:
  serviceName: {{ include "instancez.fullname" . }}-postgres
  replicas: 1
  selector:
    matchLabels:
      {{- include "instancez.selectorLabels" . | nindent 6 }}
      app.kubernetes.io/component: postgres
  template:
    metadata:
      labels:
        {{- include "instancez.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: postgres
    spec:
      containers:
        - name: postgres
          image: {{ .Values.postgres.image }}
          env:
            - name: POSTGRES_USER
              value: {{ .Values.postgres.username | quote }}
            - name: POSTGRES_DB
              value: {{ .Values.postgres.database | quote }}
            - name: POSTGRES_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: {{ include "instancez.fullname" . }}
                  key: postgresPassword
          ports:
            - name: postgres
              containerPort: 5432
              protocol: TCP
          readinessProbe:
            exec:
              command:
                - pg_isready
                - -U
                - {{ .Values.postgres.username | quote }}
            initialDelaySeconds: 5
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 12
          {{- with .Values.postgres.resources }}
          resources:
            {{- toYaml . | nindent 12 }}
          {{- end }}
          volumeMounts:
            - name: pgdata
              mountPath: /var/lib/postgresql/data
  volumeClaimTemplates:
    - metadata:
        name: pgdata
      spec:
        accessModes:
          - ReadWriteOnce
        {{- if .Values.postgres.storage.storageClass }}
        storageClassName: {{ .Values.postgres.storage.storageClass | quote }}
        {{- end }}
        resources:
          requests:
            storage: {{ .Values.postgres.storage.size }}
{{- end }}
```

- [ ] **Step 2: Create `helm/instancez/templates/postgres-service.yaml`**

```yaml
{{- if .Values.postgres.enabled }}
apiVersion: v1
kind: Service
metadata:
  name: {{ include "instancez.fullname" . }}-postgres
  labels:
    {{- include "instancez.labels" . | nindent 4 }}
    app.kubernetes.io/component: postgres
spec:
  type: ClusterIP
  ports:
    - port: 5432
      targetPort: postgres
      protocol: TCP
      name: postgres
  selector:
    {{- include "instancez.selectorLabels" . | nindent 4 }}
    app.kubernetes.io/component: postgres
{{- end }}
```

- [ ] **Step 3: Template and verify**

```bash
helm template myrelease helm/instancez/ | grep -B 2 -A 30 'kind: StatefulSet'
```
Expected: StatefulSet with `postgres:17-alpine`, PVC template for 10Gi, secretKeyRef for `postgresPassword`.

- [ ] **Step 4: Verify postgres disabled works**

```bash
helm template myrelease helm/instancez/ --set postgres.enabled=false | grep -c 'StatefulSet'
```
Expected: `0` — no StatefulSet rendered.

- [ ] **Step 5: Commit**

```bash
git add helm/instancez/templates/postgres-statefulset.yaml helm/instancez/templates/postgres-service.yaml
git commit -m "feat(helm): add postgres StatefulSet and Service templates"
```

---

## Task 11: Helm `deployment.yaml`, `service.yaml`, `ingress.yaml`

**Files:**
- Create: `helm/instancez/templates/deployment.yaml`
- Create: `helm/instancez/templates/service.yaml`
- Create: `helm/instancez/templates/ingress.yaml`

- [ ] **Step 1: Create `helm/instancez/templates/deployment.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "instancez.fullname" . }}
  labels:
    {{- include "instancez.labels" . | nindent 4 }}
spec:
  replicas: {{ .Values.replicaCount }}
  selector:
    matchLabels:
      {{- include "instancez.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "instancez.selectorLabels" . | nindent 8 }}
    spec:
      {{- if .Values.postgres.enabled }}
      initContainers:
        - name: wait-for-postgres
          image: {{ .Values.postgres.image }}
          command:
            - sh
            - -c
            - |
              until pg_isready -h {{ include "instancez.fullname" . }}-postgres -U {{ .Values.postgres.username | quote }}; do
                echo "waiting for postgres to be ready..."
                sleep 2
              done
      {{- end }}
      containers:
        - name: instancez
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          env:
            - name: INSTANCEZ_DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: {{ include "instancez.fullname" . }}
                  key: databaseUrl
            - name: INSTANCEZ_ADMIN_KEY
              valueFrom:
                secretKeyRef:
                  name: {{ include "instancez.fullname" . }}
                  key: adminKey
            - name: JWT_SECRET
              valueFrom:
                secretKeyRef:
                  name: {{ include "instancez.fullname" . }}
                  key: jwtSecret
            - name: INSTANCEZ_PORT
              value: "8080"
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
          livenessProbe:
            tcpSocket:
              port: http
            initialDelaySeconds: 10
            periodSeconds: 30
          readinessProbe:
            tcpSocket:
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          volumeMounts:
            - name: config
              mountPath: /app/instancez.yaml
              subPath: instancez.yaml
          {{- with .Values.resources }}
          resources:
            {{- toYaml . | nindent 12 }}
          {{- end }}
      volumes:
        - name: config
          configMap:
            name: {{ include "instancez.fullname" . }}-config
      {{- with .Values.nodeSelector }}
      nodeSelector:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
```

- [ ] **Step 2: Create `helm/instancez/templates/service.yaml`**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "instancez.fullname" . }}
  labels:
    {{- include "instancez.labels" . | nindent 4 }}
spec:
  type: {{ .Values.service.type }}
  ports:
    - port: {{ .Values.service.port }}
      targetPort: http
      protocol: TCP
      name: http
  selector:
    {{- include "instancez.selectorLabels" . | nindent 4 }}
```

- [ ] **Step 3: Create `helm/instancez/templates/ingress.yaml`**

```yaml
{{- if .Values.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ include "instancez.fullname" . }}
  labels:
    {{- include "instancez.labels" . | nindent 4 }}
  {{- with .Values.ingress.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  {{- if .Values.ingress.className }}
  ingressClassName: {{ .Values.ingress.className }}
  {{- end }}
  {{- if .Values.ingress.tls }}
  tls:
    {{- toYaml .Values.ingress.tls | nindent 4 }}
  {{- end }}
  rules:
    - host: {{ .Values.ingress.host | quote }}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{ include "instancez.fullname" . }}
                port:
                  number: {{ .Values.service.port }}
{{- end }}
```

- [ ] **Step 4: Full lint**

```bash
helm lint helm/instancez/
```
Expected: 0 errors.

- [ ] **Step 5: Template with ingress enabled and verify**

```bash
helm template myrelease helm/instancez/ \
  --set ingress.enabled=true \
  --set ingress.host=api.example.com \
  | grep -A 20 'kind: Ingress'
```
Expected: Ingress with host `api.example.com`, no annotations block.

- [ ] **Step 6: Template with external Postgres**

```bash
helm template myrelease helm/instancez/ \
  --set postgres.enabled=false \
  --set externalPostgres.url='postgres://super:pass@rds.example.com:5432/instancez' \
  | grep 'databaseUrl'
```
Expected: base64-encoded external URL in the Secret.

- [ ] **Step 7: Commit**

```bash
git add helm/instancez/templates/deployment.yaml helm/instancez/templates/service.yaml helm/instancez/templates/ingress.yaml
git commit -m "feat(helm): add Deployment, Service, and Ingress templates"
```

---

## Task 12: Update docs — `env-vars.md`, `docker.md`, `self-hosted.md`

**Files:**
- Modify: `docs/site/src/content/docs/deploy/env-vars.md`
- Modify: `docs/site/src/content/docs/deploy/docker.md`
- Modify: `docs/site/src/content/docs/deploy/self-hosted.md`

- [ ] **Step 1: Read current content of the three files**

```bash
cat docs/site/src/content/docs/deploy/env-vars.md
cat docs/site/src/content/docs/deploy/docker.md
cat docs/site/src/content/docs/deploy/self-hosted.md
```

- [ ] **Step 2: Update `env-vars.md`**

Remove the rows for `INSTANCEZ_OWNER_DATABASE_URL` and `INSTANCEZ_AUTH_DATABASE_URL` from the required variables table.

Update the `INSTANCEZ_DATABASE_URL` row — it is no longer dev-only. Change its description to:

```
| `INSTANCEZ_DATABASE_URL` | Superuser Postgres DSN (e.g. `postgres://postgres:pass@localhost:5432/instancez`). Used by both `inz dev` and `inz serve`. On startup instancez provisions `instancez_owner`, `authenticator`, and the three API roles from this connection using the same password, then opens two internal pools. |
```

- [ ] **Step 3: Update `docker.md`**

Remove the init SQL section (the `init/01-roles.sql` block and its explanation). Replace with a note:

```markdown
> **Role provisioning is automatic.** instancez connects as the Postgres superuser on startup and creates all required roles (`instancez_owner`, `authenticator`, `anon`, `authenticated`, `service_role`) using the superuser's password. No manual SQL setup is needed.
```

In every docker-compose/docker run example, replace:
- `INSTANCEZ_OWNER_DATABASE_URL: postgres://instancez_owner:...` → remove
- `INSTANCEZ_AUTH_DATABASE_URL: postgres://authenticator:...` → remove
- Add `INSTANCEZ_DATABASE_URL: postgres://postgres:<password>@postgres:5432/instancez?sslmode=disable`

- [ ] **Step 4: Update `self-hosted.md`**

Same env var changes as `docker.md`. Remove any "create roles manually" instructions. Add a note that role provisioning is automatic.

- [ ] **Step 5: Commit**

```bash
git add docs/site/src/content/docs/deploy/env-vars.md \
        docs/site/src/content/docs/deploy/docker.md \
        docs/site/src/content/docs/deploy/self-hosted.md
git commit -m "docs: update deployment docs for single INSTANCEZ_DATABASE_URL model"
```

---

## Task 13: Create `docs/site/src/content/docs/deploy/kubernetes.md`

**Files:**
- Create: `docs/site/src/content/docs/deploy/kubernetes.md`

- [ ] **Step 1: Read the docs site config to understand frontmatter format**

```bash
head -10 docs/site/src/content/docs/deploy/docker.md
```

- [ ] **Step 2: Create `docs/site/src/content/docs/deploy/kubernetes.md`**

Use the same frontmatter format as `docker.md`. The document should cover:

1. **Prerequisites** — Helm 3, `kubectl` access, a Kubernetes cluster
2. **Quick start** (bundled Postgres):
   ```bash
   helm install instancez ./helm/instancez \
     --set config="version: 1\nproject:\n  name: my-app"
   ```
   Note that `adminKey`, `jwtSecret`, and `postgres.password` are auto-generated and stable.
3. **Retrieving auto-generated credentials**:
   ```bash
   kubectl get secret instancez -o jsonpath='{.data.adminKey}' | base64 -d
   kubectl get secret instancez -o jsonpath='{.data.jwtSecret}' | base64 -d
   ```
4. **External Postgres** (`postgres.enabled=false`):
   ```bash
   helm install instancez ./helm/instancez \
     --set postgres.enabled=false \
     --set externalPostgres.url='postgres://super:pass@rds.host:5432/mydb'
   ```
5. **Ingress**:
   ```bash
   helm upgrade instancez ./helm/instancez \
     --set ingress.enabled=true \
     --set ingress.host=api.example.com \
     --set ingress.className=nginx
   ```
6. **Password rotation** — update the Secret value (or the `--set` flag), run `helm upgrade`, pod restarts and re-provisions roles with new password automatically.
7. **Providing `instancez.yaml`** — show the `config:` block in a custom `values.yaml` file.
8. **Production notes** — use external managed Postgres for HA, bundled StatefulSet is single-replica.

- [ ] **Step 3: Add kubernetes.md to the docs site navigation** if the site has a sidebar config

```bash
grep -r 'deploy/docker' docs/site/src/ --include='*.ts' --include='*.mjs' --include='*.json' -l
```

If a sidebar config exists, add `deploy/kubernetes` next to `deploy/docker`.

- [ ] **Step 4: Commit**

```bash
git add docs/site/src/content/docs/deploy/kubernetes.md
git commit -m "docs: add Kubernetes/Helm deployment guide"
```

---

## Task 14: Run full test suite

- [ ] **Step 1: Unit tests**

```bash
go test -race ./...
```
Expected: all pass.

- [ ] **Step 2: Integration tests** (requires Docker)

```bash
go test -tags=integration -race ./internal/cli/... ./internal/app/...
```
Expected: all pass, including `TestDBConnectionsProvisionRoles`.

- [ ] **Step 3: Dashboard tests**

```bash
cd dashboard && npm test
```
Expected: all pass.

- [ ] **Step 4: Helm lint**

```bash
helm lint helm/instancez/
```
Expected: 0 errors.

- [ ] **Step 5: Final commit if any loose files remain**

```bash
git status
```
Commit any uncommitted docs or test changes.
