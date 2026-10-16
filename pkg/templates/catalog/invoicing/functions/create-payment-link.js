// Creates a Stripe Payment Link for one invoice's total and stores the URL on the invoice.
async function stripe(key, path, form) {
  const res = await fetch(`https://api.stripe.com/v1/${path}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams(form),
    signal: AbortSignal.timeout(20000),
  }).catch((e) => ({ ok: false, json: async () => ({ error: { message: e.message } }) }));
  const json = await res.json().catch(() => ({}));
  return res.ok ? { ok: json } : { error: json.error?.message ?? "Stripe error" };
}

export default async function handler(req, ctx) {
  const key = ctx.env.STRIPE_SECRET_KEY;
  if (!key) return { status: 503, body: { error: "Stripe is not configured. Add INSTANCEZ_ENV_STRIPE_SECRET_KEY in Secrets." } };

  const invoiceId = Number(req.body?.invoice_id);
  if (!Number.isSafeInteger(invoiceId) || invoiceId <= 0) return { status: 400, body: { error: "invoice_id must be a positive integer" } };

  // RLS limits this read to the caller's own invoices.
  const { data: inv, error } = await ctx.supabase
    .from("invoices").select("id, number, status, currency, total_cents, payment_link_url, clients(name)").eq("id", invoiceId).maybeSingle();
  if (error) return { status: 400, body: { error: error.message } };
  if (!inv) return { status: 404, body: { error: "Invoice not found" } };
  if (inv.status !== "sent") return { status: 409, body: { error: `Invoice is ${inv.status}; only sent invoices can get a payment link` } };
  if (inv.payment_link_url) return { status: 200, body: { url: inv.payment_link_url, reused: true } };
  if (!(inv.total_cents > 0) || inv.total_cents > 99999999) return { status: 400, body: { error: "Invoice total must be between 1 and 99999999. Save line items first." } };

  const product = await stripe(key, "products", {
    name: `Invoice ${inv.number}${inv.clients?.name ? ` for ${inv.clients.name}` : ""}`.slice(0, 250),
    "metadata[invoice_id]": String(inv.id),
  });
  if (product.error) return { status: 502, body: { error: product.error } };

  const link = await stripe(key, "payment_links", {
    "line_items[0][quantity]": "1",
    "line_items[0][price_data][currency]": inv.currency,
    "line_items[0][price_data][product]": product.ok.id,
    "line_items[0][price_data][unit_amount]": String(inv.total_cents),
    "metadata[invoice_id]": String(inv.id),
  });
  if (link.error) return { status: 502, body: { error: link.error } };

  // Only the first link wins; a concurrent loser deactivates its link and returns the stored one.
  const { data: saved, error: upErr } = await ctx.serviceClient.from("invoices")
    .update({ payment_link_url: link.ok.url, payment_link_id: link.ok.id })
    .eq("id", inv.id).eq("status", "sent").is("payment_link_id", null).select("id");
  if (upErr) return { status: 500, body: { error: upErr.message, url: link.ok.url } };
  if (!saved?.length) {
    await stripe(key, `payment_links/${encodeURIComponent(link.ok.id)}`, { active: "false" });
    const { data: cur } = await ctx.serviceClient.from("invoices").select("payment_link_url, status").eq("id", inv.id).maybeSingle();
    if (cur?.status !== "sent" || !cur.payment_link_url) return { status: 409, body: { error: "Invoice changed while creating the link. Try again." } };
    return { status: 200, body: { url: cur.payment_link_url, reused: true } };
  }
  return { status: 200, body: { url: link.ok.url } };
}
