# CLI Superuser-DSN Bootstrap + Flag/Env Standardization

**Date:** 2026-06-09
**Status:** Draft

## Overview

Onboarding to `ultra dev` today requires either `ultra init --with-dsn <url>`
(which bootstraps the two-login role layout and writes `.development.env`) or
hand-setting both `ULTRABASE_OWNER_DATABASE_URL` and
`ULTRABASE_AUTH_DATABASE_URL`. This design makes "give the CLI a single
superuser connection string and let it provision the rest" the primary path,
and moves role bootstrapping out of `init` (which becomes pure scaffolding) into
the run commands `dev` and `serve`.

It also standardizes the CLI's flag↔env-var contract, which has drifted: some
flags carry bespoke env names (`ULTRABASE_CONFIG_WATCH`), some carry none, and
connection strings are inconsistently modeled.

## Goals

- A user can run `ultra dev` (or `ultra serve`) with **only** a superuser DSN
  set (`ULTRABASE_DATABASE_URL`); the command provisions `ultrabase_owner` +
  `authenticator` + the three API roles, then proceeds — and **says so in the
  logs**.
- `ultra init` only scaffolds files. It writes a `.development.env.example`
  documenting the superuser flow; it never touches a database.
- Derived owner/authenticator DSNs are **persisted** so subsequent runs reuse
  them and skip re-bootstrapping.
- One standardized rule governs flags and env vars:
  - **DSNs are env-var-only** (no flags).
  - **Every other flag** has both the flag and exactly one
    `ULTRABASE_<FLAG_UPPER_SNAKE>` env var — no bespoke names.

## Non-goals / Out of scope

- Changing the two-login architecture. The superuser DSN is used **only** to
  bootstrap roles; the request path still runs as the NOINHERIT `authenticator`
  that `SET LOCAL ROLE`s per query. RLS remains the only authorization layer.
- Collapsing `ultrabase_owner` into the superuser login. We keep creating
  `ultrabase_owner` so prod/dev object-ownership parity holds and
  `preflight.RoleLayoutCheck` stays satisfied unchanged.
- Backward-compatible env-var aliases. The rename to standardized names is a
  **hard break** (see below) — no legacy fallbacks.
- Multi-replica / orchestrator secret management. For `serve` against an
  `s3://` config source, bootstrap is in-memory only; persisting role DSNs is a
  local-file concern.

---

## The standard: flags vs. env vars

The generic binding rule already lives in `applyEnvDefaults` (`flags.go`): for a
flag not set on the command line, fall back to `ULTRABASE_<FLAG_UPPER_SNAKE>`.
This becomes the **single** rule.

**DSNs are env-var-only.** Connection strings are not flags. The three DSN env
vars are read directly via `os.Getenv` *after* dotenv load, exactly as the two
existing ones already are:

| Env var | Role |
|---|---|
| `ULTRABASE_DATABASE_URL` | **New.** Privileged/superuser login used to bootstrap roles. |
| `ULTRABASE_OWNER_DATABASE_URL` | Owner login (migrations/seeding). |
| `ULTRABASE_AUTH_DATABASE_URL` | Authenticator login (request path). |

There is **no** `--database-url` flag. Modeling the superuser DSN as a plain
env var (rather than a flag) keeps it consistent with its two siblings and
sidesteps an ordering hazard: cobra resolves flag env-fallbacks in `RunE`
*before* `runDev`/`runServe` load the dotenv file, so a value living only in
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
  passwords. No longer called by `init`; now called by the shared helper below.

### `internal/cli/dbsetup.go`

- **New `ensureRoles`** — the shared bootstrap-or-skip helper, logging-agnostic
  so both callers can log in their own style:

  ```go
  type roleBootstrap struct {
      Ran       bool   // bootstrap executed this run
      Persisted bool   // derived DSNs written to EnvFile
      EnvFile   string
  }

  func ensureRoles(ctx context.Context, superuserDSN, envFile string, persist bool) (roleBootstrap, error)
  ```

  Precedence:
  1. If **both** `ULTRABASE_OWNER_DATABASE_URL` and
     `ULTRABASE_AUTH_DATABASE_URL` are set → return `{}` (skip; use them).
  2. Else if `superuserDSN != ""` → `bootstrapDB`, then `os.Setenv` both derived
     DSNs (so the existing preflight checks and `dbConnections`, which read
     `os.Getenv`, just work), then if `persist && envFile != ""` write them into
     `envFile` (create with a neutral header if absent, else `mergeEnvFile`).
     Return `{Ran:true, Persisted:…, EnvFile:envFile}`.
  3. Else → return `{}` (nothing to do; the caller's existing missing-DSN error
     path fires with updated messaging).

- **`dbConnections`** error messages updated to also mention the superuser
  option, e.g. *"set `ULTRABASE_DATABASE_URL` (a superuser DSN) to have the
  roles provisioned automatically, or set `ULTRABASE_OWNER_DATABASE_URL` +
  `ULTRABASE_AUTH_DATABASE_URL` directly."*

- `scaffoldDevelopmentEnv`'s header comment is reworded (no longer
  "Auto-generated by `ultra init --with-dsn`"); the create-from-empty path uses
  a neutral header describing the dev/serve bootstrap origin.

### `internal/cli/dev.go` (`runDev`)

The ordering crux. Today `runDev` does: `LoadDotenv` → preflight
(`DSNPresent`, `OwnerConnect`, `AuthConnect`, `RoleLayout`) → connect. On a
superuser-only fresh DB **all four preflight checks fail before bootstrap could
run**. So bootstrap moves *between* dotenv load and preflight:

1. `requireLocalConfig`
2. `config.LoadDotenv(".development.env")`
3. **`ensureRoles(ctx, os.Getenv("ULTRABASE_DATABASE_URL"), ".development.env", true)`**
   — `dev` is always a local file, so `persist=true`.
4. preflight checks (now pass: env vars set + roles provisioned)
5. rest unchanged

Logging on `Ran`:
```
  Bootstrapping roles from ULTRABASE_DATABASE_URL (superuser login)...
  ✓ Roles provisioned (ultrabase_owner + authenticator + anon/authenticated/service_role)
  ✓ Wrote derived owner + authenticator DSNs to .development.env
```

### `internal/cli/serve.go` (`runServe`)

`serve` has **no** DSN/connect/role preflight (only `ConfigValidCheck`), so
there is no ordering hazard — bootstrap slots in right before `dbConnections`,
after `.production.env` is loaded and the source type is known:

- Persistence is **source-aware**: `persist = source is *config.FileSource`. For
  an `s3://` source there is no local dotenv to write, so bootstrap is in-memory
  only (`os.Setenv`).
- Call: `ensureRoles(ctx, os.Getenv("ULTRABASE_DATABASE_URL"), ".production.env", persist)`.
- Logging goes through the JSON `slog` logger (matching serve's stream), e.g.
  `logger.Info("provisioned roles from superuser DSN", "persisted", res.Persisted, "env_file", res.EnvFile)`.
  When not persisted, an additional `logger.Info`/`Warn` notes the derived DSNs
  will re-derive on restart and that setting the two role DSNs explicitly is the
  stable path.

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
- `.production.env.example` is reworded to lead with `ULTRABASE_DATABASE_URL` as
  the simple path, noting the two explicit role DSNs as the advanced override.

### `internal/cli/preflight/preflight.go`

The `with-dsn` fix-hints are repointed at the new flow (≈7 occurrences:
`DSNPresentCheck` ×3, `ConnectCheck` ×2, `RoleLayoutCheck` ×2). New wording,
e.g.: *"set `ULTRABASE_DATABASE_URL` (a superuser DSN) and re-run, or set
`ULTRABASE_OWNER_DATABASE_URL` + `ULTRABASE_AUTH_DATABASE_URL`."* No logic
changes — `RoleLayoutCheck`'s `expectedRoleNames()` is still satisfied because
`bootstrapDB` still creates `ultrabase_owner`.

---

## Data flow (dev, superuser-only first run)

```
ultra dev
  └─ LoadDotenv(.development.env)         # ULTRABASE_DATABASE_URL visible
  └─ ensureRoles(superuser, ".development.env", persist=true)
        owner+auth set?  no
        superuser set?   yes
        → bootstrapDB(superuser)          # CREATE/ALTER roles, rotate passwords
        → os.Setenv(OWNER_DSN), os.Setenv(AUTH_DSN)
        → write both into .development.env (merge or create)
        → {Ran:true, Persisted:true}
  └─ preflight: DSNPresent ✓  OwnerConnect ✓  AuthConnect ✓  RoleLayout ✓
  └─ dbConnections() → owner + authenticator pools
  └─ engine.Start
```

Second run: `.development.env` now carries owner+auth → precedence #1 → skip
bootstrap. Password rotation churn is thereby avoided; to force a fresh
rotation a user removes the two derived lines (or the file).

## Error handling

- `bootstrapDB` failure (bad superuser DSN, insufficient privilege) surfaces as
  a wrapped error from `ensureRoles`; `runDev`/`runServe` return it before
  preflight/connect.
- Neither superuser nor role DSNs set: `ensureRoles` is a no-op; `dev` fails at
  `DSNPresentCheck` (updated hint), `serve` fails at `dbConnections` (updated
  message).
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
- **`bootstrapDB`-from-dev/serve** — stays integration-tested (Docker) via the
  existing dbboot harness; add/adjust a test that `ultra dev` against a
  superuser-only container reaches a healthy boot.
- **`init_test.go`** — drop `--with-dsn` cases; assert `.development.env.example`
  is written and no DB call occurs.
- Feedback loop per CLAUDE.md: `go build ./...`, `go test -race ./...`, and the
  integration suite for touched packages must be green before push.

## Touch-point checklist

- [ ] `internal/cli/flags.go` — remove alias maps; minimal no-binding map; no DSN flag
- [ ] `internal/cli/dbsetup.go` — `ensureRoles`; `dbConnections` messages; env header
- [ ] `internal/cli/dev.go` — bootstrap between dotenv and preflight; logging
- [ ] `internal/cli/serve.go` — bootstrap before connect; source-aware persist; logging
- [ ] `internal/cli/init.go` — remove `--with-dsn`; `.development.env.example`; help + next-steps
- [ ] `internal/cli/preflight/preflight.go` — repoint `with-dsn` fix-hints
- [ ] tests: `flags_config_test.go`, `init_test.go`, `dbsetup_test.go`, integration
- [ ] `README.md` — update onboarding to the superuser-DSN flow + standardized env names
