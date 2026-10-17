import { randomBytes } from "node:crypto";

const EMAIL = /^[^@\s]+@[^@\s]+\.[^@\s]+$/;
const token = () => randomBytes(24).toString("hex");
const COOLDOWN_MS = 10 * 60 * 1000;
const HOURLY_CAP = 200;

// Public: stores a pending subscriber and emails a confirmation link. Never reveals whether an address exists.
export default async function handler(req, ctx) {
  const key = ctx.env.RESEND_API_KEY;
  if (!key) return { status: 503, body: { error: "Email is not configured. Add INSTANCEZ_ENV_RESEND_API_KEY in Secrets." } };

  const base = ctx.env.PUBLIC_BASE_URL?.replace(/\/+$/, "");
  if (!base || !URL.canParse(base)) return { status: 503, body: { error: "Links are not configured. Add INSTANCEZ_ENV_PUBLIC_BASE_URL in Secrets." } };

  const email = typeof req.body?.email === "string" ? req.body.email.trim().toLowerCase() : "";
  if (email.length > 254 || !EMAIL.test(email)) return { status: 400, body: { error: "A valid email is required" } };
  const source = typeof req.body?.source === "string" ? req.body.source.slice(0, 100) : null;

  const db = ctx.serviceClient;
  const hourAgo = new Date(Date.now() - 60 * 60 * 1000).toISOString();
  const { count, error: cerr } = await db.from("subscribers").select("id", { count: "exact", head: true }).gt("confirm_sent_at", hourAgo);
  if (cerr) return { status: 500, body: { error: "Could not subscribe" } };
  if (count >= HOURLY_CAP) return { status: 429, body: { error: "Too many signups right now, try again later" } };

  const { data: existing, error } = await db.from("subscribers").select("id, status").eq("email", email).maybeSingle();
  if (error) return { status: 500, body: { error: "Could not subscribe" } };
  const reply = { status: 200, body: { ok: true, message: "Check your inbox to confirm." } };
  if (existing?.status === "confirmed") return reply;

  const confirm_token = token();
  let id = existing?.id;
  if (existing) {
    const cutoff = new Date(Date.now() - COOLDOWN_MS).toISOString();
    const { data: rows, error: uerr } = await db.from("subscribers")
      .update({ status: "pending", confirm_token, unsubscribed_at: null, confirm_sent_at: new Date().toISOString() })
      .eq("id", existing.id).lt("confirm_sent_at", cutoff).select("id");
    if (uerr) return { status: 500, body: { error: "Could not subscribe" } };
    if (!rows?.length) return reply;
  } else {
    const { data: row, error: ierr } = await db.from("subscribers").insert({ email, confirm_token, unsubscribe_token: token(), source }).select("id").single();
    if (ierr?.code === "23505") return reply;
    if (ierr) return { status: 500, body: { error: "Could not subscribe" } };
    id = row.id;
  }

  const link = `${base}/functions/v1/confirm?token=${confirm_token}`;
  const res = await fetch("https://api.resend.com/emails", {
    method: "POST",
    signal: AbortSignal.timeout(10000),
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
    body: JSON.stringify({
      from: ctx.env.FROM_ADDRESS,
      to: [email],
      subject: "Confirm your subscription",
      html: `<p>Confirm your subscription by clicking <a href="${link}">this link</a>.</p><p>If you did not ask for this, ignore this email.</p>`,
    }),
  }).catch(() => null);
  if (!res?.ok) {
    await db.from("subscribers").update({ confirm_sent_at: new Date(0).toISOString() }).eq("id", id);
    return { status: 502, body: { error: "Could not send the confirmation email" } };
  }
  return reply;
}
