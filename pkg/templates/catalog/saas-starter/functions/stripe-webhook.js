import { createHmac, timingSafeEqual } from "node:crypto";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

// Verifies the signature, re-reads the subscription from Stripe (so event order never matters) and upserts it.
export default async function handler(req, ctx) {
  const { STRIPE_WEBHOOK_SECRET: secret, STRIPE_SECRET_KEY: key, STRIPE_PRICE_PRO, STRIPE_PRICE_TEAM } = ctx.env;
  if (!secret) return { status: 503, body: { error: "Webhook not configured. Add INSTANCEZ_ENV_STRIPE_WEBHOOK_SECRET in Secrets." } };
  if (!key) return { status: 503, body: { error: "Stripe is not configured. Add INSTANCEZ_ENV_STRIPE_SECRET_KEY in Secrets." } };

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
  let subId;
  if (event.type === "checkout.session.completed") subId = obj.mode === "subscription" ? obj.subscription : null;
  else if (event.type === "customer.subscription.updated" || event.type === "customer.subscription.deleted") subId = obj.id;
  if (!subId) return { status: 200, body: { ignored: true } };

  const res = await fetch(`https://api.stripe.com/v1/subscriptions/${encodeURIComponent(subId)}`, {
    headers: { Authorization: `Bearer ${key}` },
    signal: AbortSignal.timeout(20000),
  }).catch((e) => ({ ok: false, status: 502, json: async () => ({ error: { message: e.message } }) }));
  const sub = await res.json().catch(() => ({}));
  if (res.status === 404) return { status: 200, body: { ignored: "subscription no longer exists" } };
  if (!res.ok) return { status: 502, body: { error: sub.error?.message ?? "Stripe error" } };

  const orgId = sub.metadata?.org_id ?? obj.client_reference_id;
  if (!UUID.test(String(orgId ?? ""))) return { status: 200, body: { ignored: "no valid org_id on subscription" } };

  const { data: stored, error: readErr } = await ctx.serviceClient.from("subscriptions").select("stripe_subscription_id, status").eq("org_id", orgId).maybeSingle();
  if (readErr) return { status: 500, body: { error: readErr.message } };
  if (stored?.stripe_subscription_id && stored.stripe_subscription_id !== sub.id && ["active", "trialing"].includes(stored.status)) {
    return { status: 200, body: { ignored: "org has another active subscription" } };
  }

  const item = sub.items?.data?.[0];
  const priceId = item?.price?.id;
  const planId = priceId && priceId === STRIPE_PRICE_PRO ? "pro" : priceId && priceId === STRIPE_PRICE_TEAM ? "team" : null;
  const periodEnd = item?.current_period_end ?? sub.current_period_end;

  const { error } = await ctx.serviceClient.from("subscriptions").upsert({
    org_id: orgId,
    stripe_customer_id: typeof sub.customer === "string" ? sub.customer : sub.customer?.id ?? null,
    stripe_subscription_id: sub.id,
    plan_id: planId,
    status: sub.status,
    current_period_end: periodEnd ? new Date(periodEnd * 1000).toISOString() : null,
    cancel_at_period_end: Boolean(sub.cancel_at_period_end),
    updated_at: new Date().toISOString(),
  }, { onConflict: "org_id" });
  if (error?.code === "23503") return { status: 200, body: { ignored: "organization no longer exists" } };
  if (error) return { status: 500, body: { error: error.message } };
  return { status: 200, body: { received: true } };
}
