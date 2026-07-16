/**
 * webhook — import a product review from an external system.
 *
 * Served at /functions/v1/webhook. auth_required: false — an external sender
 * can't present a user JWT, so we authenticate the REQUEST instead, by verifying
 * an HMAC signature over the raw body against a shared secret.
 *
 * Demonstrates:
 *   - reading a raw (non-JSON) body + a custom header
 *   - a secret from ctx.env (declared as ${INSTANCEZ_ENV_WEBHOOK_SECRET} in the YAML;
 *     resolved from the INSTANCEZ_ENV_ namespace, never inherited from the host env)
 *   - a Node built-in (node:crypto) and a 3rd-party npm dep (nanoid)
 *   - ctx.serviceClient (BYPASSRLS), used *meaningfully*: the `reviews` INSERT
 *     policy is `user_id = auth.uid()`, so an anonymous client can't insert an
 *     imported review (no user). serviceClient writes it with user_id = null.
 *
 * Send it:
 *   BODY='{"product_id":1,"author":"Imported","rating":5,"body":"from partner site"}'
 *   SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$INSTANCEZ_ENV_WEBHOOK_SECRET" | awk '{print $2}')
 *   curl -s localhost:8080/functions/v1/webhook -H "x-signature: $SIG" --data-raw "$BODY"
 */
import { createHmac, timingSafeEqual } from "node:crypto";
import { nanoid } from "nanoid";

export default async function handler(req, ctx) {
  // For signature verification we need the EXACT bytes the sender signed. Send
  // the webhook with a non-JSON content-type (or no content-type) so req.body is
  // the raw string; if it arrived as JSON, fall back to a stable re-stringify.
  const raw =
    typeof req.body === "string" ? req.body : JSON.stringify(req.body ?? {});

  const provided = req.headers["x-signature"] ?? "";
  const expected = createHmac("sha256", ctx.env.WEBHOOK_SECRET)
    .update(raw)
    .digest("hex");

  const ok =
    provided.length === expected.length &&
    timingSafeEqual(Buffer.from(provided), Buffer.from(expected));
  if (!ok) {
    ctx.log.warn("webhook rejected: bad signature");
    return { status: 401, body: { error: "bad signature" } };
  }

  let payload;
  try {
    payload = JSON.parse(raw);
  } catch {
    return { status: 400, body: { error: "invalid JSON" } };
  }

  const importId = nanoid();
  ctx.log.info("importing review", { importId, product_id: payload.product_id });

  // No caller to attribute the row to → service_role (BYPASSRLS). A normal
  // ctx.supabase insert here would be rejected by the user_id = auth.uid() policy.
  const { data, error } = await ctx.serviceClient
    .from("reviews")
    .insert({
      product_id: payload.product_id,
      author: payload.author ?? "external",
      rating: payload.rating,
      body: payload.body ?? null,
      user_id: null, // imported, not owned by an instancez user
    })
    .select("id")
    .single();

  if (error) {
    ctx.log.error("review import failed", { importId, error: error.message });
    return { status: 400, body: { error: error.message } };
  }

  return { status: 200, body: { received: true, importId, reviewId: data.id } };
}
