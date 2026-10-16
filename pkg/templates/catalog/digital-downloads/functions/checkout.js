// Creates a pending purchase and returns a Stripe Checkout URL for one product.
const isHttpUrl = (v) => { try { return ["http:", "https:"].includes(new URL(v).protocol); } catch { return false; } };
const stripe = (key, path, body) =>
  fetch(`https://api.stripe.com/v1/${path}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-www-form-urlencoded" },
    body,
    signal: AbortSignal.timeout(20000),
  });

export default async function handler(req, ctx) {
  const key = ctx.env.STRIPE_SECRET_KEY;
  if (!key) return { status: 503, body: { error: "Stripe is not configured. Add INSTANCEZ_ENV_STRIPE_SECRET_KEY in Secrets." } };

  const { product_id, success_url, cancel_url } = req.body ?? {};
  if (!Number.isSafeInteger(product_id) || product_id < 1 || !isHttpUrl(success_url) || !isHttpUrl(cancel_url)) {
    return { status: 400, body: { error: "product_id (integer), success_url and cancel_url (http(s) URLs) are required" } };
  }
  const userId = ctx.claims.sub;

  const { data: product, error: productErr } = await ctx.supabase
    .from("products").select("id, name, price_cents, currency, active").eq("id", product_id).maybeSingle();
  if (productErr) return { status: 500, body: { error: productErr.message } };
  if (!product || !product.active) return { status: 404, body: { error: "Product not found" } };

  const { data: owned, error: ownedErr } = await ctx.supabase
    .from("purchases").select("id").eq("product_id", product.id).eq("user_id", userId).eq("status", "paid").limit(1);
  if (ownedErr) return { status: 500, body: { error: ownedErr.message } };
  if (owned?.length) return { status: 409, body: { error: "You already own this product" } };

  const form = new URLSearchParams({
    mode: "payment",
    success_url,
    cancel_url,
    client_reference_id: userId,
    expires_at: String(Math.floor(Date.now() / 1000) + 31 * 60),
    "line_items[0][quantity]": "1",
    "line_items[0][price_data][currency]": product.currency,
    "line_items[0][price_data][unit_amount]": String(product.price_cents),
    "line_items[0][price_data][product_data][name]": product.name,
  });
  let session;
  try {
    const res = await stripe(key, "checkout/sessions", form);
    session = await res.json();
    if (!res.ok) return { status: 502, body: { error: session.error?.message ?? "Stripe error" } };
  } catch {
    return { status: 502, body: { error: "Could not reach Stripe, try again" } };
  }

  const { error } = await ctx.serviceClient.from("purchases").insert({
    user_id: userId, product_id: product.id, amount_cents: product.price_cents,
    currency: product.currency, stripe_session_id: session.id,
  });
  if (error) {
    await stripe(key, `checkout/sessions/${session.id}/expire`).catch(() => {});
    return { status: 500, body: { error: error.message } };
  }
  return { status: 200, body: { url: session.url } };
}
