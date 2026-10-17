// Starts a Stripe Checkout session for a paid course the caller is not yet enrolled in.
const isHttpUrl = (v) => { try { return ["http:", "https:"].includes(new URL(v).protocol); } catch { return false; } };
export default async function handler(req, ctx) {
  const key = ctx.env.STRIPE_SECRET_KEY;
  if (!key) return { status: 503, body: { error: "Stripe is not configured. Add INSTANCEZ_ENV_STRIPE_SECRET_KEY in Secrets." } };

  const { course_id, success_url, cancel_url } = req.body ?? {};
  if (!Number.isInteger(course_id) || course_id <= 0) return { status: 400, body: { error: "course_id must be a positive integer" } };
  if (!isHttpUrl(success_url) || !isHttpUrl(cancel_url)) return { status: 400, body: { error: "success_url and cancel_url must be http(s) URLs" } };

  const userId = ctx.claims.sub;
  const { data: course, error } = await ctx.supabase
    .from("courses").select("id, title, price_cents, currency, published").eq("id", course_id).maybeSingle();
  if (error) return { status: 400, body: { error: error.message } };
  if (!course || !course.published) return { status: 404, body: { error: "Course not found" } };
  if (course.price_cents === 0) return { status: 400, body: { error: "Course is free, enroll directly" } };
  if (course.price_cents < 50) return { status: 400, body: { error: "Price is below Stripe's minimum charge" } };

  const { data: existing } = await ctx.supabase
    .from("enrollments").select("id").eq("course_id", course_id).eq("user_id", userId).maybeSingle();
  if (existing) return { status: 409, body: { error: "Already enrolled" } };

  const form = new URLSearchParams({
    mode: "payment",
    success_url,
    cancel_url,
    client_reference_id: userId,
    "metadata[course_id]": String(course.id),
    "metadata[user_id]": userId,
    "line_items[0][quantity]": "1",
    "line_items[0][price_data][currency]": course.currency,
    "line_items[0][price_data][unit_amount]": String(course.price_cents),
    "line_items[0][price_data][product_data][name]": course.title,
  });
  const res = await fetch("https://api.stripe.com/v1/checkout/sessions", {
    method: "POST",
    headers: {
      Authorization: `Bearer ${key}`,
      "Content-Type": "application/x-www-form-urlencoded",
      "Idempotency-Key": `course-${course.id}-${userId}-${Math.floor(Date.now() / 600000)}`,
    },
    body: form,
    signal: AbortSignal.timeout(20000),
  }).catch((e) => ({ ok: false, json: async () => ({ error: { message: e.message } }) }));
  const session = await res.json().catch(() => ({}));
  if (!res.ok) return { status: 502, body: { error: session.error?.message ?? "Stripe error" } };
  return { status: 200, body: { url: session.url } };
}
