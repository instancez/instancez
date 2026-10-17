const MAX_TEXT = 4000;
const MAX_KEYS = 200;

// Checks one answer against its question; returns an error string or null.
function check(q, v) {
  if (q.type === "text") return typeof v === "string" && v.length <= MAX_TEXT ? null : `must be text up to ${MAX_TEXT} characters`;
  if (q.type === "choice") return Array.isArray(q.options) && q.options.includes(v) ? null : "must be one of the options";
  if (q.type === "rating") return Number.isInteger(v) && v >= 1 && v <= q.max_rating ? null : `must be an integer from 1 to ${q.max_rating}`;
  return "unknown question type";
}

export default async function handler(req, ctx) {
  const { form_id, answers } = req.body ?? {};
  if (!Number.isInteger(form_id) || form_id <= 0) return { status: 400, body: { error: "form_id must be a positive integer" } };
  if (answers === null || typeof answers !== "object" || Array.isArray(answers)) return { status: 400, body: { error: "answers must be an object keyed by question id" } };

  if (Object.keys(answers).length > MAX_KEYS) return { status: 422, body: { error: `at most ${MAX_KEYS} answers` } };

  const { data: form } = await ctx.supabase.from("forms").select("id, status, closes_at").eq("id", form_id).maybeSingle();
  if (!form || form.status !== "open" || (form.closes_at && Date.parse(form.closes_at) <= Date.now())) return { status: 404, body: { error: "Form not found or closed" } };

  const { data: questions, error } = await ctx.supabase.from("questions").select("id, type, required, options, max_rating").eq("form_id", form_id);
  if (error) return { status: 500, body: { error: error.message } };

  const byId = new Map((questions ?? []).map((q) => [String(q.id), q]));
  const errors = {};
  for (const key of Object.keys(answers)) if (!byId.has(key)) errors[key] = "unknown question";
  const clean = {};
  for (const [id, q] of byId) {
    const raw = answers[id];
    const v = typeof raw === "string" ? raw.trim() : raw;
    if (v === undefined || v === null || v === "") {
      if (q.required) errors[id] = "required";
      continue;
    }
    const problem = check(q, v);
    if (problem) errors[id] = problem;
    else clean[id] = v;
  }
  if (Object.keys(errors).length) return { status: 422, body: { error: "Invalid answers", fields: errors } };
  if (Object.keys(clean).length === 0) return { status: 422, body: { error: "Submit at least one answer" } };

  const { error: insErr } = await ctx.serviceClient.from("responses").insert({ form_id, answers: clean });
  if (insErr) return { status: 500, body: { error: insErr.message } };
  return { status: 201, body: { ok: true } };
}
