const page = (msg) => ({ status: 200, headers: { "content-type": "text/html; charset=utf-8" }, body: `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width"><body style="font-family:sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem"><p>${msg}</p>` });

// Public: GET from an email link, or POST (one-click List-Unsubscribe). Token only, no login.
export default async function handler(req, ctx) {
  const token = String(req.query?.token ?? req.body?.token ?? "");
  if (!/^[a-f0-9]{48}$/.test(token)) return { ...page("This unsubscribe link is invalid."), status: 400 };

  const { error } = await ctx.serviceClient
    .from("subscribers")
    .update({ status: "unsubscribed", unsubscribed_at: new Date().toISOString() })
    .eq("unsubscribe_token", token).neq("status", "unsubscribed");
  if (error) return { ...page("Something went wrong. Try again later."), status: 500 };
  return page("You have been unsubscribed.");
}
