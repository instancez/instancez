import { createHash } from "node:crypto";

// Stable bucket 0-9999 per (flag, user), so a user keeps their result as the rollout grows.
function bucket(flagKey, userKey) {
  return createHash("sha256").update(`${flagKey}:${userKey}`).digest().readUInt32BE(0) % 10000;
}

function matches(rule, user) {
  const attr = rule?.attribute;
  const actual = typeof attr === "string" && Object.hasOwn(user, attr) ? user[attr] : undefined;
  if (actual === undefined || actual === null) return rule?.op === "neq" || rule?.op === "not_in";
  const v = rule.value;
  switch (rule.op) {
    case "eq": return String(actual) === String(v);
    case "neq": return String(actual) !== String(v);
    case "in": return Array.isArray(v) && v.map(String).includes(String(actual));
    case "not_in": return Array.isArray(v) && !v.map(String).includes(String(actual));
    case "contains": return String(actual).includes(String(v));
    case "gt": return Number(actual) > Number(v);
    case "lt": return Number(actual) < Number(v);
    default: return false;
  }
}

function evaluate(flag, user) {
  if (!flag.enabled) return false;
  const rules = Array.isArray(flag.rules) ? flag.rules : [];
  if (rules.some((r) => matches(r, user))) return true;
  const pct = Number(flag.rollout ?? 0);
  if (pct >= 100) return true;
  if (pct <= 0 || !user.key) return false;
  return bucket(flag.key, String(user.key)) < pct * 100;
}

export default async function handler(req, ctx) {
  const { client_key, user } = req.body ?? {};
  if (typeof client_key !== "string" || !client_key) return { status: 400, body: { error: "client_key is required" } };
  if (user !== undefined && (user === null || typeof user !== "object" || Array.isArray(user))) return { status: 400, body: { error: "user must be an object" } };

  const { data, error } = await ctx.supabase.rpc("flag_snapshot", { p_client_key: client_key });
  if (error) return { status: 500, body: { error: error.message } };
  if (!Array.isArray(data)) return { status: 404, body: { error: "Unknown client_key" } };

  const flags = Object.create(null);
  for (const f of data) flags[f.key] = evaluate({ ...f, enabled: f.enabled === true }, user ?? {});
  return { status: 200, body: { flags } };
}
