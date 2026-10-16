// Public intake: the submit_ticket RPC validates and rate limits, then we email a confirmation.
async function sendMail(env, to, subject, text) {
  if (!env.RESEND_API_KEY || !env.MAIL_FROM) return "skipped: set INSTANCEZ_ENV_RESEND_API_KEY and INSTANCEZ_ENV_MAIL_FROM to email customers";
  try {
    const res = await fetch("https://api.resend.com/emails", {
      method: "POST",
      signal: AbortSignal.timeout(10000),
      headers: { Authorization: `Bearer ${env.RESEND_API_KEY}`, "Content-Type": "application/json" },
      body: JSON.stringify({ from: env.MAIL_FROM, to: [to], subject, text }),
    });
    return res.ok ? "sent" : `failed: Resend returned ${res.status}`;
  } catch (e) {
    return `failed: ${e.message}`;
  }
}

export default async function handler(req, ctx) {
  const b = req.body && typeof req.body === "object" ? req.body : {};
  const str = (v) => (typeof v === "string" ? v.trim() : "");
  const email = str(b.email).toLowerCase();
  const clean = (v, max) => str(v).replace(/[\p{Cc}\p{Cf}]+/gu, " ").trim().slice(0, max);
  const subject = clean(b.subject, 200);
  const body = str(b.body);
  if (!email || !subject || !body) return { status: 400, body: { error: "email, subject and body are required" } };

  const { data, error } = await ctx.supabase.rpc("submit_ticket", {
    p_email: email, p_name: clean(b.name, 120) || null, p_subject: subject, p_body: body,
  });
  if (error) {
    if (/too many/i.test(error.message)) return { status: 429, body: { error: error.message } };
    if (error.code === "P0001") return { status: 400, body: { error: error.message } };
    return { status: 500, body: { error: "Could not create the ticket" } };
  }
  const row = Array.isArray(data) ? data[0] : data;
  if (!row) return { status: 500, body: { error: "Ticket was not created" } };

  const mail = await sendMail(
    ctx.env, email, `We got your request: ${subject}`,
    `Hi${clean(b.name, 120) ? " " + clean(b.name, 120) : ""},\n\nWe received your request (#${row.ticket_id}) and will reply soon.\nYour ticket token: ${row.public_token}\n`,
  );
  return { status: 201, body: { ticket_id: row.ticket_id, token: row.public_token, email: mail } };
}
