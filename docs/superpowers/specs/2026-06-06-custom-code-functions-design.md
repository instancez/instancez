# Design: Custom Code Functions (JavaScript HTTP handlers, Python fast-follow)

**Date:** 2026-06-06
**Status:** Approved design, pending implementation plan

## 1. Overview

Ultrabase today exposes user-defined logic only as Postgres stored procedures
(the `functions:` YAML block, served at `/rest/v1/rpc/<name>` for supabase-js
`.rpc()`). This design adds a second, distinct capability: **functions written
in real code** — HTTP handlers authored in **JavaScript** — served at
`/functions/v1/<name>`, wire-compatible with `supabase.functions.invoke()`.

Settled decisions (rationale in §12):

- **Invocation model:** HTTP request → response handlers (Supabase Edge
  Functions style), one path per function.
- **Execution:** Node is **baked into a fixed base image built once** by the
  ultrabase project. Each function runs in a **worker process that is itself a
  tiny HTTP server**; ultra invokes it with an ordinary HTTP request over a Unix
  domain socket. "Single container image" and "no image rebuild" coexist: the
  base image is a fixed artifact; **function code and dependencies ship as
  data**, not as image layers.
- **Shipping (no image rebuild):** function code and a **vendored
  `node_modules` bundle** are produced by **`ultra deploy`** and uploaded to
  object storage (S3); ultra loads and **hot-reloads** them via the existing
  `config.Source`/`Watch` mechanism. Drop/update a function → ultra reloads. No
  rebuild, no runtime package-registry access.
- **v1 language:** JavaScript on Node. Python is a deliberate fast-follow that
  reuses all the plumbing (§10).
- **Tenancy (instancez):** **one ultrabase instance per tenant.** Tenant↔tenant
  isolation is therefore the deployment boundary (separate Lambda/pod), not
  ultrabase's job.
- **Isolation:** functions are trusted-ish but must be **walled off from
  ultrabase's own ambient credentials/environment**. The worker child runs with
  a **scrubbed environment by default**; access to env vars/secrets is granted
  **explicitly** via config allowlist (§9).
- **Data access:** loopback to ultrabase's own REST API with the caller's auth
  forwarded; **RLS enforced as the caller**. A pre-authed client is injected so
  the user writes zero client boilerplate (§7).

This is explicitly a v1. §11 lists what is intentionally out of scope.

## 2. Terminology and the `functions:` → `rpc:` rename

To free the name `functions:` for code functions, the existing Postgres-RPC
block is renamed:

- YAML key `functions:` → `rpc:`
- Go field `domain.Config.Functions` → `domain.Config.RPC` (the
  `Function`/`FuncArg`/`FuncReturn` types keep their names)
- The wire route is **unchanged** — Postgres functions stay at
  `/rest/v1/rpc/<name>`. The rename touches only the config key and the Go
  field, not any HTTP surface.

No backward compatibility. `ultrabase validate` must **reject a top-level
`functions:` block with the old RPC shape** (contains `body:`/`language:`/
`returns:`) with a clear error: *"`functions:` now defines code functions; move
Postgres functions to `rpc:`."* Every YAML/docs example that uses `functions:`
for RPC must be updated to `rpc:` (not just `ultrabase.yaml`).

The new code-function block uses `functions:`.

## 3. Declaration

```yaml
functions:
  send-welcome:
    runtime: node               # node (v1). python reserved for fast-follow.
    file: functions/send-welcome.js
    auth_required: true         # reject callers without a valid JWT before invoking
    timeout: 30s                # default 30s; ultra cancels the request on breach
    env:                        # explicit map: name -> literal or ${REF} (see §9)
      STRIPE_API_BASE: "https://api.stripe.com"
      STRIPE_API_KEY: ${ULTRA_ENV_STRIPE_API_KEY}   # ref names the full ULTRA_ENV_ key (no stripping)
    # path defaults to /functions/v1/send-welcome
```

- `runtime` enum: `node` only in v1; the field exists so `python` is additive.
- `file` is a path (relative to the config root) to a single JS source file.
- `name` (the map key) must be a valid URL path segment, unique among
  functions. Function paths live under `/functions/v1/`, a separate namespace
  from `/rest/v1/rpc/`.
- `env:` is a map of name → value; a value is a literal or a `${REF}`
  interpolation. It is the single opt-in mechanism for configuration —
  absent/empty means the worker sees no configuration. `${REF}` uses the general
  config-interpolation mechanism (§9): it resolves against the **`ULTRA_ENV_`
  namespace** (an in-memory map), never ultra's full process environment.

## 4. Handler contract

A function is a JavaScript (ESM) module with a default-exported async handler:

```js
// functions/send-welcome.js
export default async function handler(req, ctx) {
  const { name } = req.body;
  await ctx.supabase.from("audit").insert({ event: "welcome", name });
  ctx.log.info("welcomed", { name });
  return { status: 200, headers: { "content-type": "application/json" }, body: { ok: true } };
}
```

- `req`: `{ method, path, query, headers, body }`. `body` is JSON-parsed when the
  request `Content-Type` is JSON; otherwise the raw string/bytes are available.
- `ctx.supabase`: a supabase-js client **pre-pointed at the internal loopback
  listener** (§7) with the caller's `Authorization`/`apikey` forwarded. Queries
  run as the caller → RLS applies exactly as for any external client.
- `ctx.serviceClient`: a supabase-js client built with the service-role token for
  **explicit** escalation (BYPASSRLS) — a deliberate choice in code, mirroring
  Supabase.
- `ctx.claims`: the validated JWT claims (or null for anon).
- `ctx.env`: the resolved `env:` map (§3) — the **only** way function code reads
  configuration; `process.env` is otherwise empty. `${REF}` values are resolved
  to plaintext by the **parent** ultra process (§9), never by the worker, and
  are redacted from logs.
- `ctx.log`: a structured, request-scoped logger (§6).
- **Return:** a plain object `{ status, headers, body }` or a Web `Response`.
  `body` objects are JSON-serialized; the handler controls `Content-Type`
  (load-bearing for supabase-js parsing — §8e).

JavaScript runs directly on Node (ESM). No build/transpile step. (TypeScript is
deferred — see §11.)

## 5. Runtime architecture — everything is plain HTTP request/response

A new `FunctionRuntime` component in `internal/app`, owned by the engine
lifecycle (started after migrate/seed, alongside the HTTP server; gracefully
shut down on stop). It depends only on `domain` interfaces, per the hexagonal
layout.

### The worker is a tiny HTTP server

- Per language, a **pool of N long-lived worker processes** launched against the
  **extracted bundle directory** (§8) — the function source + vendored
  `node_modules`.
- The worker is an **ultra-shipped shim** (`worker.js`, `go:embed`'d, written to
  the bundle/temp dir at boot). It uses **Node's built-in `http` module — no
  Express/Fastify, no framework, no extra dependency.** It imports every
  function file **once** at startup, then `http.createServer(...).listen(<unix
  socket>)`. A socket *file*, not a TCP port: nothing to allocate, unreachable
  off-box.
- The worker child is spawned with a **scrubbed environment** (§9).

### Invocation: one ordinary HTTP request per call

ultra invokes a function with a normal HTTP request to the worker's socket. No
bespoke framing, no request-id transport bookkeeping — the HTTP layer matches
responses to requests for us.

- **ultra → worker:** the caller's request is forwarded (method + body
  preserved) with an added **`X-Ultra-Context`** header — base64 JSON carrying
  `{ requestId, claims, env, dataPlane:{ url, anonKey, callerToken, serviceToken
  }, deadlineMs }` (and the original path/query so `req` reconstructs faithfully)
  — and an `X-Ultra-Fn` header naming the function. The shim reads and strips
  these, builds `ctx`, and calls the handler.
- **worker → ultra:** the function's HTTP response **verbatim** (status,
  headers, body). ultra relays it to the client.
- **Binary bodies are native in both directions** (no base64 envelope — a strict
  improvement over a custom frame).
- `requestId` is a **log-correlation/tracing id only** (§6); it is *not* used to
  multiplex the transport. HTTP already pairs each response with its request.

### Concurrency, timeouts, lifecycle — all from HTTP semantics

- **Concurrency:** Go's `http.Client` (with a Unix-socket dialer) and Node's
  event-loop HTTP server handle many concurrent in-flight requests per worker
  natively (handlers are I/O-bound — they loop back to the REST API). Multiple
  CPU cores are used by spreading requests across the **worker pool**
  (round-robin); ultra caps total in-flight concurrency.
- **Timeouts:** ultra sets a per-request deadline (`context.WithTimeout`); on
  breach it cancels → the connection closes → the shim aborts the handler via
  `AbortSignal`. Returns **504**.
- **Crash isolation:** a dead worker yields a connection error → ultra returns
  **502** and removes the worker from the pool until `GET /healthz` passes;
  restart is just relaunching the process.
- **Startup / bad code or deps:** the worker begins listening only after it has
  imported the function files successfully; ultra waits for `/healthz`. If
  imports fail, the socket never comes up → a **clear boot error**.
- **Saturation:** a bounded in-flight cap / queue, then **503**.

## 6. Log capture

Functions emit logs two ways — explicit `ctx.log.{debug,info,warn,error}(msg,
fields)` and plain `console.log/info/warn/error` — and both must be captured and
attributed to the request that produced them, **even though one worker handles
many requests concurrently** (so raw stdout lines from different requests would
otherwise interleave). The mechanism:

- **Per-request attribution via `AsyncLocalStorage`.** The shim runs each
  handler inside `als.run({ requestId, fn }, () => handler(req, ctx))`. Node's
  `AsyncLocalStorage` propagates that store across every `await`/callback in the
  request, so any log call — including a deeply nested one — can recover *which*
  request it belongs to despite concurrency.
- **`console.*` is patched** in the shim to route through the same path as
  `ctx.log`, stamping each line with the current `{ requestId, fn }` from the
  store. So users who just `console.log` still get attributed, structured logs;
  they don't have to learn `ctx.log`.
- **Transport: NDJSON on the worker's stdout.** Each captured log is one
  newline-delimited JSON object `{ ts, level, requestId, fn, msg, fields }`.
  ultra reads the worker's stdout pipe, parses each line, and forwards it into
  its own `slog` with those fields — so function logs interleave correctly with
  ultra's own structured logs and carry the correlation id. Lines that aren't
  our JSON (a library writing raw text, or **stderr**, including an uncaught
  stack trace) are forwarded best-effort at info/warn, attributed to the
  **worker** (not a request).
- **Live, not buffered.** Logs stream as they are produced (a long handler's
  logs appear mid-execution), rather than being returned only with the response.
  A worker crash therefore still yields the logs emitted up to the crash, plus
  the stderr stack, after which the worker is marked unhealthy (§5).
- **Correlation across the data hop (optional).** ultra may propagate the
  `requestId` as an `X-Request-Id` on the loopback data calls (`ctx.supabase`),
  so the REST-side request logs share the function's correlation id end to end.
- **Guards.** Oversized log lines are truncated; an optional per-request log-rate
  cap protects against a runaway handler flooding the log pipe. Interpolated
  secret values (§9) are redacted before any line is emitted.

(We considered returning logs embedded in the invocation HTTP response for
perfect ordering, but chose stdout NDJSON + `AsyncLocalStorage` so logs stream
live and survive a crash; attribution is preserved either way.)

## 7. Auth, identity, and the loopback target

- The existing JWT middleware validates the token and enforces `auth_required`.
- The caller's `Authorization`/`apikey` headers are forwarded into the worker;
  `ctx.supabase` uses them.
- **The worker's own data-plane credentials are minted per request and passed in
  the `X-Ultra-Context` header, never via the child env and never as a
  long-lived key.** For `ctx.serviceClient`, ultra mints a **short-lived
  `service_role` JWT** per request; the long-lived service-role signing key stays
  inside the Go process. The shim builds `ctx.supabase` (caller's token) and
  `ctx.serviceClient` (minted token) from these values. This is the line §9
  draws: ultra's *platform/AWS* environment is scrubbed; the tenant's
  *data-plane* access is injected — different trust classes, handled differently.
- **Loopback target:** ultra always binds a real TCP listener via
  `ListenAndServe` (`internal/adapter/http/server.go`) — including on Lambda,
  where the **AWS Lambda Web Adapter** (`Dockerfile.lambda`) forwards
  invocations to that port. The injected client's base URL points at the
  **internal `127.0.0.1:<port>`** listener, never the public URL. Robust on both
  k8s and Lambda. (The handler's loopback HTTP to `127.0.0.1:8080` is served by
  ultra's own listener on a separate goroutine — *not* a nested Lambda
  invocation — so a single process both forwards to the worker and answers the
  worker's callback.)
- No new authorization layer. RLS via the existing role-switch path is the only
  authz, per the project non-negotiable.

## 8. Shipping & dependencies (no image rebuild)

The base image (ultrabase binary + Node) is built once and pulled as a fixed
artifact. Everything function-specific is **data loaded at runtime**.

**a. Local / dev (`FileSource`):** functions live in a `functions/` directory
beside `ultrabase.yaml` with one shared `package.json`. `ultra dev` runs
`npm ci` at boot when the manifest changes, launches workers against the dir,
and hot-reloads on file change.

**b. Production / instancez (`S3Source`):** a **versioned functions bundle**
(a single tarball: all function source + the vendored `node_modules` + a
manifest) lives in object storage alongside the config. ultra:
1. fetches the bundle on boot and whenever its version/ETag changes (reusing the
   `Source`/`Watch` polling already used for config),
2. atomically extracts it to a writable dir (`/tmp` on Lambda; a volume on k8s),
3. starts a new worker pool on it, **drains** the old pool, then swaps —
   zero-downtime. Extraction is atomic (temp path → swap), so a partially
   written bundle never serves traffic.

**c. Building the bundle is part of `ultra deploy` — there is no separate
publish command.** `deploy` additionally runs `npm ci` against the shared
`package.json`, packs `node_modules` + source into the bundle tarball, uploads
it alongside the config, and **records the bundle's pointer/version in the
deployed config**. This is the **only** place with package-registry access.

**`serve` never builds — it only consumes a pre-built bundle, self-hosted
included.** This holds for *every* serve deployment: `serve` does not run
`npm ci` or vendor anything at boot; it reads the bundle pointer from its config
and downloads/extracts the already-built tarball. A self-hoster therefore runs
the build step (`ultra deploy`, targeting their own object storage) to produce
and reference the bundle **before** starting `serve`. Only `ultra dev` builds on
the fly (§8a). Rationale: production/serverless start-up stays free of a
toolchain and registry access (npm need not even exist in the runtime image),
and every `serve` start is deterministic.

**d. Shared dep set.** All node functions in a project share one `node_modules`
(single version space). Per-function dependency dirs are deferred (§11).

**e. supabase-js compatibility.** `supabase.functions.invoke('name', { body })`
POSTs to `/functions/v1/<name>` and returns `{ data, error }`. Status code and
`Content-Type` are part of the contract: 2xx → `data` = parsed body (JSON vs
text per supabase-js content sniffing); non-2xx → `FunctionsHttpError`;
transport failure → `FunctionsRelayError`. `test/integration/supabase-js/
run.mjs` gains coverage driving `functions.invoke`, asserting the success
envelope, the `FunctionsHttpError` path, and JSON-vs-text parsing.

**f. Node version:** a current Node LTS (**≥ 22**) baked into the base image.

## 9. Isolation model

Tenancy is **one ultrabase instance per tenant** (§1), so tenant↔tenant
isolation is the deployment boundary (separate Lambda/pod with a least-privilege
IAM role — an instancez concern, out of ultrabase's scope). What ultrabase owns
is walling a function off from **its own process's ambient credentials**:

- **Scrubbed child environment by default.** Worker processes are spawned with
  an explicitly constructed environment, **not** the inherited parent env. This
  alone defeats AWS credential pickup on both platforms: Lambda injects creds as
  `AWS_*` env vars; EKS/IRSA relies on `AWS_WEB_IDENTITY_TOKEN_FILE` /
  `AWS_ROLE_ARN` env vars — scrub them and the child's SDK cannot authenticate.
- **Explicit passthrough via `env:`.** A function receives only the names in its
  `env:` map, surfaced as `ctx.env` (and a minimal `process.env`). Values are
  literals or `${REF}` interpolations resolved by the parent.

### Config interpolation: the `ULTRA_ENV_` namespace (general, not function-specific)

`${REF}` resolves against an **in-memory map** — the `ULTRA_ENV_` namespace —
built at startup. **Function `env:` values are kept as raw `${ref}` strings in
the parsed `Config` and resolved at _invoke time_** (parent-side, into
`X-Ultra-Context`), so a plaintext secret never lands in the in-memory Config
that drift logs / `/openapi` / the dashboard echo. (The existing byte-level
`${VAR}` interpolation in `ParseBytes` continues to serve non-secret config from
the environment; extending map-backed resolution to arbitrary YAML positions is
a noted follow-up, not a v1 requirement.)

- **Sources (precedence high → low), exact `ULTRA_ENV_` prefix, no stripping
  (the ref names the full key):** (1) `ULTRA_ENV_*` process-env vars
  (cloud-native injection on Lambda/k8s); (2) the mode file — `.development.env`
  for `dev`, `.production.env` for `serve`; (3) the shared `.env`.
  `${ULTRA_ENV_STRIPE_API_KEY}` resolves from the `ULTRA_ENV_STRIPE_API_KEY`
  entry regardless of source — what's in the YAML is exactly the env-var name
  (React/Vite style).
- **In-memory map, not `os.Setenv` — load-bearing.** The `ULTRA_ENV_` namespace
  is parsed into a map and **never merged into the process environment.** This is
  a security improvement via *inheritance*: anything in `os.Environ()` is
  inherited by every child ultra spawns (the workers), so keeping these values
  out of the process env means the worker's default environment is already
  clean — defense in depth on top of the explicit scrub.
- **Resolve-from-map-only — the invariant that keeps it safe.** `${REF}`
  resolves from the `ULTRA_ENV_` map *only*; there is **no `os.Getenv`
  fallback** on a miss — a miss **fails at boot** (fail-early, listing missing
  keys). A fallback would let `${AWS_SECRET_ACCESS_KEY}` / `${ULTRABASE_ADMIN_KEY}`
  resolve again and re-open the hole. The `ULTRA_ENV_` prefix is disjoint from
  `ULTRABASE_*` (ultra's own config) and from any plain `ULTRA_*` vars ultra may
  reserve for itself, so `${ULTRA_ENV_*}` can never name the admin key/DSNs or
  ultra-internal settings.
- **Redaction & raw config.** Interpolated values are redacted from logs, error
  envelopes, `/openapi`, and any dashboard echo. The **raw, unexpanded** config
  (refs intact, no plaintext) is what gets logged or written back to S3.
- **Scope (v1):** the in-memory map governs the `${ULTRA_ENV_*}` interpolation
  namespace. ultra's own `ULTRABASE_*` config continues through the existing
  dotenv→`os.Setenv` path (preflight/loader/doctor read it via `os.Getenv`).
  Safe because the worker env is scrubbed regardless; routing *all* config
  through the map (no `os.Setenv` at all) is a clean follow-up, not required.
- **Residual: IMDS.** A node instance role reachable via `169.254.169.254` is
  not blockable by env scrubbing; blocking egress to it is **instancez's
  network-policy responsibility**, documented as such, not solved in ultrabase.

This is the plain reading of the requirement ("secured away… not necessarily
saying we won't give access") — scrub by default, grant explicitly — and needs
**no privileged container capabilities**, so it works unchanged on Lambda.

**Two trust classes, not one environment.** The scrub protects *ultrabase's
platform/AWS* identity (`AWS_*`, IRSA token vars, platform secrets). What is
deliberately *injected* is the *tenant's own data-plane* access (loopback URL,
anon key, a short-lived minted `service_role` JWT — §7).

**Honesty about the boundary.** Because we chose no in-process sandbox, the shim
and user code share one V8 isolate. So `ctx.supabase`'s "RLS as caller" is a
**correctness/convenience default, not a security control against the function
itself** — any function can reach `ctx.serviceClient` and act as service-role.
Acceptable *only* under the one-instance-per-tenant, trusted-code model (§1); the
per-tenant deployment boundary is what actually contains a misbehaving function.
Minting the service-role token per request (not a durable key) bounds the blast
radius to short-lived tokens.

## 10. Python fast-follow

The pool, the worker-as-HTTP-server model, the `X-Ultra-Context` envelope,
log capture, the injected-client contract, the bundle/shipping mechanism, the
env-scrub isolation, and HTTP routing are all **language-agnostic**. Adding
Python is:

1. A second shim (`worker.py`) running on the stdlib **`http.server`
   (`ThreadingHTTPServer`) — no framework, no uvicorn/gunicorn**. Concurrency is
   thread-per-request; the GIL is released during the I/O-bound loopback calls,
   which is exactly our workload. CPU scaling is the worker pool, as in Node.
2. A shared `requirements.txt`; `ultra deploy` vendors site-packages into the
   bundle (no runtime `pip`).
3. `python3` baked into the base image; `runtime: python` accepted by validation.
4. A supabase-py injected client (or a thin client matching `ctx.supabase`
   against the loopback REST API).

No Go-side architecture changes expected.

## 11. Out of scope for v1 (YAGNI)

- TypeScript authoring (JS only for now; revisit type-stripping later).
- Python runtime (fast-follow, §10).
- Per-function dependency directories / isolated version spaces.
- A WASM runtime (deps + network requirements make it the wrong tool — §12).
- Tenant↔tenant in-process sandboxing (handled by per-tenant deployment).
- Blocking IMDS egress (instancez network-policy concern — §9).
- Event/WAL- or cron-triggered code functions (HTTP-invoked only).
- Streaming / chunked responses; multi-version/canary deploys; custom domains.

## 12. Rationale for the load-bearing choices

- **HTTP handlers, not RPC:** primary surface for flexibility and Supabase
  parity.
- **Subprocess pool, not WASM:** functions need the real npm ecosystem and
  outbound network/DB access. WASM (wazero) would strip C-extension support,
  runtime package install, and ambient network. WASM wins only in a pure-compute,
  no-deps, no-network world, which this is not.
- **Worker-as-HTTP-server (plain request/response over a Unix socket):** uses
  Node's built-in `http` — no framework — and gets concurrency, cancellation,
  status codes, and native binary bodies *for free* from HTTP, instead of a
  bespoke framed/multiplexed socket protocol we'd have to build and reason about.
  One uniform request/response model end to end (client→ultra→worker→ultra).
- **Code + deps as data, built by `deploy`, consumed by `serve`:** satisfies "no
  image rebuild per deploy" with a single fixed base image; reuses the existing
  `Source`/`Watch` hot-reload; keeps package-registry access out of the runtime.
- **One instance per tenant + scrubbed env:** with the deployment boundary doing
  tenant↔tenant isolation, the only in-process requirement is keeping a function
  away from ultrabase's own creds — env scrubbing achieves that on Lambda and EKS
  without privileged capabilities.
- **Loopback-to-REST data access:** matches Supabase Edge Functions, preserves
  "RLS is the only authorization layer," adds zero new authz surface, keeps DB
  credentials out of user code.
- **In-memory `ULTRA_ENV_` map, no `os.Setenv`:** secrets never enter the
  inheritable environment, so worker children are clean by default.
- **JavaScript for v1:** ships sooner; the worker/transport/log machinery is
  language-agnostic, so TS (and Python) layer on later without rework.

## 13. Testing strategy

- **Unit:** the `X-Ultra-Context` envelope (encode/decode, faithful `req`
  reconstruction), pool/worker lifecycle, per-request timeout/cancel → 504,
  crash → 502 + `/healthz` recovery, bundle extract-and-swap, handler lookup,
  config validation (old-`functions:` rejection; `runtime`/`file`/`env` checks),
  `${ULTRA_ENV_*}` resolution (map-only, no `os.Getenv` fallback, fail-early),
  env-scrub (worker sees only `env:` values), log capture (NDJSON parse +
  per-request attribution via `AsyncLocalStorage`, `console.*` routing).
- **Integration (Docker, `node` present):** boot the real `FunctionRuntime`
  from both a `FileSource` dir and an extracted bundle; invoke via raw HTTP and
  via supabase-js `functions.invoke`; verify concurrent invocations on one
  worker; verify RLS-as-caller and service-role escalation through the injected
  clients; verify timeout (504), worker-crash (502), and that the server
  survives; verify a scrubbed worker cannot see `AWS_*`/parent env; verify logs
  are captured and attributed to the right request.
- **supabase-js compat:** extend `run.mjs` per §8e.

All CI jobs (`go build ./...`, `go test -race ./...`, integration for touched
packages, `npm test` for dashboard changes) must be green before push, per the
repo non-negotiable.
