const page = (msg) => ({ status: 200, headers: { "content-type": "text/html; charset=utf-8" }, body: `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width"><body style="font-family:sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem"><p>${msg}</p>` });

const LINK_TTL_MS = 7 * 24 * 60 * 60 * 1000;

// Public: the link in the confirmation email. Flips a pending subscriber to confirmed within 7 days.
export default async function handler(req, ctx) {
  const token = String(req.query?.token ?? "");
  if (!/^[a-f0-9]{48}$/.test(token)) return { ...page("This confirmation link is invalid."), status: 400 };

  const { data, error } = await ctx.serviceClient
    .from("subscribers")
    .update({ status: "confirmed", confirmed_at: new Date().toISOString() })
    .eq("confirm_token", token).eq("status", "pending")
    .gt("confirm_sent_at", new Date(Date.now() - LINK_TTL_MS).toISOString())
    .select("id");
  if (error) return { ...page("Something went wrong. Try again later."), status: 500 };
  if (!data?.length) return page("You are already confirmed, or this link has expired.");
  return page("Thanks, you are subscribed.");
}
