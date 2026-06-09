# CLI Superuser-DSN Dev Bootstrap + Flag/Env Standardization

**Date:** 2026-06-09
**Status:** Draft

## Overview

Onboarding to `ultra dev` today requires either `ultra init --with-dsn <url>`
(which bootstraps the two-login role layout and writes `.development.env`) or
hand-setting both `ULTRABASE_OWNER_DATABASE_URL` and
`ULTRABASE_AUTH_DATABASE_URL`. This design makes "give the CLI a single
superuser connection string and let it provision the rest" the primary dev
path, and moves role bootstrapping out of `init` (which becomes pure
scaffolding) into `dev`.

It also standardizes the CLI's flag↔env-var contract, which has drifted: some
flags carry bespoke env names (`ULTRABASE_CONFIG_WATCH`), some carry none, and
connection strings are inconsistently modeled.

`serve` (production) is **unchanged** by this work: it continues to require the
two explicit role DSNs. How those roles get provisioned for production is a
separate concern, deferred (see Non-goals).

## Goals

- A user can run `ultra dev` with **only** a superuser DSN set
  (`ULTRABASE_DATABASE_URL`); the command provisions `ultrabase_owner` +
  `authenticator` + the three API roles, then proceeds — and **says so in the
  logs**.
- `ultra init` only scaffolds files. It writes a `.development.env.example`
  documenting the superuser flow; it never touches a database.
- Derived owner/authenticator DSNs are **persisted** to `.development.env` so
  subsequent `dev` runs reuse them and skip re-bootstrapping.
- One standardized rule governs flags and env vars:
  - **DSNs are env-var-only** (no flags).
  - **Every other flag** has both the flag and exactly one
    `ULTRABASE_<FLAG_UPPER_SNAKE>` env var — no bespoke names.

## Non-goals / Out of scope

- **Production role provisioning.** `serve` keeps requiring
  `ULTRABASE_OWNER_DATABASE_URL` + `ULTRABASE_AUTH_DATABASE_URL`. We are
  deliberately *not* adding a superuser-bootstrap path to `serve` in this work:
  bootstrapping rotates passwords each run and the derived credentials need a
  durable store, which doesn't map onto an orchestrated (e.g. `s3://`-config)
  deployment. How prod roles get provisioned — init scripts, a dedicated
  provisioning command, the cloud control plane — is left for a later design.
- Changing the two-login architecture. The superuser DSN is used **only** to
  bootstrap roles in dev; the request path still runs as the NOINHERIT
  `authenticator` that `SET LOCAL ROLE`s per query. RLS remains the only
  authorization layer.
- Collapsing `ultrabase_owner` into the superuser login. We keep creating
  `ultrabase_owner` so prod/dev object-ownership parity holds and
  `preflight.RoleLayoutCheck` stays satisfied unchanged.
- Backward-compatible env-var aliases. The rename to standardized names is a
  **hard break** — no legacy fallbacks.

---

## The standard: flags vs. env vars

The generic binding rule already lives in `applyEnvDefaults` (`flags.go`): for a
flag not set on the command line, fall back to `ULTRABASE_<FLAG_UPPER_SNAKE>`.
This becomes the **single** rule.

**DSNs are env-var-only.** Connection strings are not flags. They are read
directly via `os.Getenv` *after* dotenv load, exactly as the two existing ones
already are:

| Env var | Role | Consumed by |
|---|---|---|
| `ULTRABASE_DATABASE_URL` | **New.** Privileged/superuser login used to bootstrap roles. | `dev` only |
| `ULTRABASE_OWNER_DATABASE_URL` | Owner login (migrations/seeding). | `dev`, `serve` |
| `ULTRABASE_AUTH_DATABASE_URL` | Authenticator login (request path). | `dev`, `serve` |

There is **no** `--database-url` flag. Modeling the superuser DSN as a plain
env var (rather than a flag) keeps it consistent with its two siblings and
sidesteps an ordering hazard: cobra resolves flag env-fallbacks in `RunE`
*before* `runDev` loads the dotenv file, so a value living only in
`.development.env` would be invisible to flag resolution. A direct post-dotenv
`os.Getenv` read has no such gap.

**Every other flag gets the generic env var.** The bespoke alias maps are
removed. Concrete renames (**hard break — old names stop working**):

| Flag | Old env var | New env var |
|---|---|---|
| `config` | `ULTRABASE_CONFIG_SOURCE` (+ legacy `ULTRABASE_CONFIG`) | `ULTRABASE_CONFIG` |
| `watch` | `ULTRABASE_CONFIG_WATCH` | `ULTRABASE_WATCH` |
| `watch-interval` | `ULTRABASE_CONFIG_WATCH_INTERVAL` | `ULTRABASE_WATCH_INTERVAL` |
| `verbose` | *(none)* | `ULTRABASE_VERBOSE` |
| `port`, `data`, `migrate`, `allow-destructive`, `dashboard`, `use-cloud` | `ULTRABASE_<FLAG>` | unchanged |

`no-watch` stays a pure CLI convenience (the env way to disable watching is
`ULTRABASE_WATCH=false`) and the deprecated hidden `use-dsn` keep **no** env
binding. After removing the alias maps, these two are the only entries left in a
minimal "no env binding" map; everything else flows through the generic rule.

---

## Component changes

### `internal/cli/flags.go`

- Delete `configEnvAliases`, `serveEnvAliases`, and most of `devEnvAliases`.
- Keep a minimal map of flags that intentionally have **no** env binding:
  `dev`'s `no-watch` and `use-dsn`. `serve` needs none (nil/empty map → all
  generic).
- No `--database-url` flag is added anywhere.
- Everything else is unchanged: `applyEnvDefaults`' generic fallback now governs
  `config`, `watch`, `watch-interval`, `verbose`, etc.

### `internal/cli/bootstrap.go`

- `bootstrapDB(ctx, privilegedDSN)` is reused **as-is** — it already creates the
  full role layout idempotently and returns derived owner/auth DSNs with fresh
  passwords. No longer called by `init`; now called by the dev helper below.

### `internal/cli/dbsetup.go`

- **New `ensureRoles`** — the bootstrap-or-skip helper used by `dev`:

  ```go
  type roleBootstrap struct {
      Ran     bool   // bootstrap executed this run
      EnvFile string // file the derived DSNs were written to
  }

  func ensureRoles(ctx context.Context, superuserDSN, envFile string) (roleBootstrap, error)
  ```

  Precedence:
  1. If **both** `ULTRABASE_OWNER_DATABASE_URL` and
     `ULTRABASE_AUTH_DATABASE_URL` are set → return `{}` (skip; use them).
  2. Else if `superuserDSN != ""` → `bootstrapDB`, then `os.Setenv` both derived
     DSNs (so the existing preflight checks and `dbConnections`, which read
     `os.Getenv`, just work), then persist them into `envFile` (create with a
     neutral header if absent, else `mergeEnvFile`). Return
     `{Ran:true, EnvFile:envFile}`.
  3. Else → return `{}` (nothing to do; the caller's existing missing-DSN error
     path fires).

- `dbConnections` is **unchanged**. It is shared with `serve`, so its
  missing-DSN error keeps pointing only at the two role DSNs — the superuser
  hint lives in the dev-facing preflight checks instead (see below).

- `scaffoldDevelopmentEnv`'s header comment is reworded (no longer
  "Auto-generated by `ultra init --with-dsn`"); the create-from-empty path uses
  a neutral header describing the dev bootstrap origin.

### `internal/cli/dev.go` (`runDev`)

The ordering crux. Today `runDev` does: `LoadDotenv` → preflight
(`DSNPresent`, `OwnerConnect`, `AuthConnect`, `RoleLayout`) → connect. On a
superuser-only fresh DB **all four preflight checks fail before bootstrap could
run**. So bootstrap moves *between* dotenv load and preflight:

1. `requireLocalConfig`
2. `config.LoadDotenv(".development.env")`
3. **`ensureRoles(ctx, os.Getenv("ULTRABASE_DATABASE_URL"), ".development.env")`**
4. preflight checks (now pass: env vars set + roles provisioned)
5. rest unchanged

Logging on `Ran`:
```
  Bootstrapping roles from ULTRABASE_DATABASE_URL (superuser login)...
  ✓ Roles provisioned (ultrabase_owner + authenticator + anon/authenticated/service_role)
  ✓ Wrote derived owner + authenticator DSNs to .development.env
```

### `internal/cli/serve.go`

**No bootstrap changes.** `serve` continues to require the two role DSNs via the
unchanged `dbConnections`. The only effect on `serve` is indirect: removing the
alias maps in `flags.go` standardizes its `config` / `watch` / `watch-interval`
env-var names (hard rename, per the standard above).

### `internal/cli/init.go` (`runInit`)

- **Hard-remove** `--with-dsn`: the flag, the `withDSN` field, the bootstrap
  block, and the conditional `.development.env` write.
- Add a `.development.env.example` scaffold (mirrors the existing
  `.production.env.example` pattern), documenting the superuser flow:
  ```
  # Copy to .development.env, then set a superuser/privileged Postgres DSN.
  # `ultra dev` provisions ultrabase_owner + authenticator + the API roles from
  # it on first run and writes the derived DSNs back into this file.
  ULTRABASE_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres
  ```
- Update the `Long` help (drop the `--with-dsn` paragraph) and the next-steps
  block:
  ```
  cp .development.env.example .development.env   # set ULTRABASE_DATABASE_URL
  ultra dev
  ```
- `.production.env.example` is **unchanged** — it keeps documenting the two
  explicit role DSNs, since `serve` does not bootstrap.

### `internal/cli/preflight/preflight.go`

The `with-dsn` fix-hints are repointed at the new dev flow (≈7 occurrences:
`DSNPresentCheck` ×3, `ConnectCheck` ×2, `RoleLayoutCheck` ×2 — these checks are
dev/doctor-facing, not run by `serve`). New wording, e.g.: *"set
`ULTRABASE_DATABASE_URL` (a superuser DSN) and re-run `ultra dev`, or set
`ULTRABASE_OWNER_DATABASE_URL` + `ULTRABASE_AUTH_DATABASE_URL`."* No logic
changes — `RoleLayoutCheck`'s `expectedRoleNames()` is still satisfied because
`bootstrapDB` still creates `ultrabase_owner`.

---

## Data flow (dev, superuser-only first run)

```
ultra dev
  └─ LoadDotenv(.development.env)         # ULTRABASE_DATABASE_URL visible
  └─ ensureRoles(superuser, ".development.env")
        owner+auth set?  no
        superuser set?   yes
        → bootstrapDB(superuser)          # CREATE/ALTER roles, rotate passwords
        → os.Setenv(OWNER_DSN), os.Setenv(AUTH_DSN)
        → write both into .development.env (merge or create)
        → {Ran:true}
  └─ preflight: DSNPresent ✓  OwnerConnect ✓  AuthConnect ✓  RoleLayout ✓
  └─ dbConnections() → owner + authenticator pools
  └─ engine.Start
```

Second run: `.development.env` now carries owner+auth → precedence #1 → skip
bootstrap. Password rotation churn is thereby avoided; to force a fresh
rotation a user removes the two derived lines (or the file).

## Error handling

- `bootstrapDB` failure (bad superuser DSN, insufficient privilege) surfaces as
  a wrapped error from `ensureRoles`; `runDev` returns it before preflight.
- Neither superuser nor role DSNs set: `ensureRoles` is a no-op; `dev` fails at
  `DSNPresentCheck` (updated hint).
- Persist write failure is returned (not swallowed) — a half-bootstrapped DB
  with no recorded DSNs would be confusing.

## Testing

- **`ensureRoles` precedence** — unit-tested with an injected `getenv` and a
  fake bootstrap function (or by table-driving the env preconditions): both-set
  → skip; superuser-only → bootstrap+persist; neither → no-op. No DB.
- **Persist/merge** — reuses already-tested `mergeEnvFile`; add a case for the
  create-from-empty header path.
- **Flag/env standardization** — `flags_config_test.go` updated: assert
  `ULTRABASE_CONFIG` / `ULTRABASE_WATCH` / `ULTRABASE_WATCH_INTERVAL` /
  `ULTRABASE_VERBOSE` resolve, and that the old names no longer bind.
- **`bootstrapDB`-from-dev** — stays integration-tested (Docker) via the
  existing dbboot harness; add/adjust a test that `ultra dev` against a
  superuser-only container reaches a healthy boot.
- **`init_test.go`** — drop `--with-dsn` cases; assert `.development.env.example`
  is written and no DB call occurs.
- Feedback loop per CLAUDE.md: `go build ./...`, `go test -race ./...`, and the
  integration suite for touched packages must be green before push.

## Touch-point checklist

- [ ] `internal/cli/flags.go` — remove alias maps; minimal no-binding map; no DSN flag
- [ ] `internal/cli/dbsetup.go` — `ensureRoles`; reworded env header (`dbConnections` unchanged)
- [ ] `internal/cli/dev.go` — bootstrap between dotenv and preflight; logging
- [ ] `internal/cli/init.go` — remove `--with-dsn`; `.development.env.example`; help + next-steps
- [ ] `internal/cli/preflight/preflight.go` — repoint `with-dsn` fix-hints
- [ ] tests: `flags_config_test.go`, `init_test.go`, `dbsetup_test.go`, integration
- [ ] `README.md` — update onboarding to the superuser-DSN dev flow + standardized env names
- [ ] `serve.go` — no bootstrap change (verify only flag-env standardization carries through)
