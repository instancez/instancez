// Opens the Stripe Billing Portal for an organization that already has a Stripe customer.
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const isHttpUrl = (v) => { try { return ["http:", "https:"].includes(new URL(v).protocol); } catch { return false; } };

export default async function handler(req, ctx) {
  const key = ctx.env.STRIPE_SECRET_KEY;
  if (!key) return { status: 503, body: { error: "Stripe is not configured. Add INSTANCEZ_ENV_STRIPE_SECRET_KEY in Secrets." } };

  const { org_id, return_url } = req.body ?? {};
  if (!UUID.test(String(org_id ?? ""))) return { status: 400, body: { error: "org_id must be a uuid" } };
  if (!isHttpUrl(return_url)) return { status: 400, body: { error: "return_url must be an http(s) URL" } };

  const { data: me } = await ctx.supabase.from("memberships").select("role").eq("org_id", org_id).eq("user_id", ctx.claims.sub).maybeSingle();
  if (!me || me.role === "member") return { status: 403, body: { error: "Only owners and admins can manage billing" } };

  const { data: sub } = await ctx.supabase.from("subscriptions").select("stripe_customer_id").eq("org_id", org_id).maybeSingle();
  if (!sub?.stripe_customer_id) return { status: 404, body: { error: "No billing account yet. Subscribe first." } };

  const res = await fetch("https://api.stripe.com/v1/billing_portal/sessions", {
    method: "POST",
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ customer: sub.stripe_customer_id, return_url }),
    signal: AbortSignal.timeout(20000),
  }).catch((e) => ({ ok: false, json: async () => ({ error: { message: e.message } }) }));
  const session = await res.json().catch(() => ({}));
  if (!res.ok) return { status: 502, body: { error: session.error?.message ?? "Stripe error" } };
  return { status: 200, body: { url: session.url } };
}
