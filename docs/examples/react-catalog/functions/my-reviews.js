/**
 * my-reviews — list and create the signed-in user's product reviews.
 *
 * Served at /functions/v1/my-reviews. auth_required: true, so ultrabase returns
 * 401 for anonymous callers before this runs.
 *
 * Demonstrates:
 *   - method branching (GET = list mine, POST = create)
 *   - query params (?limit=)
 *   - request-body validation with a 3rd-party npm dep (zod)
 *   - ctx.supabase: a supabase-js client carrying the CALLER's JWT, so the
 *     `reviews` RLS policies authorize every query as the caller. The INSERT
 *     policy is `user_id = auth.uid()`, so a forged user_id is rejected by
 *     Postgres regardless of what we send.
 *
 *   GET  /functions/v1/my-reviews?limit=20
 *   POST /functions/v1/my-reviews   { "product_id": 1, "author": "Alex", "rating": 5, "body": "Great!" }
 */
import { z } from "zod";

const NewReview = z.object({
  product_id: z.number().int(),
  author: z.string().min(1).max(80),
  rating: z.number().int().min(1).max(5),
  body: z.string().max(2000).optional(),
});

export default async function handler(req, ctx) {
  switch (req.method) {
    case "GET":
      return listMine(req, ctx);
    case "POST":
      return create(req, ctx);
    default:
      return { status: 405, body: { error: "method not allowed" } };
  }
}

async function listMine(req, ctx) {
  const limit = Math.min(Number(req.query.limit ?? "20") || 20, 100);

  // `reviews` SELECT is public, so we scope to the caller explicitly.
  const { data, error } = await ctx.supabase
    .from("reviews")
    .select("id, product_id, rating, body, created_at")
    .eq("user_id", ctx.claims.sub)
    .order("created_at", { ascending: false })
    .limit(limit);

  if (error) return { status: 400, body: { error: error.message } };
  return { status: 200, body: { reviews: data } };
}

async function create(req, ctx) {
  const parsed = NewReview.safeParse(req.body);
  if (!parsed.success) {
    return {
      status: 400,
      body: { error: "invalid body", issues: parsed.error.issues },
    };
  }

  // Stamp the row with the caller's id. The INSERT policy (user_id = auth.uid())
  // also enforces this, so it can't be spoofed.
  const row = { ...parsed.data, user_id: ctx.claims.sub };

  const { data, error } = await ctx.supabase
    .from("reviews")
    .insert(row)
    .select()
    .single();

  if (error) {
    ctx.log.warn("review insert rejected", { error: error.message });
    return { status: 400, body: { error: error.message } };
  }

  ctx.log.info("review created", { id: data.id, user: ctx.claims.sub });
  return {
    status: 201,
    headers: { "content-type": "application/json" },
    body: { review: data },
  };
}
