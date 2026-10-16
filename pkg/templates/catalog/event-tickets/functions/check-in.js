// Staff-only: marks a ticket used. The conditional update guarantees a code works once.
export default async function handler(req, ctx) {
  const { code: rawCode, event_id } = req.body ?? {};
  const code = typeof rawCode === "string" ? rawCode.trim().toLowerCase() : "";
  if (code.length < 8 || code.length > 64) return { status: 400, body: { error: "code is required" } };
  if (event_id !== undefined && !Number.isSafeInteger(event_id)) return { status: 400, body: { error: "event_id must be an integer" } };

  const { data: me, error: meErr } = await ctx.supabase.from("profiles").select("role").eq("id", ctx.claims.sub).maybeSingle();
  if (meErr) return { status: 500, body: { error: meErr.message } };
  if (!me || !["staff", "admin"].includes(me.role)) return { status: 403, body: { error: "Staff only" } };

  const { data: ticket, error } = await ctx.serviceClient
    .from("tickets").select("id, status, used_at, ticket_types(name, event_id, events(title))").eq("code", code).maybeSingle();
  if (error) return { status: 500, body: { error: error.message } };
  if (!ticket) return { status: 404, body: { error: "Unknown ticket" } };
  const type = ticket.ticket_types;
  if (event_id !== undefined && type?.event_id !== event_id) {
    return { status: 409, body: { error: "Ticket is for a different event", event: type?.events?.title } };
  }
  if (ticket.status === "void") return { status: 409, body: { error: "Ticket was voided" } };

  const { data: used, error: useErr } = await ctx.serviceClient
    .from("tickets").update({ status: "used", used_at: new Date().toISOString(), used_by: ctx.claims.sub })
    .eq("id", ticket.id).eq("status", "valid").select("id").maybeSingle();
  if (useErr) return { status: 500, body: { error: useErr.message } };
  if (!used) return { status: 409, body: { error: "Already used", used_at: ticket.used_at } };

  return { status: 200, body: { ok: true, ticket_type: type?.name, event: type?.events?.title } };
}
