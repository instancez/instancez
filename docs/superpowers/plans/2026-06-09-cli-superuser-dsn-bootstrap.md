# CLI Superuser-DSN Dev Bootstrap + Flag/Env Standardization — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `ultra dev` provision the two-login role layout from a single superuser DSN (`ULTRABASE_DATABASE_URL`), move bootstrapping out of `init`, and standardize the CLI's flag↔env-var contract.

**Architecture:** `ultra dev` reads a superuser DSN (env-only) after loading `.development.env`, calls a new `ensureRoles` helper that reuses the existing `bootstrapDB` to create `ultrabase_owner` + `authenticator` + the three API roles, `os.Setenv`s the derived owner/auth DSNs (so unchanged preflight + `dbConnections` just work), and persists them back to `.development.env`. `serve` is unchanged except for inheriting the standardized env names. `init` becomes pure scaffolding and writes a `.development.env.example`.

**Tech Stack:** Go, cobra/pflag, pgx v5, testcontainers-go (integration).

**Spec:** `docs/superpowers/specs/2026-06-09-cli-superuser-dsn-bootstrap-design.md`

**Conventions for every commit:** run `go build ./...` and `go test -race ./internal/cli/...` before committing. End commit messages with the `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` trailer. Work on a feature branch, not `main`.

---

## Task 0: Create the working branch

- [ ] **Step 1: Branch off main**

```bash
git checkout -b cli-superuser-dsn-bootstrap
```

- [ ] **Step 2: Confirm a clean build baseline**

Run: `go build ./... && go test -race ./internal/cli/...`
Expected: PASS (this is the green starting point).

---

## Task 1: Standardize the flag↔env-var binding

Remove the bespoke alias maps so every flag binds to `ULTRABASE_<FLAG_UPPER_SNAKE>` via the generic rule. Hard rename (no fallbacks): `config`→`ULTRABASE_CONFIG`, `watch`→`ULTRABASE_WATCH`, `watch-interval`→`ULTRABASE_WATCH_INTERVAL`, `verbose`→`ULTRABASE_VERBOSE`. `no-watch` and the deprecated `use-dsn` keep no env binding.

**Files:**
- Modify: `internal/cli/flags.go`
- Modify: `internal/cli/validate.go` (drops a `configEnvAliases` reference)
- Test: `internal/cli/serve_test.go`

- [ ] **Step 1: Update the serve tests to the standardized env names**

In `internal/cli/serve_test.go`, replace `TestParseServeFlagsEnvFallbacks` and `TestParseServeFlagsConfigSourceEnv` and the validation case so they use the new names. Apply these edits:

In `TestParseServeFlagsEnvFallbacks`, change the `env` map:

```go
	env := map[string]string{
		"ULTRABASE_WATCH":          "true",
		"ULTRABASE_WATCH_INTERVAL": "30s",
		"ULTRABASE_DASHBOARD":      "readwrite",
	}
```

Replace the whole `TestParseServeFlagsConfigSourceEnv` function with:

```go
func TestParseServeFlagsConfigEnv(t *testing.T) {
	// The single standardized name sets the config path.
	got, err := parseServeFlags([]string{}, func(k string) string {
		return map[string]string{"ULTRABASE_CONFIG": "s3://bucket/new"}[k]
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.configPath != "s3://bucket/new" {
		t.Fatalf("configPath = %q, want s3://bucket/new", got.configPath)
	}

	// The old ULTRABASE_CONFIG_SOURCE name is no longer bound.
	got, err = parseServeFlags([]string{}, func(k string) string {
		return map[string]string{"ULTRABASE_CONFIG_SOURCE": "s3://bucket/old"}[k]
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.configPath != "ultrabase.yaml" {
		t.Fatalf("ULTRABASE_CONFIG_SOURCE should no longer bind; configPath = %q, want default", got.configPath)
	}
}
```

In `TestParseServeFlagsValidation`, change the "env var below minimum" case:

```go
		{
			name:    "env var below minimum attributed to env name",
			args:    []string{},
			env:     map[string]string{"ULTRABASE_WATCH_INTERVAL": "5s"},
			wantErr: "ULTRABASE_WATCH_INTERVAL must be at least 10s",
		},
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestParseServeFlags' ./internal/cli/`
Expected: FAIL — the production alias maps still map `watch`→`ULTRABASE_CONFIG_WATCH`, etc., so the new env names don't bind yet.

- [ ] **Step 3: Remove the alias maps in flags.go**

In `internal/cli/flags.go`, delete the `configEnvAliases` var and the `serveEnvAliases`/`devEnvAliases` block (lines beginning `// configEnvAliases is the env-var precedence list…` through the closing `)` of the `var (…)` group). Replace that entire block with the minimal map below:

```go
// devNoEnvBinding lists dev flags that intentionally have NO env-var binding:
// no-watch is pure CLI sugar (the env way to disable watching is
// ULTRABASE_WATCH=false) and use-dsn is a deprecated no-op. Every other flag
// resolves through applyEnvDefaults' generic ULTRABASE_<FLAG_UPPER_SNAKE> rule.
var devNoEnvBinding = map[string][]string{
	"no-watch": {},
	"use-dsn":  {},
}
```

- [ ] **Step 4: Repoint resolveServeFlags and resolveDevFlags at the new binding**

In `resolveServeFlags`, change the `applyEnvDefaults` call to pass `nil` (all generic):

```go
	setBy, err := applyEnvDefaults(fs.flags, nil, lookup)
```

In `resolveDevFlags`, change it to use the minimal map:

```go
	setBy, err := applyEnvDefaults(fs.flags, devNoEnvBinding, lookup)
```

In `internal/cli/validate.go` (the `RunE` of the validate command, ~line 38), drop the now-deleted `configEnvAliases` reference and use the generic rule:

```go
			if _, err := applyEnvDefaults(cmd.Flags(), nil, os.Getenv); err != nil {
				return err
			}
```

- [ ] **Step 5: Update the flag help strings that name old env vars**

In `newServeFlagSet`, update these two help strings:

```go
	fs.flags.StringVar(&fs.configPath, "config", "ultrabase.yaml", "config source (file path or s3://bucket/key; env: ULTRABASE_CONFIG)")
	fs.flags.BoolVar(&fs.watch, "watch", false, "watch the config source for changes (env: ULTRABASE_WATCH)")
	fs.flags.DurationVar(&fs.watchInterval, "watch-interval", 60*time.Second, "S3-watch poll interval; min 10s (env: ULTRABASE_WATCH_INTERVAL)")
```

- [ ] **Step 6: Run the full cli test suite**

Run: `go build ./... && go test -race ./internal/cli/...`
Expected: PASS. (`root_test.go`'s `TestApplyEnvDefaults` passes literal alias maps to exercise the generic engine directly and is unaffected.)

- [ ] **Step 7: Commit**

```bash
git add internal/cli/flags.go internal/cli/validate.go internal/cli/serve_test.go
git commit -m "refactor(cli): standardize flag/env binding to ULTRABASE_<FLAG>

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Add the `ensureRoles` bootstrap helper

`ensureRoles` is the dev bootstrap-or-skip helper. Its pure decision (`shouldBootstrap`) and the skip paths are unit-tested without Docker; the bootstrap-executes path is covered by the integration test in Task 6.

**Files:**
- Modify: `internal/cli/dbsetup.go`
- Modify: `internal/cli/init.go` (reword one comment only)
- Test: `internal/cli/dbsetup_test.go`

- [ ] **Step 1: Write the failing unit tests**

Append to `internal/cli/dbsetup_test.go`:

```go
func TestShouldBootstrap(t *testing.T) {
	cases := []struct {
		name                       string
		owner, auth, superuser     string
		want                       bool
	}{
		{"both role DSNs set → skip even with superuser", "o", "a", "super", false},
		{"both role DSNs set, no superuser → skip", "o", "a", "", false},
		{"only owner set + superuser → bootstrap", "o", "", "super", true},
		{"neither role DSN, superuser set → bootstrap", "", "", "super", true},
		{"neither role DSN, no superuser → skip", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldBootstrap(tc.owner, tc.auth, tc.superuser); got != tc.want {
				t.Errorf("shouldBootstrap(%q,%q,%q) = %v, want %v", tc.owner, tc.auth, tc.superuser, got, tc.want)
			}
		})
	}
}

func TestEnsureRolesSkipsWhenRoleDSNsPresent(t *testing.T) {
	// Both role DSNs set → ensureRoles must be a no-op and never touch a DB.
	t.Setenv("ULTRABASE_OWNER_DATABASE_URL", "postgres://owner@localhost/db")
	t.Setenv("ULTRABASE_AUTH_DATABASE_URL", "postgres://auth@localhost/db")

	res, err := ensureRoles(context.Background(), "postgres://super@localhost/db", filepath.Join(t.TempDir(), ".development.env"))
	if err != nil {
		t.Fatalf("ensureRoles: %v", err)
	}
	if res.Ran {
		t.Error("ensureRoles ran bootstrap despite both role DSNs being set")
	}
}

func TestEnsureRolesNoopWhenNothingToDo(t *testing.T) {
	// No role DSNs and no superuser → no-op (the caller's missing-DSN path fires).
	t.Setenv("ULTRABASE_OWNER_DATABASE_URL", "")
	t.Setenv("ULTRABASE_AUTH_DATABASE_URL", "")

	res, err := ensureRoles(context.Background(), "", filepath.Join(t.TempDir(), ".development.env"))
	if err != nil {
		t.Fatalf("ensureRoles: %v", err)
	}
	if res.Ran {
		t.Error("ensureRoles ran bootstrap with no superuser DSN")
	}
}

func TestPersistDSNsCreatesAndMerges(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".development.env")

	// Create-from-empty path.
	if err := persistDSNs(envFile, "postgres://owner@h/db", "postgres://auth@h/db"); err != nil {
		t.Fatalf("persistDSNs (create): %v", err)
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"ULTRABASE_OWNER_DATABASE_URL=postgres://owner@h/db",
		"ULTRABASE_AUTH_DATABASE_URL=postgres://auth@h/db",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("created env file missing %q\n--- got ---\n%s", want, got)
		}
	}

	// Merge path: a user-added line survives; the DSNs are updated in place.
	if err := os.WriteFile(envFile, []byte("MY_CUSTOM=keep\nULTRABASE_OWNER_DATABASE_URL=old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := persistDSNs(envFile, "postgres://owner@h/db2", "postgres://auth@h/db2"); err != nil {
		t.Fatalf("persistDSNs (merge): %v", err)
	}
	data, _ = os.ReadFile(envFile)
	got = string(data)
	if !strings.Contains(got, "MY_CUSTOM=keep") {
		t.Errorf("merge dropped user line:\n%s", got)
	}
	if !strings.Contains(got, "ULTRABASE_OWNER_DATABASE_URL=postgres://owner@h/db2") {
		t.Errorf("merge did not update owner DSN:\n%s", got)
	}
	if !strings.Contains(got, "ULTRABASE_AUTH_DATABASE_URL=postgres://auth@h/db2") {
		t.Errorf("merge did not append auth DSN:\n%s", got)
	}
}
```

Add the imports `context`, `os`, `path/filepath`, and `strings` to the test file's import block.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -run 'TestShouldBootstrap|TestEnsureRoles|TestPersistDSNs' ./internal/cli/`
Expected: FAIL — `shouldBootstrap`, `ensureRoles`, `roleBootstrap`, and `persistDSNs` are undefined.

- [ ] **Step 3: Implement the helper in dbsetup.go**

Add to the import block of `internal/cli/dbsetup.go`: it currently imports `context`, `fmt`, `os`, plus the postgres/domain/errgroup packages. No new imports are needed (`mergeEnvFile`, `scaffoldDevelopmentEnv`, `envKV` are same-package from `init.go`).

Append to `internal/cli/dbsetup.go`:

```go
// roleBootstrap reports the outcome of ensureRoles so the caller can log it.
type roleBootstrap struct {
	Ran     bool   // bootstrap executed this run
	EnvFile string // file the derived DSNs were written to
}

// shouldBootstrap reports whether ensureRoles should provision roles: only when
// the two role DSNs are NOT both already present AND a superuser DSN is
// available to bootstrap from.
func shouldBootstrap(ownerDSN, authDSN, superuserDSN string) bool {
	bothPresent := ownerDSN != "" && authDSN != ""
	return !bothPresent && superuserDSN != ""
}

// ensureRoles provisions the ultrabase role layout from a privileged/superuser
// DSN when the two role DSNs are absent, writing the derived owner +
// authenticator DSNs into the process env (so the unchanged preflight checks
// and dbConnections pick them up) and persisting them to envFile for reuse on
// the next run. It is a no-op when both role DSNs are already set or when no
// superuser DSN is available.
func ensureRoles(ctx context.Context, superuserDSN, envFile string) (roleBootstrap, error) {
	owner := os.Getenv("ULTRABASE_OWNER_DATABASE_URL")
	auth := os.Getenv("ULTRABASE_AUTH_DATABASE_URL")
	if !shouldBootstrap(owner, auth, superuserDSN) {
		return roleBootstrap{}, nil
	}

	ownerDSN, authDSN, err := bootstrapDB(ctx, superuserDSN)
	if err != nil {
		return roleBootstrap{}, err
	}
	os.Setenv("ULTRABASE_OWNER_DATABASE_URL", ownerDSN)
	os.Setenv("ULTRABASE_AUTH_DATABASE_URL", authDSN)

	if err := persistDSNs(envFile, ownerDSN, authDSN); err != nil {
		return roleBootstrap{}, err
	}
	return roleBootstrap{Ran: true, EnvFile: envFile}, nil
}

// persistDSNs writes the derived owner + authenticator DSNs into envFile. When
// the file is absent it is created from the scaffold template; when present the
// two keys are merged in (preserving the user's other lines and comments).
func persistDSNs(envFile, ownerDSN, authDSN string) error {
	var existing string
	if data, err := os.ReadFile(envFile); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", envFile, err)
	}

	var content string
	if existing == "" {
		content = scaffoldDevelopmentEnv(ownerDSN, authDSN)
	} else {
		content = mergeEnvFile(existing, []envKV{
			{Key: "ULTRABASE_OWNER_DATABASE_URL", Val: ownerDSN},
			{Key: "ULTRABASE_AUTH_DATABASE_URL", Val: authDSN},
		})
	}
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", envFile, err)
	}
	return nil
}
```

- [ ] **Step 4: Reword the scaffoldDevelopmentEnv header comment in init.go**

In `internal/cli/init.go`, the `scaffoldDevelopmentEnv` function's first line still references `ultra init --with-dsn`. Change the header line inside the returned string:

```go
func scaffoldDevelopmentEnv(ownerDSN, authDSN string) string {
	return fmt.Sprintf(`# Owner + authenticator DSNs provisioned by 'ultra dev' from ULTRABASE_DATABASE_URL.
ULTRABASE_OWNER_DATABASE_URL=%s
ULTRABASE_AUTH_DATABASE_URL=%s
`, ownerDSN, authDSN)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race -run 'TestShouldBootstrap|TestEnsureRoles|TestPersistDSNs' ./internal/cli/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/dbsetup.go internal/cli/dbsetup_test.go internal/cli/init.go
git commit -m "feat(cli): add ensureRoles helper to bootstrap roles from a superuser DSN

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: Wire `ensureRoles` into `ultra dev`

Bootstrap runs *between* the dotenv load and preflight, so on a superuser-only fresh DB the preflight checks pass.

**Files:**
- Modify: `internal/cli/dev.go`

- [ ] **Step 1: Insert the bootstrap step in runDev**

In `internal/cli/dev.go`, find the block in `runDev`:

```go
	_ = config.LoadDotenv(".development.env")
	if r, failed := preflight.RunUntilFail([]preflight.Check{
```

Insert the bootstrap call between the `LoadDotenv` line and the `if r, failed` line:

```go
	_ = config.LoadDotenv(".development.env")

	// Bootstrap the role layout from a superuser DSN when the two role DSNs are
	// not already present. This runs BEFORE preflight: on a fresh superuser-only
	// database the DSN/connect/role checks would otherwise all fail. ensureRoles
	// sets the derived DSNs into the env (so the checks below read them) and
	// persists them to .development.env for the next run.
	if res, err := ensureRoles(context.Background(), os.Getenv("ULTRABASE_DATABASE_URL"), ".development.env"); err != nil {
		return fmt.Errorf("bootstrap roles: %w", err)
	} else if res.Ran {
		fmt.Println("  ✓ Provisioned roles from ULTRABASE_DATABASE_URL (ultrabase_owner + authenticator + anon/authenticated/service_role)")
		fmt.Printf("  ✓ Wrote derived owner + authenticator DSNs to %s\n", res.EnvFile)
	}

	if r, failed := preflight.RunUntilFail([]preflight.Check{
```

(`context`, `fmt`, and `os` are already imported in `dev.go`.)

- [ ] **Step 2: Verify build and existing dev tests still pass**

Run: `go build ./... && go test -race ./internal/cli/...`
Expected: PASS. (Unit-level `parseDevFlags` tests are unaffected; `ensureRoles` is a no-op in tests where no superuser DSN is set.)

- [ ] **Step 3: Commit**

```bash
git add internal/cli/dev.go
git commit -m "feat(cli): bootstrap roles in 'ultra dev' before preflight

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: Remove `--with-dsn` from `init`; write `.development.env.example`

`init` becomes pure scaffolding: no DB calls. It drops a `.development.env.example` documenting the superuser flow.

**Files:**
- Modify: `internal/cli/init.go`
- Test: `internal/cli/init_test.go`

- [ ] **Step 1: Update init tests for the new behavior**

In `internal/cli/init_test.go`, update `TestRunInitWritesProductionEnvExample`. Replace the trailing `.development.env`-absent comment+check and add the `.example` assertion. The function body becomes:

```go
func TestRunInitWritesProductionEnvExample(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(context.Background(), initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	for _, name := range []string{".production.env.example", ".development.env.example", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	// init never touches a database, so it must NOT write a live .development.env
	// (that is now written by `ultra dev` after bootstrapping). Only the example
	// template is written here.
	if _, err := os.Stat(filepath.Join(dir, ".development.env")); !os.IsNotExist(err) {
		t.Errorf("init wrote a live .development.env (expected only .development.env.example)")
	}
}
```

Add a focused test for the example contents:

```go
// TestRunInitDevelopmentEnvExampleDocumentsSuperuser verifies the dev example
// points users at the single superuser DSN that `ultra dev` bootstraps from.
func TestRunInitDevelopmentEnvExampleDocumentsSuperuser(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(context.Background(), initOptions{name: "demo", dir: dir}); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".development.env.example"))
	if err != nil {
		t.Fatalf("read .development.env.example: %v", err)
	}
	if !strings.Contains(string(data), "ULTRABASE_DATABASE_URL=") {
		t.Errorf(".development.env.example should document ULTRABASE_DATABASE_URL\n--- got ---\n%s", data)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test -run 'TestRunInitWritesProductionEnvExample|TestRunInitDevelopmentEnvExampleDocumentsSuperuser' ./internal/cli/`
Expected: FAIL — `.development.env.example` is not written yet.

- [ ] **Step 3: Remove the --with-dsn flag and field**

In `internal/cli/init.go`:

Remove the `withDSN` field from `initOptions` (the line `withDSN      string`).

Remove the flag registration line in `newInitCmd`:

```go
	cmd.Flags().StringVar(&opts.withDSN, "with-dsn", "", "bootstrap roles on this privileged Postgres DSN")
```

Update the `Long` help to drop the `--with-dsn` paragraph. Replace the `Long:` string with:

```go
		Long: `Scaffold a new Ultrabase project.

The project is created in the current directory by default. The project name
defaults to the directory's basename when not given as a positional argument.

init only writes scaffolding files; it never touches a database. A
'.development.env.example' is written documenting the single superuser DSN that
'ultra dev' uses to provision roles on first run.`,
```

- [ ] **Step 4: Remove the bootstrap + .development.env write blocks**

In `runInit`, delete the entire `var ownerDSN, authDSN string` + `switch { case opts.withDSN != "": … }` block (the one that prints "Bootstrapping roles on the supplied DSN..."). Then delete the `.development.env` write block that begins:

```go
	// .development.env: key-preserving merge. Rotated DSNs go in, user-added
	// custom lines (extra env vars, comments) stay put.
	if ownerDSN != "" && authDSN != "" {
		...
	}
```

- [ ] **Step 5: Add the .development.env.example scaffold write**

In `runInit`, right after the `.production.env.example` `applyWrite` block, add:

```go
	// .development.env.example: write once. Documents the superuser DSN that
	// `ultra dev` bootstraps roles from. Treated as authoritative after first
	// write — user edits are preserved on re-run.
	if err := applyWrite(dir, ".development.env.example", func(existing string) (string, writeAction) {
		if existing != "" {
			return existing, actionSkip
		}
		return scaffoldDevelopmentEnvExample(), actionCreate
	}); err != nil {
		return err
	}
```

- [ ] **Step 6: Update the next-steps block**

In `runInit`, the trailing `switch` that prints next steps has a `case opts.withDSN != "":` arm. Replace the whole `switch` with:

```go
	switch {
	case opts.withCloud:
		fmt.Println("  ultra deploy            # push your YAML to the cloud project")
	default:
		fmt.Println("  cp .development.env.example .development.env   # set ULTRABASE_DATABASE_URL")
		fmt.Println("  ultra dev")
	}
```

- [ ] **Step 7: Add the scaffoldDevelopmentEnvExample function**

Add to `internal/cli/init.go` (next to `scaffoldProductionEnvExample`):

```go
func scaffoldDevelopmentEnvExample() string {
	return `# Local development config — copy to .development.env (gitignored), then set a
# superuser/privileged Postgres DSN below. On first run, 'ultra dev' provisions
# ultrabase_owner + authenticator + the API roles from it and writes the derived
# owner/authenticator DSNs back into .development.env, so subsequent runs reuse
# them. After the first run you can remove ULTRABASE_DATABASE_URL.

ULTRABASE_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres
`
}
```

- [ ] **Step 8: Run init tests**

Run: `go build ./... && go test -race ./internal/cli/ -run TestRunInit`
Expected: PASS. (`TestRunInitPreservesProductionEnvExample` and the idempotency tests still hold; nothing writes a live `.development.env`.)

- [ ] **Step 9: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "feat(cli): make 'ultra init' pure scaffolding; drop --with-dsn

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: Repoint the preflight fix-hints

The DSN/connect/role checks (run by `dev` and `doctor`) hard-code `ultra init --with-dsn <dsn>`, a flag that no longer exists.

**Files:**
- Modify: `internal/cli/preflight/preflight.go`

- [ ] **Step 1: Replace the with-dsn hints**

In `internal/cli/preflight/preflight.go`, replace every occurrence of the fix-hint text that mentions `ultra init --with-dsn`:

In `DSNPresentCheck`, the three `FixHint` values become:

```go
		// owner == "" && auth == ""
		FixHint: "set ULTRABASE_DATABASE_URL (a superuser DSN) in .development.env and run `ultra dev`, or set " + EnvOwnerDSN + " + " + EnvAuthDSN + " directly",
```
```go
		// owner == ""
		FixHint: "set ULTRABASE_DATABASE_URL (a superuser DSN) and run `ultra dev`, or set " + EnvOwnerDSN + " in .development.env",
```
```go
		// auth == ""
		FixHint: "set ULTRABASE_DATABASE_URL (a superuser DSN) and run `ultra dev`, or set " + EnvAuthDSN + " in .development.env",
```

In `ConnectCheck`, the two hints become:

```go
		// dsn == ""
		FixHint: "set ULTRABASE_DATABASE_URL (a superuser DSN) and run `ultra dev`, or set the role DSNs in .development.env",
```
```go
		// pool open error
		FixHint: "set ULTRABASE_DATABASE_URL (a superuser DSN) so `ultra dev` provisions the database",
```

In `roleLayoutDecision` (both the "missing roles" and "authenticator not granted" branches) and in `RoleLayoutCheck`'s two query-error branches, set:

```go
		FixHint: "set ULTRABASE_DATABASE_URL (a superuser DSN) and run `ultra dev` to provision the roles",
```

- [ ] **Step 2: Run preflight tests**

Run: `go test -race ./internal/cli/preflight/...`
Expected: PASS. (`preflight_test.go` only asserts hints are non-empty and that the s3 read-error hint does not mention `ultra init`; the new hints satisfy both.)

- [ ] **Step 3: Commit**

```bash
git add internal/cli/preflight/preflight.go
git commit -m "fix(cli): repoint preflight fix-hints at the superuser-DSN dev flow

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: Integration test — `ultra dev` bootstraps from a raw superuser container

Confirms the end-to-end dev path against a fresh Postgres that has none of the ultrabase roles.

**Files:**
- Modify: `internal/testutil/dbboot/container.go` (add a raw-container helper)
- Create: `internal/cli/dbsetup_integration_test.go`

- [ ] **Step 1: Add a raw-container helper to dbboot**

Append to `internal/testutil/dbboot/container.go`:

```go
// StartRawContainer launches a postgres testcontainer and returns the superuser
// connection string WITHOUT provisioning any ultrabase roles. Use it to test
// code that must bootstrap the role layout itself (e.g. ensureRoles).
func StartRawContainer(t *testing.T, image ...string) string {
	t.Helper()
	img := "postgres:16-alpine"
	if len(image) > 0 {
		img = image[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := pgcontainer.Run(ctx, img,
		pgcontainer.WithDatabase("ultrabase_test"),
		pgcontainer.WithUsername("postgres"),
		pgcontainer.WithPassword("postgres"),
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}
```

- [ ] **Step 2: Write the failing integration test**

Create `internal/cli/dbsetup_integration_test.go`:

```go
//go:build integration

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/saedx1/ultrabase/internal/testutil/dbboot"
)

// TestEnsureRolesBootstrapsFromSuperuser drives the full dev bootstrap path:
// against a raw superuser container with no ultrabase roles, ensureRoles must
// provision the layout, set the derived DSNs into the env, persist them, and
// leave a database the owner DSN can connect to with all five roles present.
func TestEnsureRolesBootstrapsFromSuperuser(t *testing.T) {
	superURL := dbboot.StartRawContainer(t)

	// No role DSNs in env → ensureRoles must bootstrap.
	t.Setenv("ULTRABASE_OWNER_DATABASE_URL", "")
	t.Setenv("ULTRABASE_AUTH_DATABASE_URL", "")

	envFile := filepath.Join(t.TempDir(), ".development.env")
	res, err := ensureRoles(context.Background(), superURL, envFile)
	if err != nil {
		t.Fatalf("ensureRoles: %v", err)
	}
	if !res.Ran {
		t.Fatal("ensureRoles did not run bootstrap on a fresh superuser DB")
	}

	// Derived DSNs were exported into the env.
	ownerDSN := os.Getenv("ULTRABASE_OWNER_DATABASE_URL")
	if ownerDSN == "" || os.Getenv("ULTRABASE_AUTH_DATABASE_URL") == "" {
		t.Fatal("ensureRoles did not set the derived DSNs in the env")
	}

	// And persisted to the env file.
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read persisted env file: %v", err)
	}
	if !strings.Contains(string(data), "ULTRABASE_OWNER_DATABASE_URL=") ||
		!strings.Contains(string(data), "ULTRABASE_AUTH_DATABASE_URL=") {
		t.Fatalf("env file missing derived DSNs:\n%s", data)
	}

	// The owner DSN connects and all five roles exist.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, ownerDSN)
	if err != nil {
		t.Fatalf("connect as derived owner: %v", err)
	}
	defer conn.Close(ctx)

	for _, role := range []string{"ultrabase_owner", "authenticator", "anon", "authenticated", "service_role"} {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role).Scan(&exists); err != nil {
			t.Fatalf("query role %s: %v", role, err)
		}
		if !exists {
			t.Errorf("role %q was not provisioned", role)
		}
	}
}
```

- [ ] **Step 3: Run the integration test (requires Docker)**

Run: `go test -tags=integration -race -run TestEnsureRolesBootstrapsFromSuperuser ./internal/cli/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/testutil/dbboot/container.go internal/cli/dbsetup_integration_test.go
git commit -m "test(cli): integration test for ensureRoles superuser bootstrap

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 7: Update docs

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md` (stale dev-requirements line)
- Modify: `internal/cli/bootstrap.go` (comment only)

- [ ] **Step 1: Update the bootstrap.go provenance comment**

In `internal/cli/bootstrap.go`, the `ownerRole`/`authenticatorRole` const comment says "provisioned by `ultra init`". Change the first comment line to:

```go
// ownerRole and authenticatorRole are the default login role names provisioned
// by `ultra dev` (via ensureRoles) when bootstrapping from a superuser DSN. They
// match the names dbboot uses for integration tests so a dev-bootstrapped
// project behaves identically to a test-container project.
```

- [ ] **Step 2: Update README onboarding**

In `README.md`, update the references found at the quickstart and env-var sections:

- Line ~52 / ~95 / ~102: replace `ultra dev --use-dsn` with `ultra dev`.
- Line ~90: replace `./ultra init --with-dsn postgres://superuser:pass@localhost:5432/mydb` with a two-line flow:
  ```
  # 1. point ultra at a superuser DSN
  export ULTRABASE_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres
  # 2. dev provisions roles on first run
  ./ultra dev
  ```
- Line ~101: replace `ultra init [--with-dsn <url>]        # Scaffold project; optionally bootstrap a DB` with `ultra init                          # Scaffold project (no DB calls)`.
- In the env-var table (lines ~344–345), add a row above the owner row:
  ```
  | `ULTRABASE_DATABASE_URL` | dev only | Superuser/privileged DSN. `ultra dev` provisions the role layout from it and writes the derived owner/authenticator DSNs to `.development.env`. Not used by `serve`. |
  ```
- Anywhere the README documents config/watch env vars, ensure the standardized names are used: `ULTRABASE_CONFIG`, `ULTRABASE_WATCH`, `ULTRABASE_WATCH_INTERVAL`. Grep to confirm none of the old `ULTRABASE_CONFIG_SOURCE` / `ULTRABASE_CONFIG_WATCH` / `ULTRABASE_CONFIG_WATCH_INTERVAL` names remain:

  Run: `grep -n "ULTRABASE_CONFIG_SOURCE\|ULTRABASE_CONFIG_WATCH\|with-dsn\|--use-dsn" README.md`
  Expected: no output.

- [ ] **Step 3: Update CLAUDE.md's stale dev-requirements line**

`CLAUDE.md` states `./ultra dev` *"(requires the two DB URLs + ULTRABASE_ADMIN_KEY in env …)"* — no longer true, since dev can derive the two role DSNs from a single `ULTRABASE_DATABASE_URL`. Update that parenthetical to reflect the superuser-DSN path, e.g.:

```
./ultra dev              # hot-reload dev server (set ULTRABASE_DATABASE_URL — a superuser DSN — and dev provisions the two role DSNs on first run; or set them directly. JWT keys are DB-managed via auth.jwt_keys)
```

- [ ] **Step 4: Repo-wide staleness sweep (excluding historical specs/plans)**

Run:
```bash
grep -rn "with-dsn\|--use-dsn\|ULTRABASE_CONFIG_SOURCE\|ULTRABASE_CONFIG_WATCH" \
  --include="*.md" . | grep -v "docs/superpowers/specs/" | grep -v "docs/superpowers/plans/"
```
Expected: no output. (The `docs/superpowers/specs` and `docs/superpowers/plans` trees are historical record — do NOT rewrite them. Fix any other hit, e.g. `docs/examples/react-catalog/README.md`.)

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md internal/cli/bootstrap.go docs/examples/
git commit -m "docs: document superuser-DSN dev flow and standardized env names

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

> **Non-blocking notes (so they aren't later filed as regressions):**
> - The provisioning log lines print *before* the `Ultrabase v…` banner, since `ensureRoles` runs before the banner block in `runDev`. Cosmetic only; reorder if desired.
> - `ultra doctor` on a project that has been `init`'d and given a superuser DSN but has **not yet run `dev`** will report `role layout` as unhealthy. This is correct: provisioning now happens in `dev`, and `doctor` is read-only by design — it does not bootstrap.

---

## Task 8: Full verification

- [ ] **Step 1: Build + unit tests**

Run: `go build ./... && go test -race ./...`
Expected: PASS.

- [ ] **Step 2: Integration tests for touched packages (requires Docker)**

Run: `go test -tags=integration -race ./internal/cli/... ./internal/testutil/...`
Expected: PASS.

- [ ] **Step 3: supabase-js compat suite (contract guard; requires Docker + node)**

Run: `go test -run TestSupabaseJSCompat -tags=integration -race ./internal/adapter/http/...`
Expected: PASS. (No HTTP/wire surface changed, but this is the load-bearing contract — confirm it stays green.)

- [ ] **Step 4: Manual smoke (optional, requires a local Postgres)**

```bash
go build -o ultra ./cmd/ultra
mkdir /tmp/ultra-smoke && cd /tmp/ultra-smoke
/path/to/ultra init smoke
cp .development.env.example .development.env
# edit .development.env: set ULTRABASE_DATABASE_URL to a reachable superuser DSN
/path/to/ultra dev
# expect: "✓ Provisioned roles from ULTRABASE_DATABASE_URL …" then a healthy boot,
# and .development.env now carries ULTRABASE_OWNER_DATABASE_URL + ULTRABASE_AUTH_DATABASE_URL.
```

---

## Self-Review notes (for the implementer)

- **Spec coverage:** Task 1 = standardization; Tasks 2–3 = dev bootstrap + ordering; Task 4 = init scaffolding + example; Task 5 = preflight hints; Task 6 = integration; Task 7 = docs/serve-unchanged note via README. `serve` has no bootstrap task by design (Non-goal).
- **Type consistency:** `roleBootstrap{Ran, EnvFile}`, `shouldBootstrap(owner, auth, superuser)`, `ensureRoles(ctx, superuserDSN, envFile)`, `persistDSNs(envFile, ownerDSN, authDSN)`, `scaffoldDevelopmentEnvExample()` are used identically wherever referenced.
- **`dbConnections` is intentionally NOT modified** — it is shared with `serve`, whose missing-DSN message must keep pointing only at the two role DSNs.
