const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);

// Books a slot through the book_slot rpc (which prevents double booking), then emails a confirmation if Resend is configured.
export default async function handler(req, ctx) {
  const { service_id, starts_at, notes } = req.body ?? {};
  if (!Number.isInteger(service_id) || service_id < 1 || typeof starts_at !== "string" || Number.isNaN(Date.parse(starts_at))) {
    return { status: 400, body: { error: "service_id (positive integer) and starts_at (ISO timestamp) are required" } };
  }
  if (notes != null && (typeof notes !== "string" || notes.length > 1000)) {
    return { status: 400, body: { error: "notes must be a string of at most 1000 characters" } };
  }

  const { data: id, error } = await ctx.supabase.rpc("book_slot", {
    p_service_id: service_id,
    p_starts_at: new Date(starts_at).toISOString(),
    p_notes: typeof notes === "string" && notes.trim() ? notes.trim() : null,
  });
  if (error) {
    const status = { "23P01": 409, P0002: 404, "42501": 403, "53400": 429 }[error.code] ?? (/^(22|23)/.test(error.code ?? "") ? 400 : 500);
    return { status, body: { error: error.message } };
  }

  const { data: booking } = await ctx.supabase
    .from("bookings").select("id, starts_at, ends_at, status, services(name), providers(name, timezone)").eq("id", id).maybeSingle();

  const key = ctx.env.RESEND_API_KEY;
  const to = ctx.claims?.email;
  let emailed = false;
  if (key && to && booking) {
    const when = new Date(booking.starts_at).toLocaleString("en-US", { dateStyle: "full", timeStyle: "short", timeZone: booking.providers?.timezone ?? "UTC" });
    const res = await fetch("https://api.resend.com/emails", {
      method: "POST",
      signal: AbortSignal.timeout(8000),
      headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
      body: JSON.stringify({
        from: ctx.env.FROM_ADDRESS,
        to: [to],
        subject: `Booking confirmed: ${booking.services?.name ?? "appointment"}`,
        html: `<p>You are booked for <b>${esc(booking.services?.name ?? "an appointment")}</b> with ${esc(booking.providers?.name ?? "us")} on ${esc(when)} (${esc(booking.providers?.timezone ?? "UTC")}).</p>`,
      }),
    }).catch(() => null);
    emailed = !!res?.ok;
    if (!emailed) ctx.log.warn("confirmation email failed", { booking_id: id });
  }
  return { status: 200, body: { booking, emailed } };
}
