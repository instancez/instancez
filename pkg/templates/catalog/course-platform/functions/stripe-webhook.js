import { createHmac, timingSafeEqual } from "node:crypto";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

// Verifies Stripe's signature, grants the paid enrollment, and revokes it on a full refund. Replay-safe.
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
  const obj = event.data?.object ?? {};

  if (event.type === "charge.refunded") {
    if (obj.refunded !== true || typeof obj.payment_intent !== "string") return { status: 200, body: { ignored: true } };
    const { error } = await ctx.serviceClient.from("enrollments").delete().eq("stripe_payment_intent_id", obj.payment_intent).eq("source", "stripe");
    if (error) return { status: 500, body: { error: error.message } };
    return { status: 200, body: { received: true } };
  }

  if (event.type !== "checkout.session.completed" && event.type !== "checkout.session.async_payment_succeeded") return { status: 200, body: { ignored: true } };

  const courseId = Number(obj.metadata?.course_id);
  const userId = obj.metadata?.user_id;
  if (obj.payment_status !== "paid" || !Number.isSafeInteger(courseId) || courseId <= 0 || !UUID.test(String(userId ?? ""))) return { status: 200, body: { ignored: true } };

  const { error } = await ctx.serviceClient.from("enrollments").upsert({
    user_id: userId, course_id: courseId, source: "stripe", stripe_session_id: obj.id,
    stripe_payment_intent_id: typeof obj.payment_intent === "string" ? obj.payment_intent : null,
  }, { onConflict: "user_id,course_id", ignoreDuplicates: true });
  if (error?.code === "23503") {
    console.error(`paid session ${obj.id} has no matching user or course; refund it in Stripe`);
    return { status: 200, body: { ignored: "user or course no longer exists" } };
  }
  if (error) return { status: 500, body: { error: error.message } };
  return { status: 200, body: { received: true } };
}
