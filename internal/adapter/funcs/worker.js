import http from "node:http";
import { pathToFileURL } from "node:url";
import { AsyncLocalStorage } from "node:async_hooks";

// AsyncLocalStorage tracks the current request context so log lines emitted
// from concurrent handler invocations can be attributed to the right request.
const als = new AsyncLocalStorage();

// emitLog writes a single NDJSON log line to stdout. Stdout is EXCLUSIVELY
// for NDJSON log lines; HTTP responses travel over the unix socket.
// Wrapped in try/catch so a circular-ref or BigInt in fields never throws
// and rejects the handler (which would 500 the request).
function emitLog(level, msg, fields) {
  const s = als.getStore() || {};
  let line;
  try {
    line = JSON.stringify({
      ts: Date.now(),
      level,
      requestId: s.requestId,
      fn: s.fn,
      msg: typeof msg === "string" ? msg : JSON.stringify(msg),
      fields: (fields !== null && typeof fields === "object" && !Array.isArray(fields))
        ? fields
        : (fields === undefined ? undefined : { value: fields }),
    });
  } catch {
    line = JSON.stringify({
      ts: Date.now(), level, requestId: s.requestId, fn: s.fn,
      msg: "[log serialization failed]",
    });
  }
  process.stdout.write(line + "\n");
}

// Patch console so that all stdout-writing console methods from function code
// are captured as NDJSON log lines attributed to the current request. This
// keeps stdout exclusively NDJSON; HTTP responses travel over the unix socket.
// Note: Node's own process-warning handler routes through console.error, so
// Node warnings also become NDJSON ERROR lines with empty requestId/fn
// (outside any als.run context).
// Methods and their mapped log levels:
//   log/info/dir/table/group/groupEnd/count → "info"
//   warn → "warn"
//   error → "error"
//   debug → "debug"
//   trace → "error"  (trace includes a stack, maps to error severity)
for (const [m, lvl] of [
  ["log",      "info"],
  ["info",     "info"],
  ["warn",     "warn"],
  ["error",    "error"],
  ["debug",    "debug"],
  ["dir",      "info"],
  ["table",    "info"],
  ["trace",    "error"],
  ["group",    "info"],
  ["groupEnd", "info"],
  ["count",    "info"],
]) {
  console[m] = (msg, fields) => emitLog(lvl, msg, fields);
}

// ctxLog is the structured logger exposed as ctx.log in function handlers.
const ctxLog = {
  debug: (m, f) => emitLog("debug", m, f),
  info:  (m, f) => emitLog("info",  m, f),
  warn:  (m, f) => emitLog("warn",  m, f),
  error: (m, f) => emitLog("error", m, f),
};

// Guarded, lazy load of supabase-js. Functions that never touch ctx.supabase /
// ctx.serviceClient (and deployments that don't vendor the package) must still
// boot — a top-level static import would crash the whole worker at load time.
let createClient;
try {
  ({ createClient } = await import("@supabase/supabase-js"));
} catch (e) {
  // not vendored; data-access clients throw on first access (see buildCtx).
  // Use stderr so this warning doesn't pollute the NDJSON stdout stream.
  process.stderr.write("ultra-worker: @supabase/supabase-js not available: " + String(e && e.message || e) + "\n");
}

// args: <socketPath> <fnName=absPath,...>
const [, , socketPath, fnSpec] = process.argv;
const fns = {};
for (const pair of fnSpec.split(",").filter(Boolean)) {
  const [name, file] = pair.split("=");
  try {
    const mod = await import(pathToFileURL(file).href);
    fns[name] = mod.default;
  } catch (e) {
    // Import errors go to stderr (not stdout) so they don't corrupt NDJSON.
    process.stderr.write("ultra-worker: failed to import " + name + " (" + file + "): " + String(e && e.message || e) + "\n");
  }
}

// lowerFirst converts a map[string][]string (Go JSON encoding) to a flat
// object with lowercased keys and the first array value per key.
// Returns {} when h is null/undefined.
function lowerFirst(h) {
  if (!h) return {};
  const out = {};
  for (const [k, v] of Object.entries(h)) {
    out[k.toLowerCase()] = Array.isArray(v) ? v[0] : v;
  }
  return out;
}

// firstVals converts a map[string][]string to { key: firstValue }.
// Returns {} when q is null/undefined.
function firstVals(q) {
  if (!q) return {};
  const out = {};
  for (const [k, v] of Object.entries(q)) {
    out[k] = Array.isArray(v) ? v[0] : v;
  }
  return out;
}

// buildCtx assembles the second argument passed to a function handler. It
// builds two @supabase/supabase-js clients pointed at ultrabase's own REST API
// over loopback:
//   - supabase: carries the caller's JWT, so RLS applies as the caller.
//   - serviceClient: carries an ultra-minted service_role JWT (BYPASSRLS) for
//     explicit escalation.
// Credentials arrive in the decoded X-Ultra-Context (uctx.dataPlane), never the
// child env. env/log are placeholders filled by later tasks.
function buildCtx(uctx) {
  const dp = uctx.dataPlane || {};
  const mk = (token) => {
    if (!createClient) throw new Error("@supabase/supabase-js not vendored");
    return createClient(dp.url, dp.anonKey, {
      global: {
        headers: {
          Authorization: `Bearer ${token || dp.anonKey}`,
          apikey: dp.anonKey,
        },
      },
    });
  };
  // Lazy getters: the clients are only constructed on first access, so
  // functions that ignore ctx (and envs without supabase-js) never trip mk().
  return {
    get supabase() {
      return mk(dp.callerToken);
    },
    get serviceClient() {
      return mk(dp.serviceToken);
    },
    claims: uctx.claims ?? null,
    env: uctx.env || {},
    log: ctxLog,
  };
}

// addCtxSignal attaches an AbortController's signal to ctx as ctx.signal.
// User handlers MAY honor ctx.signal to abort their own work early when the
// caller disconnects (e.g. the Go-side per-request timeout closes the socket).
// Honoring it is optional: even if a handler ignores the signal, the Go-side
// timeout still returns 504 and the worker's late response is discarded.
function addCtxSignal(ctx, signal) {
  ctx.signal = signal;
  return ctx;
}

const server = http.createServer(async (req, res) => {
  if (req.url === "/healthz") { res.writeHead(200); res.end("ok"); return; }
  const fnName = req.headers["x-ultra-fn"];
  const handler = fns[fnName];
  if (!handler) { res.writeHead(404); res.end(JSON.stringify({ message: "unknown fn" })); return; }

  // Best-effort worker-side abort: when the request connection closes before a
  // response is sent (e.g. the Go-side per-request timeout closes the socket),
  // abort the per-request controller so handlers honoring ctx.signal can bail.
  const ac = new AbortController();
  const onClose = () => {
    if (!res.writableEnded) ac.abort();
  };
  req.on("close", onClose);
  req.on("aborted", onClose);

  // Swallow socket 'error' events. When the Go side enforces a per-request
  // timeout it destroys the connection; a late res.write/res.end (or a handler
  // that ignores ctx.signal and finishes after the deadline) then emits an
  // 'error' on the response stream. Without a listener that becomes an
  // unhandled 'error' event that crashes the whole worker process. Attaching a
  // no-op listener keeps the worker alive so its late response is simply
  // discarded — exactly the "timeout ⇒ worker stays healthy" contract.
  res.on("error", () => {});
  req.on("error", () => {});

  // canWrite guards every response write: once the socket is gone (timeout/
  // disconnect) there is nothing to write to, and attempting it would throw.
  const canWrite = () => !res.writableEnded && !res.destroyed;

  // Stage 1: read body + decode context + parse body by content-type +
  // build the injected ctx (data-access clients). Any failure here is a 400.
  let reqObj;
  let fnCtx;
  let requestId;
  try {
    const chunks = [];
    for await (const c of req) chunks.push(c);
    const rawBody = Buffer.concat(chunks);

    const uctx = JSON.parse(Buffer.from(req.headers["x-ultra-context"], "base64").toString());
    requestId = uctx.requestId;
    const headers = lowerFirst(uctx.headers);
    const ct = headers["content-type"] || "";
    const body = ct.includes("application/json")
      ? (rawBody.length ? JSON.parse(rawBody.toString()) : undefined)
      : (rawBody.length ? rawBody.toString() : undefined);
    reqObj = { method: uctx.method, path: uctx.path, query: firstVals(uctx.query), headers, body };
    fnCtx = addCtxSignal(buildCtx(uctx), ac.signal);
  } catch (e) {
    if (canWrite() && !res.headersSent) {
      res.writeHead(400);
      res.end(JSON.stringify({ message: "bad request: " + String((e && e.message) || e) }));
    }
    return;
  }

  // Stage 2: call handler + serialize response. Any failure here is a 500.
  // Wrap the invocation in als.run so every log line emitted during this
  // request (via ctx.log or patched console) is attributed to requestId/fnName.
  try {
    const result = await als.run({ requestId, fn: fnName }, () => handler(reqObj, fnCtx));
    // The Go-side timeout may have destroyed the socket while the handler ran;
    // if so, drop the late response (the worker stays alive and healthy).
    if (!canWrite()) return;
    const headers = result.headers || { "content-type": "application/json" };
    const payload = typeof result.body === "string" ? result.body : JSON.stringify(result.body ?? null);
    res.writeHead(result.status || 200, headers);
    res.end(payload);
  } catch (e) {
    if (canWrite() && !res.headersSent) {
      res.writeHead(500);
      res.end(JSON.stringify({ message: String((e && e.message) || e) }));
    }
  }
});
server.listen(socketPath);
