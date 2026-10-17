// Agent reply or internal note. A public reply stamps first_responded_at and emails the customer.
export default async function handler(req, ctx) {
  const b = req.body && typeof req.body === "object" ? req.body : {};
  const ticketId = b.ticket_id;
  const text = typeof b.body === "string" ? b.body.trim() : "";
  const internal = b.internal === true;
  if (!Number.isInteger(ticketId) || ticketId <= 0) return { status: 400, body: { error: "ticket_id must be a positive integer" } };
  if (!text || text.length > 10000) return { status: 400, body: { error: "body must be 1-10000 characters" } };

  const { data: ticket, error: tErr } = await ctx.supabase
    .from("tickets").select("id, subject, customer_email, status, first_responded_at").eq("id", ticketId).maybeSingle();
  if (tErr) return { status: 400, body: { error: tErr.message } };
  if (!ticket) return { status: 404, body: { error: "Ticket not found, or you are not an agent" } };

  const { error } = await ctx.supabase.from("ticket_messages").insert({
    ticket_id: ticketId, kind: internal ? "note" : "reply", author_id: ctx.claims.sub, body: text,
  });
  if (error) return { status: 403, body: { error: error.message } };
  if (internal) return { status: 200, body: { ok: true, email: "not sent: internal note" } };

  const patch = {};
  if (ticket.status === "open") patch.status = "pending";
  if (!ticket.first_responded_at) patch.first_responded_at = new Date().toISOString();
  if (Object.keys(patch).length) {
    const { error: uErr } = await ctx.supabase.from("tickets").update(patch).eq("id", ticketId);
    if (uErr) return { status: 500, body: { error: `Reply saved but the ticket was not updated: ${uErr.message}` } };
  }

  const { RESEND_API_KEY, MAIL_FROM } = ctx.env;
  if (!RESEND_API_KEY || !MAIL_FROM) {
    return { status: 200, body: { ok: true, email: "skipped: set INSTANCEZ_ENV_RESEND_API_KEY and INSTANCEZ_ENV_MAIL_FROM to email customers" } };
  }
  try {
    const res = await fetch("https://api.resend.com/emails", {
      method: "POST",
      signal: AbortSignal.timeout(10000),
      headers: { Authorization: `Bearer ${RESEND_API_KEY}`, "Content-Type": "application/json" },
      body: JSON.stringify({ from: MAIL_FROM, to: [ticket.customer_email], subject: `Re: ${ticket.subject.replace(/[\p{Cc}\p{Cf}]+/gu, " ")}`, text: `${text}\n\nTicket #${ticket.id}` }),
    });
    return { status: 200, body: { ok: true, email: res.ok ? "sent" : `failed: Resend returned ${res.status}` } };
  } catch (e) {
    return { status: 200, body: { ok: true, email: `failed: ${e.message}` } };
  }
}
