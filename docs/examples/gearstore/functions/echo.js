/**
 * echo — explore the request/ctx surface instancez hands a code function.
 *
 * Served at /functions/v1/echo (any method). No deps, no auth.
 *
 * Try it:
 *   curl -s 'http://localhost:8080/functions/v1/echo/anything?q=hi&limit=5' \
 *     -H "apikey: $PUBLISHABLE_KEY" -H 'content-type: application/json' \
 *     -d '{"hello":"world"}' | jq
 *
 * handler(req, ctx)
 *   req.method   – "GET" | "POST" | ...
 *   req.path     – the full request path, e.g. "/functions/v1/echo/anything"
 *   req.query    – query params as { key: value } strings (first value per key)
 *   req.headers  – request headers, keys lowercased (first value per key)
 *   req.body     – parsed object when Content-Type is application/json; the raw
 *                  string otherwise; undefined when there is no body
 *   ctx.claims   – the verified JWT payload, or null when the caller is anonymous
 *   ctx.env      – only the keys declared in this function's `env:` block
 *   ctx.log      – structured logger (debug/info/warn/error) → instancez logs
 */
export default async function handler(req, ctx) {
  ctx.log.info("echo called", { method: req.method, path: req.path });

  return {
    status: 200,
    headers: { "content-type": "application/json" },
    body: {
      method: req.method,
      path: req.path,
      query: req.query, // e.g. { q: "hi", limit: "5" } — values are strings
      contentType: req.headers["content-type"] ?? null,
      userAgent: req.headers["user-agent"] ?? null,
      body: req.body ?? null,
      caller: ctx.claims ? { sub: ctx.claims.sub, role: ctx.claims.role } : null,
      // ctx.env only exposes the function's declared env keys (see instancez.yaml):
      apiKeyConfigured: Boolean(ctx.env.API_KEY),
    },
  };
}
