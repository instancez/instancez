# Custom Code Functions — Examples & Flows

Companion to `2026-06-06-custom-code-functions-design.md`. Concrete code +
end-to-end flows for every scenario, to validate the design before planning.

## 1. What a project looks like

```
my-app/
  ultrabase.yaml
  functions/
    create-order.js
    stripe-webhook.js
    package.json          # one shared manifest for all node functions
    package-lock.json
```

`ultrabase.yaml` (note: `rpc:` is the renamed Postgres-function block;
`functions:` is the new code block):

```yaml
functions:
  create-order:
    runtime: node
    file: functions/create-order.js
    auth_required: true            # caller must present a valid JWT
    timeout: 15s

  stripe-webhook:
    runtime: node
    file: functions/stripe-webhook.js
    auth_required: false           # Stripe calls it; verified via signing secret
    env:
      STRIPE_WEBHOOK_SECRET: ${ULTRA_ENV_STRIPE_WEBHOOK_SECRET}   # ref names the full ULTRA_ENV_ key (no stripping)
```

## 2. What a function looks like

**`create-order.js` — runs as the caller, RLS enforced:**

```js
export default async function handler(req, ctx) {
  if (req.method !== "POST") {
    return { status: 405, body: { error: "method not allowed" } };
  }
  const { items } = req.body;                       // JSON-parsed (Content-Type: application/json)

  // ctx.supabase carries the CALLER's JWT -> the INSERT is subject to RLS,
  // exactly as if the browser had called /rest/v1/orders directly.
  const { data: order, error } = await ctx.supabase
    .from("orders")
    .insert({ user_id: ctx.claims.sub, status: "pending", items })
    .select()
    .single();

  if (error) return { status: 400, body: { error: error.message } };
  ctx.log.info("order created", { order_id: order.id });
  return { status: 201, headers: { "content-type": "application/json" }, body: { order } };
}
```

**`stripe-webhook.js` — uses a secret, escalates with `serviceClient`:**

```js
import Stripe from "stripe";                          // a real npm dep, vendored at deploy

export default async function handler(req, ctx) {
  // Raw body is available because Content-Type isn't JSON — needed for signature check.
  const event = Stripe.webhooks.constructEvent(
    req.body, req.headers["stripe-signature"], ctx.env.STRIPE_WEBHOOK_SECRET,
  );

  if (event.type === "checkout.session.completed") {
    // Privileged write: mark the order paid regardless of who's calling.
    // ctx.serviceClient uses a short-lived, ultra-minted service_role JWT.
    await ctx.serviceClient
      .from("orders")
      .update({ status: "paid" })
      .eq("id", event.data.object.metadata.order_id);
  }
  return { status: 200, body: { received: true } };
}
```

Client side, unchanged supabase-js:

```js
const { data, error } = await supabase.functions.invoke("create-order", {
  body: { items: [{ sku: "A1", qty: 2 }] },
});
// 2xx -> data = parsed body; non-2xx -> error is a FunctionsHttpError
```

## 3. Request flow (the data path) — same in every environment

Everything is plain HTTP request/response — three hops, one mental model.

```
browser / supabase-js
  │  POST /functions/v1/create-order   (apikey + Authorization: Bearer <caller JWT>)
  ▼
ultra HTTP server  ── middleware: validate apikey + JWT, enforce auth_required
  │  HTTP request to the worker (over its unix socket), original method+body, plus:
  │    X-Ultra-Fn: create-order
  │    X-Ultra-Context: base64({ requestId, claims, env,
  │                              dataPlane:{ url:"http://127.0.0.1:8080",
  │                                          anonKey, callerToken, serviceToken } })
  ▼  (HTTP over a Unix domain socket — no framing, no multiplexing bookkeeping)
node worker (HTTP server)  ── reads+strips X-Ultra-*, builds ctx
  │                            (ctx.supabase = caller JWT, ctx.serviceClient = minted JWT)
  │                            runs handler(req, ctx)
  │
  │  ctx.supabase.from("orders").insert(...)
  ▼
http://127.0.0.1:8080/rest/v1/orders   ← LOOPBACK into the SAME ultra process
  │  served by ultra's normal HTTP server on its own goroutines
  │  SET LOCAL ROLE authenticated  → RLS policies apply as the caller
  ▼
Postgres
  ▲  rows
  │
  └─ handler returns its HTTP response VERBATIM → ultra relays it → client
```

The loopback is served by ultra's own listening socket directly — it does **not**
re-enter through Lambda's invocation model (see §7). That's why one process can
both forward to the worker and answer the worker's callback.

## 3b. The ultra↔worker interface (plain HTTP, no custom protocol)

There is **no bespoke framing or multiplexing**. Two HTTP relationships:

**Invoke — ultra → worker (HTTP over a Unix domain socket).** The worker is a
tiny HTTP server (Node's built-in `http`, no framework) listening on a socket
file. ultra forwards the caller's request to it with two extra headers:

```
POST /invoke            (worker socket: /tmp/ultra-fn-<n>.sock)
X-Ultra-Fn:      create-order
X-Ultra-Context: <base64 JSON>
                 { requestId, claims|null, env:{…},
                   dataPlane:{ url:"http://127.0.0.1:8080", anonKey,
                               callerToken:"<caller JWT|null>",   // → ctx.supabase
                               serviceToken:"<minted service_role JWT>" }, // → ctx.serviceClient
                   deadlineMs: 15000 }
<original request body, verbatim — binary native, no base64>

→ worker returns the handler's HTTP response VERBATIM (status/headers/body)
```

- Concurrency is HTTP-native: Node's event loop runs many in-flight requests per
  worker; ultra spreads load across the worker pool. No request-id transport
  matching — HTTP pairs response↔request.
- `requestId` is a **log-correlation id only** (§3c), not transport plumbing.
- Per-request creds ride in the **header, not the child env** — keeps the scrub
  (§8) literal.

**Data — worker → ultra (loopback HTTP).** `ctx.supabase.from(...)` →
`http://127.0.0.1:8080/rest/v1/...` with the relevant JWT → existing PostgREST +
`SET LOCAL ROLE` + RLS path. Same request/response shape as everything else; no
second data path. "Passing data to ultrabase" from a function = calling its REST
API this way.

## 3c. Log capture flow

```
handler runs inside:  als.run({ requestId, fn }, () => handler(req, ctx))
  │                    (AsyncLocalStorage — survives awaits, so concurrent
  │                     requests on one worker never cross logs)
  │
  ├─ ctx.log.info("order created", { order_id })   ─┐
  └─ console.log("anything")  (patched → same path) ─┤  each becomes one NDJSON line:
                                                      ▼   { ts, level, requestId, fn, msg, fields }
                                            worker STDOUT (newline-delimited JSON)
                                                      │
                                                      ▼
ultra reads the worker's stdout pipe → parses each line → forwards to slog
   (carries requestId, so it correlates with ultra's own request logs)

stderr / non-JSON (uncaught stack, noisy lib) → forwarded best-effort,
   attributed to the WORKER (not a request); a crash stack also marks it unhealthy.
```

- **Live, not buffered** — logs stream during execution, and survive a crash up
  to the crash point.
- **Guards:** oversized lines truncated; optional per-request log-rate cap;
  resolved secret values (§8) redacted before emit.

## 4. Local dev flow (`ultra dev`, FileSource)

```
$ cd my-app && ultra dev
```

```
1. ultra loads ultrabase.yaml via FileSource (local file).
2. Sees functions/package.json changed since last run → runs `npm ci` in functions/.
3. Launches the node worker pool against ./functions (source + node_modules).
   Each worker imports create-order.js / stripe-webhook.js once and starts its
   HTTP server on a unix socket.
4. Serves on http://localhost:8080. /functions/v1/* is live.
5. fsnotify watches functions/*.js:
     - edit create-order.js  → recycle pool (workers re-import) → next request hits new code
     - edit package.json      → re-run `npm ci` → recycle pool
```

No bundle, no S3 — you edit a `.js` file and the next request runs it. No build
step.

## 5. Deploy flow (builds + ships the bundle — no separate publish command)

Bundling is part of **`ultra deploy`** (which also pushes config, previews the
migration, and promotes). This is the **only** step with package-registry access:

```
$ ultra deploy
```

```
1. Validate config (functions:/rpc: shapes, runtimes, file paths exist)
2. npm ci against functions/package.json            (resolve real deps)
3. Pack a versioned bundle tarball:
       bundle/
         functions/create-order.js
         functions/stripe-webhook.js
         node_modules/...            (vendored — the real ecosystem, frozen)
         manifest.json               (function→file map, runtime, checksums)
4. Upload config + bundle to object storage:
       s3://<bucket>/<app>/functions-bundle-<version>.tar.zst
5. Record the bundle pointer (version/ETag) IN the deployed config — this is what
   `serve` reads. The developer never hand-builds or hand-references the tarball.
```

If `npm ci` fails or a dep is broken, **deploy fails — the running production
deployment is untouched.** Bad deps never reach the runtime.

`serve` **never builds** — it always consumes a pre-built bundle, self-hosted
included. A self-hoster runs `ultra deploy` (targeting their own object storage)
to produce + reference the bundle first, then points `serve` at the config. Only
`ultra dev` builds on the fly; `serve` start-up needs no npm/toolchain.

## 6. Production deployment — long-running container (EKS / k8s / VM)

```
$ ultra serve            # config spec = s3://<bucket>/<app>/ultrabase.yaml
```

```
Boot:
  1. S3Source.Load() → config (includes the functions-bundle pointer/version)
  2. Download functions-bundle-<version>.tar.zst → extract to a writable dir
     (temp path → atomic swap; never serve a half-written bundle)
  3. migrate → seed → start HTTP server
  4. Launch node worker pool against the extracted bundle dir (each = an HTTP
     server on a unix socket)
  5. Serve /functions/v1/*  (ultra spreads concurrent calls across the pool)

Hot update (no rebuild, no redeploy):
  6. Source.Watch polls S3; sees a NEW bundle version
  7. Download + extract the new bundle to a fresh dir
  8. Start a new worker pool on it; drain the old pool (finish in-flight), then swap
  9. Old dir removed. Zero-downtime function update.
```

Pool size = N (tunable) → real concurrency across many simultaneous callers.

## 7. Serverless deployment — AWS Lambda (container image + Web Adapter)

The base image already ships the AWS Lambda Web Adapter and `ultra serve`
(`Dockerfile.lambda`). Node is baked into that fixed image.

```
COLD START (new execution environment):
  1. Lambda starts the container → `ultra serve` boots, binds 127.0.0.1:8080
  2. S3Source.Load() config + download/extract functions bundle to /tmp
     (the only writable path on Lambda — fine, bundle is read-only at runtime)
  3. Launch a small worker pool (Lambda runs ~1 concurrent req per env, so a
     small pool suffices; extra envs scale horizontally via Lambda concurrency)

PER INVOCATION:
  4. Web Adapter receives the Lambda event → forwards as real HTTP to :8080
  5. ultra routes /functions/v1/* → HTTP request to a worker (unix socket) → handler
  6. ctx.supabase → http://127.0.0.1:8080/rest/v1/... LOOPBACK
        ↳ served by ultra's in-process HTTP server directly, NOT a nested Lambda
          invocation. The outer Lambda is still blocked waiting for ultra to
          return; ultra answers its own loopback on a separate goroutine. ✔
  7. Handler's HTTP response → ultra → Web Adapter → Lambda response

WARM INVOCATIONS: reuse the booted pool + extracted /tmp bundle (no re-download).
NEW BUNDLE VERSION: picked up on the next cold start (or by the same Watch poll
  if the env lives long enough).
```

**Honest cold-start cost:** a cold env pays config load + bundle download +
extract + Node worker spawn before the first request. Keep the bundle lean and
co-locate the S3 bucket in-region. This is the price of "ship code as data, no
image rebuild." (Baking the bundle into the image would cut cold start but
reintroduce the rebuild you explicitly rejected.)

## 8. Isolation & secrets flow (every environment)

```
Worker spawn:
  ultra spawns `node worker.js` with an EXPLICITLY constructed env, NOT inherited:
     present:  PATH, NODE_ENV, the function's `env:` allowlist values
     absent:   AWS_ACCESS_KEY_ID, AWS_SECRET_*, AWS_SESSION_TOKEN,
               AWS_WEB_IDENTITY_TOKEN_FILE, AWS_ROLE_ARN, ULTRABASE_* secrets, ...
  → the child's AWS SDK cannot authenticate on Lambda OR EKS/IRSA. ✔
  → bonus: the ULTRA_ENV_ values live in an in-memory map (NOT os.Setenv), so
    they are never in the inheritable environment to begin with.

env + secret resolution (parent does it; child never sees the source):
  - `env: { STRIPE_WEBHOOK_SECRET: ${ULTRA_ENV_STRIPE_WEBHOOK_SECRET} }`
        left side  = ctx.env key the function reads
        right side = ref into the ULTRA_ENV_ namespace (full key, no stripping)
  - the ULTRA_ENV_ namespace is an IN-MEMORY MAP built at startup (not os.Setenv),
    precedence: ULTRA_ENV_* process env > .<mode>.env > .env  (exact ULTRA_ENV_ prefix)
  - resolve-from-map-ONLY: no os.Getenv fallback. ULTRA_ENV_ is disjoint from
    ULTRABASE_*, so ${ULTRA_ENV_...} can never name AWS_*/ULTRABASE_ADMIN_KEY/DSNs;
    a missing ref FAILS AT BOOT (fail-early)
  - the resolved value is injected into X-Ultra-Context / ctx.env for that fn only
  - worker reads ctx.env.STRIPE_WEBHOOK_SECRET; cannot enumerate others;
    values redacted from logs/openapi; only the raw ${ref} is written back to S3

Residual (documented, not solved by ultra):
  - the node instance role via IMDS 169.254.169.254 is an instancez
    network-policy concern, not env-scrubbable.

Trust reality:
  - shim + user code share one V8 isolate → any function CAN reach
    ctx.serviceClient. RLS-as-caller is a convenience default, not a wall.
  - The real containment is one-instance-per-tenant: a misbehaving function is
    confined to its own tenant's deployment + least-privilege IAM role.
```

## 9. Failure scenarios

| Scenario | Behavior |
|---|---|
| Handler exceeds `timeout` | ultra cancels the HTTP call (connection closes → handler `AbortSignal`), returns **504**; other in-flight requests untouched |
| Handler throws | shim catches → **500** with error envelope; worker stays up |
| Worker hangs / crashes | connection error → **502**; ultra pulls it from the pool until `GET /healthz` passes; server survives |
| All workers at in-flight cap | bounded queue, then **503** |
| New bundle mid-traffic | new pool started, old pool drained, atomic swap → zero-downtime |
| `npm ci` fails at deploy | **deploy fails**; running deployment untouched; bad deps never ship |
| Bundle download fails at boot | boot fails loudly (same as a bad config today) |
| Function calls AWS | SDK finds no creds (scrubbed env) → auth error, by design |

## 10. Scenario matrix (where code & deps come from)

| Scenario | Config source | Function code | node_modules | Reload |
|---|---|---|---|---|
| Local dev | local file | `functions/` dir | `npm ci` at boot | fsnotify hot-reload |
| Deploy | — | packed into bundle | vendored into bundle | n/a (produces artifact) |
| Prod container (k8s) | S3 | extracted bundle | extracted bundle | Watch poll → drain+swap |
| Serverless (Lambda) | S3 | extracted to /tmp | extracted to /tmp | next cold start / Watch |

Across all of them the **base image is identical and never rebuilt per deploy**;
only the bundle (data) differs.
```