# Code Functions

Code functions are JavaScript (ESM) HTTP handlers declared in `instancez.yaml` under `functions:` and served at `/functions/v1/<name>`. They run in Node.js worker processes managed by Instancez and are fully invocable from `@supabase/supabase-js` via `supabase.functions.invoke()`.

> **Note on naming:** The `functions:` YAML block now declares *code* functions. Postgres stored procedures (previously under `functions:`) were renamed to `rpc:` and are served at `/rest/v1/rpc/<name>`. See the [Functions reference](site/src/content/functions.mdx) for both.

## Authoring a handler

A function is an ESM module with a default export:

```js
// functions/hello.js
export default async function handler(req, ctx) {
  const name = req.body?.name ?? "world";
  ctx.log.info("hello called", { name });
  return {
    status: 200,
    body: { hello: name },
  };
}
```

### `req` — the incoming request

| Property | Type | Description |
|---|---|---|
| `req.method` | string | HTTP method (`"GET"`, `"POST"`, …) |
| `req.path` | string | URL path |
| `req.query` | object | Query parameters as `{ key: firstValue }` |
| `req.headers` | object | Request headers (lowercased keys) |
| `req.body` | object \| string \| undefined | Parsed for `application/json`; raw string otherwise |

### `ctx` — the injected context

| Property | Type | Description |
|---|---|---|
| `ctx.supabase` | SupabaseClient | Caller-RLS `@supabase/supabase-js` client. Reads/writes respect the caller's RLS policies. |
| `ctx.serviceClient` | SupabaseClient | Service-role client (`BYPASSRLS`). Use only when you explicitly need to bypass RLS. |
| `ctx.claims` | object \| null | Decoded JWT claims (`sub`, `role`, `email`, …) or `null` for anonymous callers. |
| `ctx.env` | object | Resolved secrets from the `env:` block (see below). |
| `ctx.log` | object | Structured logger: `.info(msg, fields?)`, `.warn`, `.error`, `.debug`. Logs are emitted as NDJSON and attributed to the request in Instancez's structured log output. |
| `ctx.signal` | AbortSignal | Fires when the per-request timeout elapses. Honoring it is optional. |

## Declaring a function in YAML

```yaml
functions:
  hello:
    runtime: node          # required; only "node" (JavaScript ESM) is supported in v1
    file: functions/hello.js   # path relative to instancez.yaml
    auth_required: false   # when true, instancez returns 401 for unauthenticated callers before invoking
    timeout: 30s           # per-request deadline (Go duration string; default 30s)
    env:
      API_KEY: "${INSTANCEZ_ENV_MY_API_KEY}"  # resolved from INSTANCEZ_ENV_* at startup
      REGION: "us-east-1"                # plain string literal
```

The `file` path is relative to the directory that contains `instancez.yaml`. Names may contain letters, digits, hyphens, and underscores; they must not start with a hyphen.

## Secrets (`env:` and `INSTANCEZ_ENV_*`)

Instancez runs function workers with a minimal, scrubbed environment — host secrets (AWS credentials, database URLs, etc.) are not visible inside the worker. Per-function secrets are injected through a controlled mechanism:

1. Set `INSTANCEZ_ENV_MY_API_KEY=your-secret` in `.env`, `.<mode>.env`, or the process environment.
2. Reference it in `instancez.yaml` under `env:` as `"${INSTANCEZ_ENV_MY_API_KEY}"`.
3. Access it inside the handler as `ctx.env.API_KEY`.

Secrets are resolved at startup. If a referenced `INSTANCEZ_ENV_*` variable is missing, Instancez fails early with a clear error rather than silently serving requests with empty values. Resolved secrets are passed to the worker per-request via an internal header — they are never written to the worker's process environment.

## Dependencies (npm)

All functions in a project share one `functions/package.json`. Add a dependency there, and it's available to every handler via `import`:

```json
// functions/package.json
{
  "type": "module",
  "dependencies": {
    "@supabase/supabase-js": "^2.107.0",
    "zod": "^3.23.8",
    "nanoid": "^5.0.7"
  }
}
```

`inz dev` runs `npm ci` for you on boot; `inz deploy` vendors `node_modules` into the bundle so `serve` never needs a registry. Dependencies must be importable as ESM and must not require native add-ons that aren't prebuilt for the deployment platform.

> **Required for `ctx.supabase` / `ctx.serviceClient`:** the injected clients are built with `@supabase/supabase-js`, which you must add to `functions/package.json`. If it's missing, calls that touch `ctx.supabase` or `ctx.serviceClient` fail at invoke time with `@supabase/supabase-js not vendored`. (Functions that don't use the clients — like a pure transform — don't need it.)

## Lifecycle

### `inz dev`

Functions are loaded directly from the local `functions/` directory. If a `package.json` is present, `npm ci` is run automatically to install dependencies before the runtime starts. Schema and migration changes in `instancez.yaml` are applied automatically on save, but changes to function code or the `functions:` config block require restarting `inz dev` to take effect.

### `inz deploy`

`deploy` vendors dependencies (`npm ci` in the `functions/` directory), packages everything — source + `node_modules` — into a tarball with a generated manifest, uploads it to the configured bundle destination (`--functions-bundle-dest s3://...`), and records the bundle pointer (`functions_bundle:`) in the configuration. The running deployment is not interrupted during the upload.

### `inz serve`

`serve` consumes the pre-built bundle produced by `deploy`. It never runs `npm ci` or accesses the local filesystem for functions. The bundle must already exist at the configured location before `serve` starts. This is the correct mode for production deployments (Lambda, containers, etc.).

## Calling a function

```bash
# Direct HTTP
curl -X POST http://localhost:8080/functions/v1/hello \
  -H "Authorization: Bearer <jwt>" \
  -H "Content-Type: application/json" \
  -d '{"name": "instancez"}'

# → {"hello":"instancez"}
```

With `@supabase/supabase-js`:

```js
const { data, error } = await supabase.functions.invoke('hello', {
  body: { name: 'instancez' },
})
```

## Runtime limits

| Limit | Default | Override |
|---|---|---|
| Per-request timeout | 30 s | `timeout:` per function |
| Worker pool size | `min(4, GOMAXPROCS)` | Internal; not user-configurable |
| Max concurrent invocations | `pool_size × 64` | Internal |

When the timeout is exceeded the caller receives HTTP 504. When the worker pool is saturated the caller receives HTTP 503.

## Examples

A complete, runnable example lives in [`docs/examples/gearstore/`](examples/gearstore/) — a product-catalog app you can start with `docker compose up`. Its [`instancez.yaml`](examples/gearstore/instancez.yaml) declares four functions; the source is under [`functions/`](examples/gearstore/functions/) with shared deps in [`functions/package.json`](examples/gearstore/functions/package.json). Each shows a different part of the surface.

### `hello` — the minimum

A default-exported handler returning `{ status, body }`. See [`functions/hello.js`](examples/gearstore/functions/hello.js).

### `echo` — the request surface

[`functions/echo.js`](examples/gearstore/functions/echo.js) reflects everything the handler receives — method, path, query, headers, body, `ctx.claims`, `ctx.env` — so you can see exactly what arrives:

```js
export default async function handler(req, ctx) {
  return { status: 200, body: {
    method: req.method,
    query: req.query,                    // ?q=hi&limit=5 → { q: "hi", limit: "5" } (strings)
    contentType: req.headers["content-type"] ?? null,
    body: req.body ?? null,              // parsed object for JSON; raw string otherwise
    caller: ctx.claims ? { sub: ctx.claims.sub, role: ctx.claims.role } : null,
  }};
}
```

```bash
curl -s 'http://localhost:8080/functions/v1/echo/anything?q=hi&limit=5' \
  -H 'content-type: application/json' -d '{"hello":"world"}' | jq
```

### `my-reviews` — `ctx.supabase` (RLS as the caller) + an npm dep

[`functions/my-reviews.js`](examples/gearstore/functions/my-reviews.js) lists and creates the signed-in user's reviews on the catalog's `reviews` table. It branches on method, validates the body with `zod`, and uses `ctx.supabase` — a client carrying the **caller's** JWT, so Postgres RLS authorizes every query as that user. It's declared `auth_required: true`, so anonymous callers get a 401 before the handler runs.

```js
import { z } from "zod";
const NewReview = z.object({ product_id: z.number().int(), author: z.string().min(1), rating: z.number().int().min(1).max(5), body: z.string().optional() });

export default async function handler(req, ctx) {
  if (req.method === "GET") {
    const { data, error } = await ctx.supabase
      .from("reviews").select("id,product_id,rating,body,created_at")
      .eq("user_id", ctx.claims.sub).limit(Number(req.query.limit ?? 20));
    return error ? { status: 400, body: { error: error.message } } : { status: 200, body: { reviews: data } };
  }
  if (req.method === "POST") {
    const parsed = NewReview.safeParse(req.body);
    if (!parsed.success) return { status: 400, body: { error: "invalid body", issues: parsed.error.issues } };
    // user_id is stamped from the verified JWT; the INSERT policy also enforces
    // user_id = auth.uid(), so it can't be spoofed.
    const { data, error } = await ctx.supabase
      .from("reviews").insert({ ...parsed.data, user_id: ctx.claims.sub }).select().single();
    return error ? { status: 400, body: { error: error.message } } : { status: 201, body: { review: data } };
  }
  return { status: 405, body: { error: "method not allowed" } };
}
```

### `webhook` — `ctx.serviceClient`, a secret, and a raw body

[`functions/webhook.js`](examples/gearstore/functions/webhook.js) imports a review from an external system. There's no user JWT (`auth_required: false`), so it authenticates the *request* with an HMAC signature over the raw body using a secret from `ctx.env`, then writes with `ctx.serviceClient`. That's meaningful here: the `reviews` INSERT policy is `user_id = auth.uid()`, so an anonymous client *can't* insert an imported review — `serviceClient` (BYPASSRLS) writes it with `user_id = null`.

```js
import { createHmac, timingSafeEqual } from "node:crypto";
import { nanoid } from "nanoid";

export default async function handler(req, ctx) {
  const raw = typeof req.body === "string" ? req.body : JSON.stringify(req.body ?? {});
  const expected = createHmac("sha256", ctx.env.WEBHOOK_SECRET).update(raw).digest("hex");
  const provided = req.headers["x-signature"] ?? "";
  if (provided.length !== expected.length ||
      !timingSafeEqual(Buffer.from(provided), Buffer.from(expected))) {
    return { status: 401, body: { error: "bad signature" } };
  }
  const payload = JSON.parse(raw);
  const { data, error } = await ctx.serviceClient
    .from("reviews")
    .insert({ product_id: payload.product_id, author: payload.author, rating: payload.rating, user_id: null })
    .select("id").single();
  return error ? { status: 400, body: { error: error.message } }
               : { status: 200, body: { received: true, importId: nanoid(), reviewId: data.id } };
}
```

---
*Audited 2026-06-13 — corrections applied to `docs/site/src/content/docs/build/functions.md` and `docs/site/src/content/docs/api-reference/functions.md`.*
