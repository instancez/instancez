// Public join: the join_waitlist RPC validates, dedupes and ranks; we then send a welcome email once.
export default async function handler(req, ctx) {
  const b = req.body && typeof req.body === "object" ? req.body : {};
  const str = (v) => (typeof v === "string" ? v.trim() : "");
  const email = str(b.email).toLowerCase();
  if (!email) return { status: 400, body: { error: "email is required" } };

  const ip = String(req.headers["x-forwarded-for"] ?? req.headers["x-real-ip"] ?? "").split(",")[0].trim();
  const { data, error } = await ctx.serviceClient.rpc("join_waitlist", {
    p_email: email, p_ref: str(b.ref) || null, p_source: str(b.source) || null, p_ip: ip || null,
  });
  if (error) {
    if (/too many/i.test(error.message)) return { status: 429, body: { error: error.message } };
    if (error.code === "P0001") return { status: 400, body: { error: error.message } };
    return { status: 500, body: { error: "Signup failed" } };
  }
  const row = Array.isArray(data) ? data[0] : data;
  if (!row) return { status: 500, body: { error: "Signup failed" } };

  let mail = "skipped";
  const { RESEND_API_KEY, MAIL_FROM, LANDING_URL } = ctx.env;
  if (!row.is_new) mail = "skipped: already on the list";
  else if (!RESEND_API_KEY || !MAIL_FROM) mail = "skipped: set INSTANCEZ_ENV_RESEND_API_KEY and INSTANCEZ_ENV_MAIL_FROM to send email";
  else {
    let link = "";
    try {
      const u = new URL(LANDING_URL);
      if (["http:", "https:"].includes(u.protocol)) { u.searchParams.set("ref", row.referral_code); link = `\nShare your link to move up: ${u}\n`; }
    } catch { /* no landing URL, send the code only */ }
    try {
      const res = await fetch("https://api.resend.com/emails", {
        method: "POST",
        signal: AbortSignal.timeout(10000),
        headers: { Authorization: `Bearer ${RESEND_API_KEY}`, "Content-Type": "application/json" },
        body: JSON.stringify({
          from: MAIL_FROM, to: [email], subject: "You're on the list",
          text: `You are number ${row.queue_position} of ${row.queue_total}.\nYour referral code: ${row.referral_code}\n${link}`,
        }),
      });
      mail = res.ok ? "sent" : `failed: Resend returned ${res.status}`;
    } catch (e) {
      mail = `failed: ${e.message}`;
    }
  }
  return { status: row.is_new ? 201 : 200, body: { referral_code: row.referral_code, position: row.queue_position, total: row.queue_total, is_new: row.is_new, email: mail } };
}
