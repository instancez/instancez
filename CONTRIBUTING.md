# Contributing to instancez

Thanks for considering a contribution. This guide covers the dev setup, the test loop you need to keep green, and how the codebase is organized.

## Before you start

For anything larger than a small fix, open an issue first so we can agree on the approach. It saves you from building something that does not fit the design. Small fixes (typos, obvious bugs, doc corrections) can go straight to a pull request.

## Dev setup

You need:

- Go 1.25 or newer
- Postgres 14 or newer (a local instance or Docker)
- Node.js 22 or newer (only if you work on the dashboard or on code functions)
- Docker (only for integration tests)

Build and run:

```sh
go build -o inz ./cmd/inz
export INSTANCEZ_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres
./inz dev
```

The dev server provisions the Postgres roles it needs on first run and reloads when you save `instancez.yaml`. For the full stack including the dashboard, use Docker:

```sh
docker compose -f docker-compose.dev.yaml up
```

## The test loop

Keep all of this green before you push or open a pull request:

```sh
go build ./...
golangci-lint run                         # same linter CI runs
go test -race ./...                       # unit tests
go test -tags=integration -race ./...     # integration tests, needs Docker
npm test                                  # inside dashboard/, for any frontend change
```

CI runs the same jobs. If your change touches a package, run that package's integration tests too.

## Supabase compatibility is a hard contract

instancez stays wire-compatible with `@supabase/supabase-js`. This is a product promise, not a preference. The contract is enforced by `TestSupabaseJSCompat` in `internal/adapter/http/`, which boots a real instancez instance and drives the actual Supabase client against it.

Any change that touches auth, REST or PostgREST behavior, RPC, storage, error shapes, headers, or JWT and role handling must keep that test green. If you add a feature that has a `supabase-js` API surface, add coverage to the Node harness in `test/integration/supabase-js/run.mjs`. Do not ship behavior that only the Go tests exercise.

A few specifics that fall out of the contract:

- The JWT `role` claim values (`anon`, `authenticated`, `service_role`) are fixed wire tokens. Do not rename them, even though the underlying Postgres role names are configurable.
- The `apikey` header, `Authorization: Bearer` scheme, PostgREST query operators, the error envelope shape, and the `/auth/v1`, `/rest/v1`, `/storage/v1` URL prefixes are all part of the contract.

## How the code is laid out

The backend uses a hexagonal layout under `internal/`:

- `cmd/inz/` is the entry point.
- `internal/cli/` holds the cobra commands (`dev`, `serve`, `init`, `validate`, and others).
- `internal/app/` orchestrates the lifecycle: migrate, then serve HTTP plus the file watcher. It depends only on domain interfaces.
- `internal/domain/` holds pure types and the port interfaces (`OwnerDB`, `RequestDB`, `Roles`, `Config`, and so on).
- `internal/adapter/` implements those ports: Postgres via pgx, the HTTP and PostgREST surface via Gin, plus S3 and email adapters.

Two things in here look unusual and are deliberate:

- **Two Postgres logins.** A privileged owner login runs migrations and DDL. A separate non-privileged `authenticator` login serves requests and switches role per request based on the validated JWT. The role switch is what enforces access, so do not collapse the two connections into one.
- **RLS is the only authorization layer.** There is no HTTP-level role check and no application-side role table. Access decisions are Postgres policies declared in `instancez.yaml`. The middleware validates the JWT and picks the Postgres role; everything else is RLS.

## Docs and examples

Documentation lives in `docs/site/`. When you change behavior, config, a CLI flag, or an endpoint, update the docs and examples in the same pull request.

## Contributor License Agreement

Before we can merge your first pull request, you need to agree to the [Contributor License Agreement](CLA.md). Include this line in your PR description or as a PR comment:

```
I have read and agree to the instancez CLA (CLA.md).
```

You only need to do this once; it covers every future contribution from the same GitHub account. A maintainer will add your GitHub username to the approved contributors list; comment `@cla-bot check` on the PR afterward to re-run the check.

## Pull request checklist

- The test loop above passes locally.
- Docs and examples are updated if behavior changed.
- The supabase-js compatibility test still passes for changes near the HTTP surface.
- The CLA is signed (first-time contributors only).
