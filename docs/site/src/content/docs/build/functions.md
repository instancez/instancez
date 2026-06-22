---
title: Code Functions
description: JavaScript ESM HTTP handlers served at /functions/v1/<name>. Full access to ctx.supabase, secrets, and structured logging.
---

Code functions are JavaScript ESM handlers served at `/functions/v1/<name>`, callable from supabase-js via `supabase.functions.invoke()`.

## Requirements

Functions run in Node.js worker processes, so **Node.js must be installed and on `PATH`** wherever instancez serves or builds functions. With a `functions:` block declared, `inz dev` and `inz serve` require `node` at startup; `inz cloud deploy`/`inz bundle` require it when your functions have npm dependencies (a `functions/package.json`, which it vendors with `npm ci`). Each refuses to proceed with an actionable error if node is missing. Projects without a `functions:` block do not need Node.js.

The supported minimum is **Node.js 22**. Functions get an injected `@supabase/supabase-js` client (`ctx.supabase` / `ctx.serviceClient`), and that client opens a realtime connection that needs a native `WebSocket`, which Node ships from v22 on. The minimum is documented, not enforced, so an older version may appear to work until a function touches `ctx.supabase`.

When your functions have npm dependencies, **`inz cloud deploy`/`inz bundle` require a committed `package-lock.json`.** They vendor dependencies with `npm ci` (reproducible, never `npm install`), which fails without a lockfile. Run `npm install` in `functions/` once to generate it and commit the result. (`inz dev` is more lenient: it falls back to `npm install` to create the lockfile on first run.)

instancez also verifies at startup that **every declared function's source file exists** on disk (the `file:` path under each entry). A missing file fails fast with a clear error instead of surfacing later when the function is first called.

## A minimal handler

```js
// functions/hello.js
export default async function handler(req, ctx) {
  return { status: 200, body: { message: "hello" } };
}
```

A handler receives two arguments — `req` (the incoming request) and `ctx` (runtime context) — and returns an object with `status`, `body`, and optionally `headers`.

## Request object (req)

| Property | Type | Description |
|----------|------|-------------|
| `method` | `string` | HTTP method (`"GET"`, `"POST"`, etc.) |
| `path` | `string` | Request path including the function prefix (e.g. `/functions/v1/todos`) |
| `query` | `object` | URL query parameters as a flat string-keyed object (first value per key) |
| `headers` | `object` | Lowercased request headers (first value per key) |
| `body` | `any` | Parsed request body. JSON when `content-type: application/json`, raw string otherwise. `undefined` when body is empty. |

## Context object (ctx)

| Property | Type | Description |
|----------|------|-------------|
| `ctx.supabase` | `SupabaseClient` | A `@supabase/supabase-js` client carrying the **caller's JWT**. RLS applies as the calling user. Lazily constructed on first access. Throws if `@supabase/supabase-js` is not vendored. |
| `ctx.serviceClient` | `SupabaseClient` | A `@supabase/supabase-js` client carrying a short-lived `service_role` JWT (bypasses RLS). Use for explicit privilege escalation. |
| `ctx.claims` | `object \| null` | Claims extracted from the caller's JWT. `null` for anonymous callers. Contains at most four keys: `sub` (user ID string), `role` (wire role string), `email` (if present in the JWT), and `jwt` (raw token string). Custom JWT fields beyond these are not available. |
| `ctx.env` | `object` | Secrets declared in the function's `env:` YAML block, resolved from `INSTANCEZ_ENV_*` variables. |
| `ctx.log` | `object` | Structured logger with methods `debug`, `info`, `warn`, `error`. Each takes `(message, fields?)`. Log lines appear in `inz dev` output. |
| `ctx.signal` | `AbortSignal` | Aborted when the caller disconnects or the per-request timeout fires. Honoring it is optional — the server enforces the timeout regardless. |

`console.log`, `console.warn`, `console.error`, and related methods are patched to emit structured log lines. Prefer `ctx.log` for structured field support.

## Declaring in YAML

Functions are declared under the top-level `functions:` key in `instancez.yaml`:

```yaml
functions:
  todos:
    runtime: node         # required; "node" is the only supported value
    file: functions/todos.js   # path relative to the config root
    auth_required: true   # when true, unauthenticated callers receive 401 before the handler runs
    timeout: 30s          # per-request deadline; defaults to 30s
    env:                  # secrets injected as ctx.env
      STRIPE_KEY: ${INSTANCEZ_ENV_STRIPE_KEY}
      FIXED_VALUE: "literal"
```

| Key | Type | Description |
|-----|------|-------------|
| `runtime` | `string` | Runtime identifier. Only `"node"` is supported. |
| `file` | `string` | Path to the handler file, relative to the config root. |
| `auth_required` | `bool` | If `true`, instancez returns `401` for anonymous requests before invoking the handler. Default `false`. |
| `timeout` | `string` | Go duration string (e.g. `"30s"`, `"5s"`). Defaults to `30s`. Exceeding the timeout returns `504`. |
| `env` | `map[string]string` | Secrets available as `ctx.env`. Values are either plain literals or `${INSTANCEZ_ENV_*}` references. |

## Secrets

Set secrets as environment variables with the `INSTANCEZ_ENV_` prefix:

```sh
# .env or .development.env (gitignored)
INSTANCEZ_ENV_STRIPE_KEY=sk_test_...
```

Reference them in YAML:

```yaml
functions:
  charge:
    runtime: node
    file: functions/charge.js
    env:
      STRIPE_KEY: ${INSTANCEZ_ENV_STRIPE_KEY}
```

Access in the handler:

```js
const stripe = new Stripe(ctx.env.STRIPE_KEY);
```

Secrets are resolved from three sources in ascending precedence order:
1. `.env` (base file)
2. `.<mode>.env` (e.g. `.development.env`, `.production.env`)
3. Process environment variables (`INSTANCEZ_ENV_*`)

Only keys with the `INSTANCEZ_ENV_` prefix are passed to functions. Other environment variables in those files are ignored.

Function workers run with a scrubbed environment, so host secrets like AWS credentials and database URLs are not visible inside a handler. The values you declare under `env:` are the only secrets a worker sees. instancez injects them per request over an internal channel and never writes them to the worker's process environment.

## npm dependencies

Functions run from the `functions/` subdirectory of your project. Place a `package.json` there to declare dependencies:

```json
{
  "name": "functions",
  "private": true,
  "type": "module",
  "dependencies": {
    "@supabase/supabase-js": "^2.107.0"
  }
}
```

`@supabase/supabase-js` is required if any function uses `ctx.supabase` or `ctx.serviceClient`. The worker loads it lazily — functions that never access those properties work without it.

Dependencies must import as ESM, and they must not rely on native add-ons unless those are prebuilt for the platform you deploy to.

## Calling a function

**curl:**

```sh
curl https://your-project.instancez.ai/functions/v1/todos \
  -H "Authorization: Bearer <user-jwt>"
```

**supabase-js:**

```js
const { data, error } = await supabase.functions.invoke("todos", {
  body: { title: "Buy milk" },
});
```

## Lifecycle

| Command | npm | Hot reload |
|---------|-----|------------|
| `inz dev` | Runs `npm ci` on startup. Falls back to `npm install` when no lockfile exists yet (first run). Restart required only when adding or removing npm dependencies. | JS code changes and `functions:` YAML changes are picked up automatically without a restart. |
| `inz cloud deploy` | Runs `npm ci` (requires a committed `package-lock.json`) and bundles `functions/` into a tar archive recorded in `instancez.yaml`. | N/A |
| `inz serve` | Never runs npm. Consumes the pre-built bundle produced by `inz cloud deploy`. | N/A |

## Runtime limits

| Setting | Value |
|---------|-------|
| Default timeout | `30s` (configurable per-function via `timeout:`) |
| Worker pool size | `min(4, GOMAXPROCS)` Node processes |
| Max concurrent requests | `pool_size × 64` |

**Error codes:**

| Code | Meaning |
|------|---------|
| `401` | `auth_required: true` and no valid JWT provided |
| `504` | Handler exceeded the `timeout` |
| `503` | All in-flight slots are occupied (runtime saturated) |
| `502` | Worker process died or no healthy worker available |
| `500` | Handler threw an unhandled exception |

## What's next

