---
title: Code Functions
description: JavaScript ESM HTTP handlers served at /functions/v1/<name>. Full access to ctx.supabase, secrets, and structured logging.
---

Code functions are JavaScript ESM handlers served at `/functions/v1/<name>`, callable from supabase-js via `supabase.functions.invoke()`.

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

## Calling a function

**curl:**

```sh
curl https://your-project.instancez.io/functions/v1/todos \
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
| `inz deploy` | Runs `npm ci` and bundles `functions/` into a tar archive recorded in `instancez.yaml`. | N/A |
| `inz serve` | Never runs npm. Consumes the pre-built bundle produced by `inz deploy`. | N/A |

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

- [Code Functions API reference](/instancez/api-reference/functions/) — full handler contract, return value shape, and error envelope
