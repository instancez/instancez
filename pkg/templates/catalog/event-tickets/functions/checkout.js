// Holds seats for the caller, then returns a Stripe Checkout URL.
const isHttpUrl = (v) => { try { return ["http:", "https:"].includes(new URL(v).protocol); } catch { return false; } };
export default async function handler(req, ctx) {
  const key = ctx.env.STRIPE_SECRET_KEY;
  if (!key) return { status: 503, body: { error: "Stripe is not configured. Add INSTANCEZ_ENV_STRIPE_SECRET_KEY in Secrets." } };

  const { ticket_type_id, quantity, success_url, cancel_url } = req.body ?? {};
  if (!Number.isSafeInteger(ticket_type_id) || ticket_type_id < 1 || !Number.isSafeInteger(quantity) || quantity < 1 || quantity > 50) {
    return { status: 400, body: { error: "ticket_type_id and quantity (1-50) must be positive integers" } };
  }
  if (!isHttpUrl(success_url) || !isHttpUrl(cancel_url)) return { status: 400, body: { error: "success_url and cancel_url must be http(s) URLs" } };

  const { data: hold, error } = await ctx.supabase.rpc("reserve_tickets", { p_ticket_type_id: ticket_type_id, p_quantity: quantity });
  if (error) {
    const status = /sold out|pending hold/.test(error.message) ? 409 : /not available/.test(error.message) ? 404 : 400;
    return { status, body: { error: error.message } };
  }

  const release = () => ctx.serviceClient.from("orders").update({ status: "expired" }).eq("id", hold.order_id).eq("status", "pending");
  const form = new URLSearchParams({
    mode: "payment",
    success_url,
    cancel_url,
    client_reference_id: String(hold.order_id),
    expires_at: String(Math.floor(Date.now() / 1000) + 31 * 60),
    "line_items[0][quantity]": String(quantity),
    "line_items[0][price_data][currency]": hold.currency,
    "line_items[0][price_data][unit_amount]": String(hold.unit_cents),
    "line_items[0][price_data][product_data][name]": hold.name,
  });
  let session;
  try {
    const res = await fetch("https://api.stripe.com/v1/checkout/sessions", {
      method: "POST",
      headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-www-form-urlencoded" },
      body: form,
      signal: AbortSignal.timeout(20000),
    });
    session = await res.json().catch(() => ({}));
    if (!res.ok || !session.id) {
      await release();
      return { status: 502, body: { error: session.error?.message ?? "Stripe error" } };
    }
  } catch {
    await release();
    return { status: 502, body: { error: "Could not reach Stripe, try again" } };
  }

  const { error: linkErr } = await ctx.serviceClient.from("orders").update({ stripe_session_id: session.id }).eq("id", hold.order_id);
  if (linkErr) {
    await release();
    await fetch(`https://api.stripe.com/v1/checkout/sessions/${session.id}/expire`, { method: "POST", headers: { Authorization: `Bearer ${key}` }, signal: AbortSignal.timeout(10000) }).catch(() => {});
    return { status: 500, body: { error: linkErr.message } };
  }
  return { status: 200, body: { url: session.url, order_id: hold.order_id } };
}
