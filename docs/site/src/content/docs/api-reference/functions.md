---
title: Functions API
description: /functions/v1/<name> endpoint reference. Full req and ctx property specification.
---

`functions:` declares JavaScript code functions served over HTTP. This is distinct from `rpc:`, which declares Postgres stored procedures.

## Endpoint

```
GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS /functions/v1/<name>
GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS /functions/v1/<name>/<subpath>
```

Any HTTP method is accepted. The subpath is passed through to `req.path` so a single function can route internally.

## Request headers

| Header | When required | Description |
|--------|---------------|-------------|
| `Authorization: Bearer <token>` | When `auth_required: true` | Identifies the caller. Absence returns 401. |
| `Content-Type` | Recommended | Determines how `req.body` is decoded. Defaults to `application/json` when omitted. |

## `req` properties

The first argument passed to the handler.

| Property | Type | Description |
|----------|------|-------------|
| `req.method` | `string` | HTTP method in uppercase (`"GET"`, `"POST"`, etc.) |
| `req.path` | `string` | Full request path including `/functions/v1/<name>/<subpath>` |
| `req.query` | `object` | Query parameters as `{ key: string }`. Multi-value params expose only the first value. |
| `req.headers` | `object` | Request headers as `{ lowercased-name: string }`. Multi-value headers expose only the first value. |
| `req.body` | `object \| string \| undefined` | Parsed from the raw request body. When `Content-Type` is `application/json`, the body is parsed as JSON. Otherwise it is a raw string. `undefined` when the body is empty. |

## `ctx` properties

The second argument passed to the handler.

| Property | Type | Description |
|----------|------|-------------|
| `ctx.supabase` | `SupabaseClient` | `@supabase/supabase-js` client that carries the **caller's JWT**. RLS applies as the calling user. Constructed lazily on first access. Throws if `@supabase/supabase-js` is not vendored. |
| `ctx.serviceClient` | `SupabaseClient` | `@supabase/supabase-js` client that carries an inz-minted **service_role JWT** (BYPASSRLS). Use for admin-level escalation. Falls back to the anon key when no service token is available. |
| `ctx.claims` | `object \| null` | JWT claims for the authenticated caller. `null` for anonymous requests. When present, contains at most four keys: `sub` (user UUID), `role` (`"authenticated"`), `email` (if present in token), and `jwt` (raw encoded token). Not a full JWT passthrough. |
| `ctx.env` | `object` | Per-function env values declared under `functions.<name>.env:` in `instancez.yaml`, resolved at invoke time. Keys are the declared names; values are literals or resolved `${INSTANCEZ_ENV_*}` references. |
| `ctx.log` | `object` | Structured logger. See [ctx.log methods](#ctxlog-methods) below. |
| `ctx.signal` | `AbortSignal` | Fires when the upstream connection closes before a response is sent (e.g. Go-side timeout). Honoring it is optional; the timeout still applies regardless. |

## ctx.log methods

```ts
ctx.log.info(msg: string, fields?: object): void
ctx.log.warn(msg: string, fields?: object): void
ctx.log.error(msg: string, fields?: object): void
ctx.log.debug(msg: string, fields?: object): void
```

Log lines are emitted as NDJSON and forwarded to the server's structured logger (`slog`). `fields` must be a plain object; non-object values are wrapped in `{ value: ... }`. Circular references are caught and a fallback message is emitted.

`console.log`, `console.warn`, `console.error`, and `console.debug` are patched to the same NDJSON pipeline.

## Handler return shape

The handler must return (or resolve) an object:

```ts
{
  status: number;          // HTTP status code (100–599). Defaults to 200.
  body: string | object;   // Strings are sent as-is; objects are JSON-serialized.
  headers?: object;        // Response headers. Defaults to { "content-type": "application/json" }.
}
```

If the handler throws, the response is `500` with `{ "message": "<error>" }`.

## HTTP status codes

| Status | Cause |
|--------|-------|
| `200` | Default when the handler returns without specifying a status. |
| `400` | Body could not be read, or context header could not be decoded. |
| `401` | `auth_required: true` and no valid JWT was provided. |
| `404` | No function registered under `<name>`. |
| `501` | Functions runtime not available (no `functions:` block or runtime failed to start). |
| `502` | Worker process failed (crashed or no healthy worker available). |
| `503` | Runtime saturated — in-flight cap reached. Retry with backoff. |
| `504` | Per-function timeout exceeded (default 30s, configurable via `functions.<name>.timeout`). |

Pass-through: the handler's returned `status` is used as-is for any code in the 100–599 range.

## Timeout behavior

Each function has a per-request timeout (default: `30s`; override with `functions.<name>.timeout` in `instancez.yaml`). When the timeout fires:

1. `ctx.signal` is aborted on the worker side.
2. The Go runtime returns `504` to the caller immediately.
3. The worker process remains alive and healthy; the late response is discarded.

The worker is **not** killed on timeout — only on a transport/connection failure, which triggers an automatic restart.

## Declaring a function

```yaml
functions:
  hello:
    runtime: node
    file: functions/hello.js
    auth_required: false
    timeout: 30s
    env:
      GREETING: Hello
      API_KEY: ${INSTANCEZ_ENV_MY_API_KEY}
```

```js
// functions/hello.js
export default async function handler(req, ctx) {
  const name = req.query.name ?? "world";
  return {
    status: 200,
    body: { message: `${ctx.env.GREETING}, ${name}!` },
  };
}
```
