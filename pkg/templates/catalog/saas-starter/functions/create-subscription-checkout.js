// Starts a Stripe Checkout subscription for an organization. Only its owners and admins may call it.
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const isHttpUrl = (v) => { try { return ["http:", "https:"].includes(new URL(v).protocol); } catch { return false; } };

export default async function handler(req, ctx) {
  const env = ctx.env;
  if (!env.STRIPE_SECRET_KEY) return { status: 503, body: { error: "Stripe is not configured. Add INSTANCEZ_ENV_STRIPE_SECRET_KEY in Secrets." } };

  const { org_id, plan, success_url, cancel_url } = req.body ?? {};
  const prices = { pro: ["STRIPE_PRICE_PRO", "INSTANCEZ_ENV_STRIPE_PRICE_PRO"], team: ["STRIPE_PRICE_TEAM", "INSTANCEZ_ENV_STRIPE_PRICE_TEAM"] };
  const priceEnv = typeof plan === "string" && Object.hasOwn(prices, plan) ? prices[plan] : null;
  if (!UUID.test(String(org_id ?? ""))) return { status: 400, body: { error: "org_id must be a uuid" } };
  if (!priceEnv) return { status: 400, body: { error: "plan must be 'pro' or 'team'" } };
  if (!isHttpUrl(success_url) || !isHttpUrl(cancel_url)) return { status: 400, body: { error: "success_url and cancel_url must be http(s) URLs" } };
  const price = env[priceEnv[0]];
  if (!price) return { status: 503, body: { error: `Price not configured. Add ${priceEnv[1]} in Secrets.` } };

  const userId = ctx.claims.sub;
  const { data: me } = await ctx.supabase.from("memberships").select("role").eq("org_id", org_id).eq("user_id", userId).maybeSingle();
  if (!me || me.role === "member") return { status: 403, body: { error: "Only owners and admins can manage billing" } };

  const { data: sub } = await ctx.supabase.from("subscriptions").select("stripe_customer_id, status").eq("org_id", org_id).maybeSingle();
  if (sub && ["active", "trialing", "past_due"].includes(sub.status)) {
    return { status: 409, body: { error: "Organization already has a subscription. Use the billing portal to change it." } };
  }

  const form = new URLSearchParams({
    mode: "subscription", success_url, cancel_url, client_reference_id: org_id,
    "line_items[0][price]": price, "line_items[0][quantity]": "1",
    "subscription_data[metadata][org_id]": org_id, "metadata[org_id]": org_id,
  });
  if (sub?.stripe_customer_id) form.set("customer", sub.stripe_customer_id);
  else if (ctx.claims.email) form.set("customer_email", ctx.claims.email);

  const res = await fetch("https://api.stripe.com/v1/checkout/sessions", {
    method: "POST",
    headers: { Authorization: `Bearer ${env.STRIPE_SECRET_KEY}`, "Content-Type": "application/x-www-form-urlencoded" },
    body: form,
    signal: AbortSignal.timeout(20000),
  }).catch((e) => ({ ok: false, json: async () => ({ error: { message: e.message } }) }));
  const session = await res.json().catch(() => ({}));
  if (!res.ok) return { status: 502, body: { error: session.error?.message ?? "Stripe error" } };
  return { status: 200, body: { url: session.url } };
}
