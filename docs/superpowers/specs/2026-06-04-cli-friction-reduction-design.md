# CLI Friction Reduction Design

**Date:** 2026-06-04
**Status:** Draft

## Overview

The `ultra` CLI carries several avoidable friction points: a mandatory
`--use-*` backend choice when only one backend works, dead flags advertised in
help, sequential trial-and-error on misconfiguration, an `init` that errors
instead of completing on re-run, an extra `ultra login` round-trip before cloud
actions, a fire-and-forget deploy with no status surface, and a hardcoded
version string. This design eliminates them.

The work splits along a dependency line: **local CLI ergonomics** (Group A,
shippable today, no server changes) and a **cloud workflow** built on the
draft/production model (Group B, depends on cloud API endpoints). This is one
combined spec; cloud-dependent items are marked **[blocked-on-API]**.

## Goals

- `ultra dev` works with no flag against the DSN in `.development.env`.
- One command (`ultra doctor`) reports the full local environment health; every
  command fails early with the same checks instead of one error at a time.
- `ultra init` is safely re-runnable: keeps what exists, fills what's missing,
  never duplicates a cloud project.
- Cloud actions trigger login inline (prompt-first) instead of erroring.
- A `ultra status` command shows draft + production project state.
- `deploy` reflects the draft→production promotion model.
- `ultra version` reflects the real build.

## Non-goals / Out of scope

- Implementing the Docker dev backend (removed, not replaced).
- Server-side rate limiting / abuse controls on project creation and the
  device-code endpoint. These are assumed to exist in the cloud API; this CLI
  work depends on them but does not implement them.
- Multi-*project* deploy targets (`--project-id` override). The environment axis
  is draft/prod *within* a project, not multiple project ids.

---

## Group A — Local CLI ergonomics (no server dependency)

### A1. Remove dead backends; `dev` defaults to DSN

Today `resolveDevFlags` forces "exactly one of `--use-dsn`, `--use-docker`,
`--use-cloud-ephemeral`" (`internal/cli/flags.go`), but `--use-docker` and
`--use-cloud-ephemeral` only error as unimplemented (`dev.go`). So every user
types `--use-dsn` for the only working path.

Changes:
- Remove `--use-docker` (dev) and `--with-docker` (init) entirely, including the
  `DevDBSource` enum, `selectDevDBSource`, and the dead `switch` arms in
  `runDev`.
- `--use-cloud-ephemeral` is **renamed/reworked into `--use-cloud`** (see B3); it
  is *not* deleted.
- `ultra dev` with no backend flag runs against the DSN from `.development.env` /
  shell env. `dbConnections` already emits clear errors when the URLs are absent.
- `--use-dsn` is retained as a **hidden, deprecated no-op** (cobra
  `MarkHidden` + `MarkDeprecated`) so existing invocations don't break.
- Update `init` next-steps output (`init.go`) and the `./ultra dev --use-dsn`
  example in `CLAUDE.md` to `./ultra dev`.

### A2. Preflight + `ultra doctor`

A new `internal/cli/preflight` package holds composable checks, each returning a
pass/fail result with a one-line fix hint. Two consumption modes share the same
check functions:

- **Per-command preflight (fail-fast):** each command runs the subset relevant
  to it at startup and returns on the first failure, so misconfiguration is
  caught before any partial work.
- **`ultra doctor` (report-all):** runs the full local-dev set, prints every
  result with `✓`/`✗`, and exits non-zero if any check fails.

Check subsets:

| Command | Checks |
|---|---|
| `dev`, `serve` | config parse + `config.Validate`; both DSNs present; owner pool connects; auth pool connects; role layout exists (`ultrabase_owner`, `authenticator`, anon/authenticated/service) and `authenticator` is granted the API roles |
| `deploy` | config parse + validate; `project.cloud.project_id` present (cloud auth is handled by `ensureLoggedIn` at the cloud step, not fail-fast preflight, so inline login can prompt) |
| `validate`, `login`, `logout`, `whoami`, `version` | unchanged (minimal) |
| `ultra doctor` | full dev/serve set |

The role-layout check is the diagnosis that points users at `ultra init
--with-dsn` (A3) as the fix.

### A3. `init` becomes idempotent / re-runnable

`ultra init` should complete on re-run in an existing project: keep what's
there, fill what's missing, provision roles. Most files already merge/skip; the
two non-idempotent spots are fixed:

- **`ultrabase.yaml`:** currently a hard error if it exists. Change to
  keep-existing and continue, printing an "unchanged" line. `--force` still
  overwrites.
- **`--generate-like` over an existing yaml:** refuse unless `--force` — fail
  fast rather than spend tokens generating output that would be discarded.

Already idempotent (no change): `.gitignore` (append-only merge),
`.production.env.example` (skip-if-exists), `.development.env` (key-preserving
merge), DB roles (`IF NOT EXISTS` + password rotation in `bootstrap.go`).

### A4. Housekeeping

- `version` (`root.go`) becomes a package `var` injected via
  `-ldflags "-X .../internal/cli.version=$(git describe --tags --always)"`,
  defaulting to `"dev"` for a plain `go build`. Build tooling updated to pass it.
- Fix the stale `JWT_SECRET` reference in `CLAUDE.md`: JWT keys are DB-managed in
  `auth.jwt_keys` (`internal/app/jwtkeys.go`); no `JWT_SECRET` env var is read.
  The scaffolded `.development.env` (DSNs only) is already correct.

---

## Group B — Cloud workflow (draft/production model)

The cloud model is one project with a **draft** version and a **production**
version, **each backed by its own persistent (non-ephemeral) database** — the
draft DB is the working copy, the production DB serves live traffic. `dev
--use-cloud` runs against the draft; `deploy` promotes draft → production.

This is the same backend the agent-facing MCP surface drives (the `data`
service); the `ultra` CLI and the MCP tools are two clients of one cloud API.
The shared operations are therefore identical on both sides: `deploy` (CLI) and
`publish_app` (MCP) both call the backend's `DeployProject`
(`POST /ultrabase/projects/:id/deploy`) to promote draft → production, and the
draft↔production config diff behind `ultra`'s migration preview is the same
structural differ as the MCP `draft_dirty` flag. The backend-side contract
(deployment-status fields, `draft_dirty`, the two persistent DBs) is defined in
the `data` service's `mcp-friction-reduction` design; this spec consumes it
rather than redefining it.

### B1. Inline login

Extract `ensureLoggedIn(ctx, opts) (cloud.Credentials, error)` from `runLogin`
(`login.go`). Behavior:

- If `cloud.Load()` returns valid creds, return them unchanged.
- Otherwise, on a **TTY**, print "Creating a cloud project requires signing
  in." and prompt `[Y/n]`; on confirm, run the existing device-code flow, save
  creds, continue. `--yes` skips the prompt and starts the flow directly.
- On a **non-TTY** (CI), keep the current hard error pointing to `ultra login`
  so nothing hangs waiting on a browser confirmation.

Used by `init --with-cloud`, `init --generate-like`, and the cloud paths below.
The abuse boundary is unchanged: nothing is created until a human confirms the
device code in a browser, and the server enforces per-account limits on
`CreateProject` / the device-code endpoint.

### B2. `init --with-cloud` duplicate-project guard

`runInit` currently always calls `CreateProject`, so re-running creates a second
project. Guard it: if `project.cloud.project_id` is already present in the yaml,
skip creation and print "already linked to project <id>".

*(B1 and B2 have no new server dependency and ship with Group A.)*

### B3. `dev --use-cloud` [blocked-on-API]

Replaces the dead `--use-cloud-ephemeral` — renamed precisely because the draft
DB is **persistent, not ephemeral** (see the shared model above). Runs `ultra
dev` against the project's draft version, backed by the draft's own cloud
database.

- **Requires `init --with-cloud` first:** if no `project.cloud.project_id`,
  error with a pointer to `ultra init --with-cloud`. No implicit project
  creation from `dev`.
- Triggers inline login (B1) if unauthenticated.

**Depends on:** the draft's own database — which is the platform's **existing**
preview-environment DB (`<app_id>-preview`, provisioned by the deployer's preview
path), separate from the production DB. This is the same DB the MCP surface's
draft-targeted `run_sql` uses; the remaining work is exposing/connecting it for
`dev --use-cloud`. See the MCP spec's Dependencies. Endpoint shape TBD.

### B4. `deploy` = upload-then-promote

`deploy` keeps its current safety sequence (validate local yaml → fetch deployed
yaml → plan migration → confirm, with `-y` to skip) and reshapes the action to:

1. Upload local yaml to the project **draft** (existing `UploadYAML`).
2. Plan the migration from production to the draft (existing `MigrationPreview`).
3. On confirm, **promote draft → production**.

The promote is the existing `Deploy(projectID)` →
`POST /ultrabase/projects/:id/deploy` → `DeployProject` — the **same backend op
the MCP `publish_app` tool calls** (verified in the `data` service). No new
`Promote` method or endpoint is needed; `UploadYAML` and `MigrationPreview`
clients already exist. B4 is therefore **not** blocked-on-API.

Self-contained: works whether or not a `dev --use-cloud` session ran first.

**Caveat (shared backend):** step 1 overwrites the project draft with the local
yaml. If an agent is concurrently editing the same project's draft via MCP
`update_app`, `ultra deploy` will clobber those unpublished edits. Out of scope
to solve here, but worth noting since both clients write the one draft.

### B5. `ultra status`

New subcommand showing **draft + production side by side** for the current
project (state, current version, deploy status, project name/id/url). Reads
`project.cloud.project_id` from the yaml; triggers inline login if needed.

Adds a `GetApp(projectID)` method to `internal/cloud/client.go` that calls
`GET /ultrabase/projects/:id` (exists per the MCP support spec, 2026-05-31). The
response carries the **same deployment projection the MCP surface defines** in
the `data` service's `mcp-friction-reduction` design: a
`deployment { status, deployed_at, error }` object read from the production
version (`status` ∈ `building` / `build_done` / `deploying` / `deploy_done` /
`deploy_failed` / `not_ready`) plus a `draft_dirty` boolean. `ultra status`
renders those directly instead of inventing its own shape — one backend
`GetDeploymentState` projection behind both the HTTP endpoint and MCP `get_app`.

`doctor` (local environment health) and `status` (cloud app state) are
deliberately separate verbs.

---

## Architecture summary

| Unit | Responsibility | Depends on |
|---|---|---|
| `internal/cli/preflight` (new) | composable env/config/db checks; pass/fail + fix hint | `config`, `postgres`, `domain.Roles` |
| `ultra doctor` (new cmd) | run full local set, report all, exit code | `preflight` |
| `ensureLoggedIn` helper | shared TTY-gated inline device-code login | `cloud` |
| `GetApp` (new client method) | fetch app details + `deployment`/`draft_dirty` projection (shared with MCP `get_app`) | cloud API `GET /ultrabase/projects/:id` |
| `ultra status` (new cmd) | render draft+prod state | `GetApp`, `ensureLoggedIn` |

Extract the bootstrap-and-write-env logic currently inside `runInit` into a
shared helper so `init` re-runs and the role-layout fix path share one code path.

## Error handling

- Missing config / DSNs / roles surface through preflight with actionable hints
  (e.g. "roles missing — run `ultra init --with-dsn <dsn>`").
- `ultra doctor` never stops at the first failure; it reports the complete
  picture and exits non-zero if any check failed.
- Non-TTY cloud paths fail fast with the `ultra login` hint rather than blocking.
- `deploy` retains validate-and-plan-before-act; destructive migration steps stay
  gated behind the existing confirmation.

## Testing

- `preflight`: unit tests per check with injected fakes (env lookup, a fake DB
  reporting role presence/absence); table-driven pass/fail.
- `doctor`: asserts report-all behavior (does not short-circuit) and exit code.
- A1: `parseDevFlags` tests for no-flag default, hidden `--use-dsn` still
  accepted, removed flags rejected.
- A3: `init` re-run tests — existing yaml kept, `--force` overwrites,
  `--generate-like` refused without `--force`.
- B1: `ensureLoggedIn` TTY vs non-TTY branches with a fake prompt + device flow.
- B2: duplicate-project guard skips `CreateProject` when `project_id` present.
- B5: `GetApp` client test against an `httptest` server; status render snapshot.
- The supabase-js compat suite is unaffected (no HTTP-surface change), but
  `go build ./...`, `go test -race ./...`, and touched-package integration tests
  must stay green per the feedback-loop non-negotiable.

## Dependencies / sequencing

Group A + B1 + B2 are unblocked and implemented first.

- **B4 is also unblocked:** the promote is the existing `Deploy(projectID)` →
  `POST /ultrabase/projects/:id/deploy` → `DeployProject` (verified — the same op
  MCP `publish_app` calls); the `UploadYAML` and `MigrationPreview` clients
  already exist.
- **B3** and **B5** remain **[blocked-on-API]**:
  - B3: the draft's own database = the platform's **existing** preview-environment
    DB (`<app_id>-preview`); remaining work is exposing/connecting it for
    `dev --use-cloud`. Shared with the MCP surface's draft-targeted `run_sql`.
  - B5: `GET /ultrabase/projects/:id` exists; confirm it exposes the
    `deployment` + `draft_dirty` projection the MCP design defines.
