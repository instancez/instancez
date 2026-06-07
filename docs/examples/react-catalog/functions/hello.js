/**
 * Example code function for docs/example-ultrabase.yaml.
 *
 * Served at: POST /functions/v1/hello
 * Invoke via supabase-js: supabase.functions.invoke('hello', { body: { name: 'ultrabase' } })
 *
 * handler(req, ctx)
 *   req  – { method, path, query, headers, body }
 *   ctx  – { supabase, serviceClient, claims, env, log, signal }
 */
export default async function handler(req, ctx) {
  const name = req.body?.name ?? "world";
  ctx.log.info("hello function called", { name });
  return {
    status: 200,
    body: { hello: name },
  };
}
