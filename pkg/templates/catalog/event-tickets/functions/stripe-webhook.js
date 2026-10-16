import { createHmac, timingSafeEqual, randomBytes } from "node:crypto";

// Verifies Stripe's signature, then issues tickets for paid sessions and frees seats for expired ones.
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
  const orders = () => ctx.serviceClient.from("orders");

  if (event.type === "checkout.session.expired") {
    const { error } = await orders().update({ status: "expired" }).eq("stripe_session_id", session.id).eq("status", "pending");
    return error ? { status: 500, body: { error: error.message } } : { status: 200, body: { received: true } };
  }
  if (event.type !== "checkout.session.completed" || session.payment_status !== "paid") return { status: 200, body: { ignored: true } };

  const { data: order, error: readErr } = await orders().select("id, user_id, ticket_type_id, quantity, total_cents, currency, status").eq("stripe_session_id", session.id).maybeSingle();
  if (readErr) return { status: 500, body: { error: readErr.message } };
  if (!order || order.status === "expired") return { status: 200, body: { ignored: true } };
  if (order.total_cents !== session.amount_total || order.currency !== String(session.currency ?? "").toLowerCase()) {
    ctx.log.warn("stripe amount differs from order", { order: order.id, session: session.id });
    return { status: 200, body: { ignored: true } };
  }

  const { error: payErr } = await orders().update({ status: "paid" }).eq("id", order.id).eq("status", "pending");
  if (payErr) return { status: 500, body: { error: payErr.message } };
  // (order_id, seq) is unique, so a retried or concurrent delivery cannot issue extra tickets.
  const rows = Array.from({ length: order.quantity }, (_, i) => ({
    order_id: order.id,
    seq: i + 1,
    ticket_type_id: order.ticket_type_id,
    owner_id: order.user_id,
    code: randomBytes(12).toString("hex"),
  }));
  const { error } = await ctx.serviceClient.from("tickets").upsert(rows, { onConflict: "order_id,seq", ignoreDuplicates: true });
  if (error) return { status: 500, body: { error: error.message } };
  return { status: 200, body: { received: true } };
}
