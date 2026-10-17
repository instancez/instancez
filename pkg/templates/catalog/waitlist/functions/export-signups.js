// Admin CSV export. RLS only lets waitlist admins read signups, so non-admins get an empty 403.
const cell = (v) => {
  let s = v == null ? "" : String(v);
  if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`;
  return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
};

export default async function handler(req, ctx) {
  const { data: admin } = await ctx.supabase.from("waitlist_admins").select("user_id").eq("user_id", ctx.claims.sub).maybeSingle();
  if (!admin) return { status: 403, body: { error: "Admins only" } };

  const cols = ["id", "email", "status", "referral_code", "referral_count", "source", "created_at", "invited_at"];
  const lines = [cols.join(",")];
  for (let from = 0; ; from += 1000) {
    const { data, error } = await ctx.supabase.from("signups").select(cols.join(",")).order("id").range(from, from + 999);
    if (error) return { status: 500, body: { error: error.message } };
    for (const r of data ?? []) lines.push(cols.map((c) => cell(r[c])).join(","));
    if (!data || data.length < 1000) break;
  }
  return { status: 200, headers: { "Content-Type": "text/csv", "Content-Disposition": 'attachment; filename="waitlist.csv"' }, body: lines.join("\n") + "\n" };
}
