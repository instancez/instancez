import { createHmac, timingSafeEqual } from "node:crypto";

// Verifies Stripe's signature, then marks the purchase paid or expired.
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
  const patch =
    event?.type === "checkout.session.completed" && session?.payment_status === "paid"
      ? { status: "paid", paid_at: new Date().toISOString() }
      : event?.type === "checkout.session.expired"
        ? { status: "expired" }
        : null;
  if (!patch || !session?.id) return { status: 200, body: { ignored: true } };

  const isPaid = patch.status === "paid";
  let q = ctx.serviceClient.from("purchases").update(patch).eq("stripe_session_id", session.id);
  if (isPaid) {
    // Paid beats a local "expired".
    q = q.in("status", ["pending", "expired"]).eq("amount_cents", session.amount_total).eq("currency", String(session.currency ?? "").toLowerCase());
  } else {
    q = q.eq("status", "pending");
  }
  const { data, error } = await q.select("id");
  if (error) return { status: 500, body: { error: error.message } };
  if (isPaid && !data?.length) ctx.log.warn("purchase not marked paid: unknown, already handled or amount mismatch", { session: session.id });
  return { status: 200, body: { received: true } };
}
