# Embedded Postgres for `inz dev`

**Date:** 2026-06-15
**Status:** Draft

## Overview

Add `--embedded-pg` to `inz dev` so developers can run a full local instancez stack without installing or running an external Postgres. A persistent data directory (`./pgdata/`) is used so data survives restarts. A `--reset-pg` flag wipes it for a clean slate.

## Motivation

The current `inz dev` flow requires an external Postgres and a superuser DSN in the environment. This is the main setup friction for new users. Embedded Postgres eliminates it: `inz dev --embedded-pg` is the full setup.

## Removed: `--use-cloud`

`--use-cloud` / `DevDBSourceCloud` is removed as part of this work. It was stubbed out and returned an error immediately — dead code. Removing it simplifies the source model to two modes: DSN (default) and embedded.

## Library

**`github.com/fergusstrange/embedded-postgres`** — new dependency.

- Downloads pre-built Postgres binaries from Maven Central on first use, cached in `~/.embedded-postgres-go/<version>/` (~30MB, one-time per platform)
- Starts a real Postgres 16 process; full SQL compatibility
- Supported platforms: Linux amd64/arm64, macOS amd64/arm64 (M1/M2), Windows amd64
- Postgres version pinned to 16 to match the `postgres:16-alpine` image used in integration tests

## Flags

Two new flags on `inz dev`:

```
--embedded-pg        use embedded Postgres (./pgdata/ next to instancez.yaml)
--reset-pg           wipe ./pgdata/ before starting (requires --embedded-pg)
```

`--reset-pg` without `--embedded-pg` is an error caught in `resolveDevFlags`.

## Data Directory

`./pgdata/` relative to the `instancez.yaml` file location. Already covered by the `pgdata/` entry in `scaffoldGitignore()` — no changes needed to `inz init`.

## Startup Sequence

```
1. [new] if --reset-pg: wipe ./pgdata/
2. [new] start embedded PG 16 → random local port, data dir ./pgdata/
3. [new] inject superuser DSN as INSTANCEZ_DATABASE_URL in-process
4. [existing] ensureRoles → provisions owner + authenticator → writes .development.env
5. [existing] preflight checks
6. [existing] migrate, HTTP server, watcher
7. [new] on shutdown: stop embedded PG process
```

The embedded PG start happens before the `requireLocalConfig` / `ensureRoles` block in `runDev`. Everything from `ensureRoles` onward is unchanged — it just has a working `INSTANCEZ_DATABASE_URL` to operate against.

Startup overhead: ~1–2s after first download (real Postgres process init). First ever run adds a one-time ~30MB download.

## Code Changes

### `internal/cli/flags.go`
- Remove `DevDBSourceCloud` constant and `useCloud` field from `devFlagSet`
- Remove `--use-cloud` flag registration in `newDevFlagSet`
- Remove cloud→source mapping in `resolveDevFlags`
- Add `DevDBSourceEmbedded` constant
- Add `--embedded-pg` and `--reset-pg` bool fields + flag registrations
- Validate `--reset-pg` requires `--embedded-pg` in `resolveDevFlags`
- Add `pgDataDir` (derived: dir of configPath + `pgdata`) to `devOptions`
- Add `resetPG` bool to `devOptions`

### `internal/cli/dev.go`
- Remove `DevDBSourceCloud` case from the `switch opts.dbSrc` block
- Add `DevDBSourceEmbedded` case: call `startEmbeddedPostgres(opts)`, set `INSTANCEZ_DATABASE_URL` in-process, defer stop
- Extract a `startEmbeddedPostgres(opts devOptions) (stop func(), err error)` helper

### `go.mod` / `go.sum`
- Add `github.com/fergusstrange/embedded-postgres`

## Error Handling

- Unsupported platform (e.g., Windows arm64): the library fails to find a binary; surface as a startup error with hint: "use `INSTANCEZ_DATABASE_URL` with a full Postgres installation"
- `--reset-pg` on a non-existent `./pgdata/`: no-op (not an error)
- Extension errors (`pgvector`, PostGIS, etc.) surface naturally from the migration step — no special handling; hint in the migration error is sufficient

## What Does Not Change

- Integration tests: still use testcontainers (`postgres:16-alpine`) — embedded-postgres is not used in tests
- The two-DSN model (`INSTANCEZ_OWNER_DATABASE_URL` + `INSTANCEZ_AUTH_DATABASE_URL`): still provisioned by `ensureRoles` from the superuser DSN as before
- All preflight checks, migration, HTTP surface, RLS, JWT handling — unchanged
- `.development.env` persistence — unchanged
