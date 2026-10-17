const BATCH = 100;
const PAGE = 1000;
const CLAIM_STALE_MS = 5 * 60 * 1000;

async function resend(key, payload, path = "emails") {
  const res = await fetch(`https://api.resend.com/${path}`, {
    method: "POST",
    signal: AbortSignal.timeout(20000),
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  }).catch((e) => ({ ok: false, status: e.name, json: async () => ({}) }));
  return res.ok ? null : ((await res.json().catch(() => ({}))).message ?? `Resend error ${res.status}`);
}

async function allRows(build) {
  const rows = [];
  for (let from = 0; ; from += PAGE) {
    const { data, error } = await build().range(from, from + PAGE - 1);
    if (error) throw new Error(error.message);
    rows.push(...data);
    if (data.length < PAGE) return rows;
  }
}

// Admin only: sends a draft issue to every confirmed subscriber who has not received it yet.
export default async function handler(req, ctx) {
  const key = ctx.env.RESEND_API_KEY;
  if (!key) return { status: 503, body: { error: "Email is not configured. Add INSTANCEZ_ENV_RESEND_API_KEY in Secrets." } };

  const { data: isAdmin } = await ctx.supabase.rpc("is_admin");
  if (isAdmin !== true) return { status: 403, body: { error: "Admins only" } };

  const { issue_id, test_to } = req.body ?? {};
  if (!Number.isInteger(issue_id)) return { status: 400, body: { error: "issue_id (integer) is required" } };
  const db = ctx.serviceClient;
  const { data: issue } = await db.from("issues").select("*").eq("id", issue_id).maybeSingle();
  if (!issue) return { status: 404, body: { error: "Issue not found" } };

  const from = ctx.env.FROM_ADDRESS;
  const base = ctx.env.PUBLIC_BASE_URL?.replace(/\/+$/, "");
  const esc = (t) => t.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);
  const preheader = issue.preview_text ? `<div style="display:none;max-height:0;overflow:hidden">${esc(issue.preview_text)}</div>` : "";
  const wrap = (token) => {
    const link = `${base}/functions/v1/unsubscribe?token=${token}`;
    return {
      html: `${preheader}${issue.body_html}<hr><p style="font-size:12px;color:#666"><a href="${link}">Unsubscribe</a></p>`,
      headers: { "List-Unsubscribe": `<${link}>`, "List-Unsubscribe-Post": "List-Unsubscribe=One-Click" },
    };
  };

  if (test_to) {
    if (typeof test_to !== "string" || test_to.length > 254 || !/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(test_to)) return { status: 400, body: { error: "test_to must be an email" } };
    const err = await resend(key, { from, to: [test_to], subject: `[Test] ${issue.subject}`, html: preheader + issue.body_html });
    return err ? { status: 502, body: { error: err } } : { status: 200, body: { test: true } };
  }
  if (issue.status === "sent") return { status: 409, body: { error: "Issue was already sent" } };
  if (!base || !URL.canParse(base)) return { status: 503, body: { error: "Links are not configured. Add INSTANCEZ_ENV_PUBLIC_BASE_URL in Secrets." } };

  // Claim the issue so concurrent calls cannot both send; a claim older than the function timeout is retaken.
  const stale = new Date(Date.now() - CLAIM_STALE_MS).toISOString();
  const { data: claimed, error: claimErr } = await db.from("issues")
    .update({ status: "sending", sent_at: new Date().toISOString() })
    .eq("id", issue.id).or(`status.eq.draft,and(status.eq.sending,sent_at.lt.${stale})`).select("id");
  if (claimErr) return { status: 500, body: { error: claimErr.message } };
  if (!claimed?.length) return { status: 409, body: { error: "Issue is already being sent or was sent" } };
  const release = () => db.from("issues").update({ status: "draft", sent_at: null }).eq("id", issue.id);

  let subs, done;
  try {
    subs = await allRows(() => db.from("subscribers").select("id, email, unsubscribe_token").eq("status", "confirmed").order("id"));
    done = new Set((await allRows(() => db.from("sends").select("subscriber_id").eq("issue_id", issue.id).order("subscriber_id"))).map((r) => r.subscriber_id));
  } catch (e) {
    await release();
    return { status: 500, body: { error: e.message } };
  }
  const todo = subs.filter((s) => !done.has(s.id));
  if (!todo.length && !done.size) {
    await release();
    return { status: 409, body: { error: "No confirmed subscribers to send to" } };
  }

  let sent = 0;
  for (let i = 0; i < todo.length; i += BATCH) {
    const chunk = todo.slice(i, i + BATCH);
    const err = await resend(key, chunk.map((s) => ({ from, to: [s.email], subject: issue.subject, ...wrap(s.unsubscribe_token) })), "emails/batch");
    if (err) {
      await release();
      return { status: 502, body: { error: err, sent_this_call: sent, remaining: todo.length - sent } };
    }
    const { error: sendErr } = await db.from("sends").insert(chunk.map((s) => ({ issue_id: issue.id, subscriber_id: s.id })));
    if (sendErr) {
      await release();
      return { status: 500, body: { error: `Sent but could not record: ${sendErr.message}`, sent_this_call: sent + chunk.length } };
    }
    sent += chunk.length;
  }

  const total = done.size + sent;
  const { error: doneErr } = await db.from("issues").update({ status: "sent", sent_at: new Date().toISOString(), recipient_count: total }).eq("id", issue.id);
  if (doneErr) return { status: 500, body: { error: doneErr.message, sent, total_recipients: total } };
  return { status: 200, body: { sent, total_recipients: total } };
}
