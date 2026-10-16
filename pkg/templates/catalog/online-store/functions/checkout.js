// Turns the caller's cart into a pending order and returns a Stripe Checkout URL.
const isHttpUrl = (v) => { try { return ["http:", "https:"].includes(new URL(v).protocol); } catch { return false; } };
const stripe = (key, path, init = {}) =>
  fetch(`https://api.stripe.com/v1/${path}`, {
    method: "POST",
    ...init,
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-www-form-urlencoded" },
    signal: AbortSignal.timeout(20000),
  });

export default async function handler(req, ctx) {
  const key = ctx.env.STRIPE_SECRET_KEY;
  if (!key) return { status: 503, body: { error: "Stripe is not configured. Add INSTANCEZ_ENV_STRIPE_SECRET_KEY in Secrets." } };

  const { success_url, cancel_url, shipping_countries } = req.body ?? {};
  if (!isHttpUrl(success_url) || !isHttpUrl(cancel_url)) return { status: 400, body: { error: "success_url and cancel_url must be http(s) URLs" } };
  if (shipping_countries !== undefined && (!Array.isArray(shipping_countries) || shipping_countries.length > 100 || !shipping_countries.every((c) => /^[A-Z]{2}$/.test(c)))) {
    return { status: 400, body: { error: "shipping_countries must be an array of 2-letter country codes" } };
  }

  const userId = ctx.claims.sub;
  const { data: items, error } = await ctx.supabase
    .from("cart_items")
    .select("quantity, products(id, name, price_cents, currency, stock, active)")
    .eq("user_id", userId)
    .limit(101);
  if (error) return { status: 400, body: { error: error.message } };

  const lines = (items ?? []).filter((i) => i.products?.active && i.quantity > 0);
  if (lines.length === 0) return { status: 400, body: { error: "Cart is empty" } };
  if (lines.length > 100) return { status: 400, body: { error: "Cart has too many items (max 100)" } };
  if (lines.some((i) => i.products.stock < i.quantity)) return { status: 409, body: { error: "Not enough stock for an item in your cart" } };
  const currency = lines[0].products.currency;
  if (lines.some((i) => i.products.currency !== currency)) return { status: 400, body: { error: "Cart mixes currencies" } };

  const total = lines.reduce((sum, i) => sum + i.products.price_cents * i.quantity, 0);
  if (total < 1) return { status: 400, body: { error: "Order total must be above zero" } };
  const form = new URLSearchParams({
    mode: "payment",
    success_url,
    cancel_url,
    client_reference_id: userId,
    expires_at: String(Math.floor(Date.now() / 1000) + 31 * 60),
  });
  shipping_countries?.forEach((c, n) => form.set(`shipping_address_collection[allowed_countries][${n}]`, c));
  lines.forEach((i, n) => {
    form.set(`line_items[${n}][quantity]`, String(i.quantity));
    form.set(`line_items[${n}][price_data][currency]`, currency);
    form.set(`line_items[${n}][price_data][unit_amount]`, String(i.products.price_cents));
    form.set(`line_items[${n}][price_data][product_data][name]`, i.products.name);
  });

  let session;
  try {
    const res = await stripe(key, "checkout/sessions", { body: form });
    session = await res.json();
    if (!res.ok) return { status: 502, body: { error: session.error?.message ?? "Stripe error" } };
  } catch {
    return { status: 502, body: { error: "Could not reach Stripe, try again" } };
  }

  const expire = () => stripe(key, `checkout/sessions/${session.id}/expire`).catch(() => {});
  const { data: order, error: orderErr } = await ctx.serviceClient
    .from("orders")
    .insert({ user_id: userId, total_cents: total, currency, stripe_session_id: session.id })
    .select("id")
    .single();
  if (orderErr) {
    await expire();
    return { status: 500, body: { error: orderErr.message } };
  }
  const { error: itemsErr } = await ctx.serviceClient.from("order_items").insert(
    lines.map((i) => ({ order_id: order.id, product_id: i.products.id, name: i.products.name, unit_price_cents: i.products.price_cents, quantity: i.quantity })),
  );
  if (itemsErr) {
    await ctx.serviceClient.from("orders").delete().eq("id", order.id);
    await expire();
    return { status: 500, body: { error: itemsErr.message } };
  }
  return { status: 200, body: { url: session.url, order_id: order.id } };
}
