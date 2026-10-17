import { createHmac, timingSafeEqual } from "node:crypto";

// Verifies Stripe's signature, then marks the matching order paid or cancelled.
export default async function handler(req, ctx) {
  const secret = ctx.env.STRIPE_WEBHOOK_SECRET;
  if (!secret) return { status: 503, body: { error: "Webhook not configured. Add INSTANCEZ_ENV_STRIPE_WEBHOOK_SECRET in Secrets." } };

  const header = String(req.headers["stripe-signature"] ?? "");
  const pairs = header.split(",").map((p) => p.trim().split("="));
  const t = pairs.find(([k]) => k === "t")?.[1] ?? "";
  const expected = createHmac("sha256", secret).update(`${t}.${req.rawBody}`).digest("hex");
  const valid = pairs.some(([k, v]) => k === "v1" && /^[0-9a-f]{64}$/.test(v ?? "") && timingSafeEqual(Buffer.from(v), Buffer.from(expected)));
  const fresh = Math.abs(Date.now() / 1000 - Number(t)) < 300;
  if (!valid || !fresh) return { status: 400, body: { error: "Invalid signature" } };

  let event;
  try { event = JSON.parse(req.rawBody.toString()); } catch { return { status: 400, body: { error: "Invalid JSON" } }; }
  const session = event?.data?.object;
  if (!session?.id) return { status: 200, body: { ignored: true } };
  const db = ctx.serviceClient;

  if (event.type === "checkout.session.expired") {
    const { error } = await db.from("orders").update({ status: "cancelled" }).eq("stripe_session_id", session.id).eq("status", "pending");
    return error ? { status: 500, body: { error: error.message } } : { status: 200, body: { received: true } };
  }
  if (event.type !== "checkout.session.completed" || session.payment_status !== "paid") return { status: 200, body: { ignored: true } };

  const { data: order, error } = await db
    .from("orders")
    .update({ status: "paid", paid_at: new Date().toISOString(), shipping: session.shipping_details ?? session.collected_information?.shipping_details ?? null })
    .eq("stripe_session_id", session.id).eq("status", "pending")
    .eq("total_cents", session.amount_total).eq("currency", String(session.currency ?? "").toLowerCase())
    .select("id, user_id").maybeSingle();
  if (error) return { status: 500, body: { error: error.message } };
  if (!order) {
    ctx.log.warn("order not marked paid: unknown, already handled or amount mismatch", { session: session.id });
    return { status: 200, body: { received: true, updated: false } };
  }

  const { data: items, error: itemsErr } = await db.from("order_items").select("product_id, quantity").eq("order_id", order.id);
  if (itemsErr) return { status: 500, body: { error: itemsErr.message } };
  for (const it of items ?? []) {
    if (!it.product_id) continue;
    for (let tries = 0; ; tries++) {
      const { data: prod, error: readErr } = await db.from("products").select("stock").eq("id", it.product_id).maybeSingle();
      if (readErr) return { status: 500, body: { error: readErr.message } };
      if (!prod) break;
      const { data: hit, error: stockErr } = await db.from("products")
        .update({ stock: Math.max(prod.stock - it.quantity, 0) }).eq("id", it.product_id).eq("stock", prod.stock).select("id");
      if (stockErr) return { status: 500, body: { error: stockErr.message } };
      if (hit?.length) break;
      if (tries >= 4) return { status: 500, body: { error: "stock update contention" } };
    }
  }
  const productIds = (items ?? []).map((i) => i.product_id).filter(Boolean);
  if (productIds.length) {
    const { error: cartErr } = await db.from("cart_items").delete().eq("user_id", order.user_id).in("product_id", productIds);
    if (cartErr) return { status: 500, body: { error: cartErr.message } };
  }
  return { status: 200, body: { received: true, updated: true } };
}
