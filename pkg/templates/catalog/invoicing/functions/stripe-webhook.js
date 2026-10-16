import { createHmac, timingSafeEqual } from "node:crypto";

// Marks an invoice paid when its Payment Link checkout completes. Safe to replay: only a sent invoice flips.
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
  try { event = JSON.parse(req.rawBody.toString()); } catch { return { status: 400, body: { error: "Invalid payload" } }; }
  if (!["checkout.session.completed", "checkout.session.async_payment_succeeded"].includes(event.type)) return { status: 200, body: { ignored: true } };

  const session = event.data?.object ?? {};
  if (session.payment_status !== "paid" || typeof session.payment_link !== "string") return { status: 200, body: { ignored: true } };

  const { data: inv, error } = await ctx.serviceClient.from("invoices")
    .select("id, status, total_cents, currency").eq("payment_link_id", session.payment_link).maybeSingle();
  if (error) return { status: 500, body: { error: error.message } };
  if (!inv) return { status: 200, body: { ignored: "unknown payment link" } };
  if (inv.status === "paid") return { status: 200, body: { received: true, already: true } };
  if (inv.status !== "sent") return { status: 200, body: { ignored: `invoice is ${inv.status}` } };
  if (session.amount_total !== inv.total_cents || String(session.currency).toLowerCase() !== inv.currency) {
    return { status: 200, body: { ignored: "amount or currency mismatch" } };
  }

  const { error: upErr } = await ctx.serviceClient.from("invoices")
    .update({ status: "paid", paid_at: new Date().toISOString() }).eq("id", inv.id).eq("status", "sent");
  if (upErr) return { status: 500, body: { error: upErr.message } };
  return { status: 200, body: { received: true } };
}
