# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Non-negotiables

<feedback_loop>
**Tests and the local feedback loop must be green before any push.** That means, at minimum:

- `go build ./...` succeeds
- `go test -race ./...` (unit) succeeds
- `go test -tags=integration -race ./...` (integration, requires a running Docker daemon) succeeds for any package you touched
- `npm test` inside `dashboard/` succeeds for any frontend change

Do not push, open a PR, or claim work is "done" until these pass locally. CI runs the same three jobs (`Go unit tests`, `Go integration tests`, `Dashboard tests`) in `.github/workflows/ci.yml` and gates the Docker build job on them. If you find yourself bypassing the loop ("I'll just push and let CI catch it"), stop and fix the loop first — a broken local feedback loop is a higher-priority bug than whatever you were working on.
</feedback_loop>

<supabase_js_compat>
**Ultrabase must stay wire-compatible with `@supabase/supabase-js`.** This is a load-bearing product promise, not a nice-to-have.

The contract is enforced by `internal/adapter/http/supabase_integration_test.go` (`TestSupabaseJSCompat`), which spins up Postgres in a container, boots the real ultrabase HTTP handler, then shells out to the Node harness in `test/integration/supabase-js/run.mjs` to drive `@supabase/supabase-js` against it. Any change touching auth, REST/PostgREST behavior, RPC, storage endpoints, error shapes, headers, or JWT/role handling MUST keep this test green.

Concrete rules that fall out of this:
- JWT `role` claim values are Supabase wire tokens (`anon`, `authenticated`, `service_role`) and must not be renamed even though the Postgres role names are configurable. See `internal/domain/database.go`.
- The `apikey` header, `Authorization: Bearer …`, PostgREST query operators (`eq`, `gt`, `like`, `order`, `limit`, embeds, `on_conflict`, `Prefer: return=…`, `Range`, etc.), error envelope shape, and `/auth/v1`, `/rest/v1`, `/storage/v1` URL prefixes are part of the contract.
- When adding a new feature exposed over HTTP, add coverage in `run.mjs` if `supabase-js` has a corresponding API surface — don't ship behavior that only the bespoke Go tests exercise.
</supabase_js_compat>

## Common commands

<commands>
**Build & run:**
```sh
go build -o inz ./cmd/inz
./inz dev              # hot-reload dev server (set INSTANCEZ_DATABASE_URL — a superuser DSN — and dev provisions the two role DSNs on first run; or set them directly. JWT keys are DB-managed via auth.jwt_keys)
./inz serve            # production mode
./inz validate         # YAML syntax check, no DB
docker compose -f docker-compose.dev.yaml up   # full stack: postgres + backend + dashboard
```

**Go tests:**
```sh
go test -race ./...                                    # unit
go test -tags=integration -race ./...                  # full integration (needs Docker)
go test -run TestSupabaseJSCompat -tags=integration -race ./internal/adapter/http/...   # the supabase-js compat suite
go test -run <RegExp> ./internal/...                   # single test
```
Integration tests are gated behind `//go:build integration` and use `internal/testutil/dbboot` to provision `ultrabase_owner` + `authenticator` roles inside a fresh testcontainers Postgres. They need `docker` on `PATH`; the supabase-js harness additionally needs `node` + `npm`.

**Dashboard (in `dashboard/`):**
```sh
npm ci
npm test          # vitest run
npm run test:watch
npm run dev       # vite, port 5173
npm run build     # tsc -b && vite build
```
</commands>

## Architecture (the parts that span files)

<architecture>
Hexagonal layout under `internal/`:

```
cmd/inz/main.go
        │
        ▼
internal/cli/         cobra commands (dev, serve, init, validate, deploy, doctor, status, login, …)
        │
        ▼
internal/app/         engine.go orchestrates lifecycle: migrate → seed → http + watcher
        │             — depends only on internal/domain interfaces
        ▼
internal/domain/      pure types + port interfaces (OwnerDB, RequestDB, Roles, Config, …)
        ▲
        │ implemented by
internal/adapter/     postgres (pgx pool), http (Gin handlers + PostgREST surface),
                      s3, resend, sendgrid
```

**Two Postgres logins, by design.** This is non-obvious and load-bearing:
- `INSTANCEZ_OWNER_DATABASE_URL` → privileged login (`CREATEROLE CREATEDB BYPASSRLS REPLICATION`). Used for migrations and seeding. Lives behind `domain.OwnerDB`.
- `INSTANCEZ_AUTH_DATABASE_URL` → `authenticator` login (`NOINHERIT`) that is granted `anon` / `authenticated` / `service_role`. Every query the request pool runs goes through a tx that issues `SET LOCAL ROLE`: CRUD endpoints pick the role from the validated JWT, system endpoints (auth/admin/mfa/storage) default to `service_role`. NOINHERIT is load-bearing — without an explicit role switch the login carries no table privileges, which is exactly what we want as a regression guard. Lives behind `domain.RequestDB`. See `internal/adapter/postgres/context.go` and `pool.go` (`buildSessionSetup`, the auto-wrap on `Query`/`QueryRow`/`Exec`).

**RLS is the only authorization layer.** There is no HTTP-level RBAC and no application-side role table. All access decisions are Postgres policies declared in `instancez.yaml` under each table's `rls:` block. The middleware's job is to validate the JWT and pick the right Postgres role; everything else is RLS. The `service_role` (used by the admin key path) has `BYPASSRLS`. See `internal/domain/database.go` for the `Roles` struct — wire JWT values (`anon`/`authenticated`/`service_role`) are fixed for supabase-js compat, but the Postgres role identifiers are configurable via `INSTANCEZ_DB_*_ROLE` env vars.

**YAML is the source of truth.** On boot, `internal/app/migrate.go` diffs `instancez.yaml` against the live database and applies migrations (gated by `--allow-destructive` for drops). `migrate_config_diff.go` is where the diff lives. The dev watcher (`watcher.go`) re-applies on file change.

**HTTP surface mirrors PostgREST + Supabase.** `internal/adapter/http/` contains `crud_handler.go`, `rpc_handler.go`, `storage_v1_handler.go`, `auth_handler.go`, `mfa_handler.go`. The `where.go` / `select.go` / `csv.go` files implement PostgREST query parsing. Handlers must stay parseable by `@supabase/supabase-js`. **Code functions** are served at `/functions/v1/<name>` by `functions_handler.go`; they run in Node.js worker processes managed by `internal/adapter/funcs/`. **The Postgres-RPC block was renamed from `functions:` to `rpc:` in Task 1** — `functions:` now declares code functions exclusively. Docs and examples must use `rpc:` for Postgres stored procedures.
</architecture>

## Things that look weird but are intentional

<gotchas>
- **Two DB URLs are required even for `validate`-adjacent flows.** Don't try to "simplify" by collapsing to one connection — RLS enforcement depends on the role-switch happening on a non-superuser login.
- **JWT role tokens are not Postgres role names.** `anon`/`authenticated`/`service_role` on the wire stay constant; the Postgres-side names are looked up from `domain.Roles` and may differ. Don't conflate them.
- **`auth.uid()` and `auth.is_authenticated()`** are Postgres functions installed by migrations, used inside RLS policy expressions. They read session GUCs set by the request middleware, not application memory. They live in the `auth` schema and are typically referenced from RLS policies on tables that FK to `auth.users.id`.
- **No auto-added columns, not even `id`.** The migrator does not inject primary keys. Every column, including PKs, must be declared in YAML.
- **Reserved schemas:** `auth` and `storage`. The migrator owns these schemas; user tables declaring `schema: auth` or `schema: storage` are rejected at validation. The auth user record lives at `auth.users`; profile data is a regular user-defined table FK'd to `auth.users.id`.
- **Underscore-prefixed names** are still reserved for framework-internal tables (`_ultrabase_migrations`).
- **The Lambda image is per-arch.** `Dockerfile.lambda` is built once per platform (`-lambda-amd64`, `-lambda-arm64`) because Lambda rejects multi-arch manifest lists. Don't "fix" this back to a manifest list.
</gotchas>
