const COUNTRY_HEADERS = ["cf-ipcountry", "cloudfront-viewer-country", "x-vercel-ip-country", "x-country-code"];

// Public: resolves a slug, logs the click, then returns a redirecting page or {url} with json=1.
export default async function handler(req, ctx) {
  const slug = String(req.query?.slug ?? "");
  if (!/^[a-zA-Z0-9_-]{3,64}$/.test(slug)) return { status: 400, body: { error: "A valid slug is required" } };

  const { data: link, error } = await ctx.serviceClient
    .from("links").select("id, target_url, active, expires_at").eq("slug", slug).maybeSingle();
  if (error) return { status: 500, body: { error: "Lookup failed" } };
  if (!link) return { status: 404, body: { error: "Link not found" } };
  if (!link.active || (link.expires_at && new Date(link.expires_at) <= new Date())) {
    return { status: 410, body: { error: "Link is no longer active" } };
  }

  const country = COUNTRY_HEADERS.map((h) => req.headers[h]).find(Boolean);
  const referrer = req.headers.referer ?? req.headers.referrer ?? null;
  const { error: clickErr } = await ctx.serviceClient.from("clicks").insert({
    link_id: link.id,
    referrer: referrer ? String(referrer).slice(0, 500) : null,
    country: country ? String(country).slice(0, 2).toUpperCase() : null,
  });
  if (clickErr) ctx.log.warn("click not recorded", { slug });

  if (["1", "true"].includes(req.query?.json)) return { status: 200, body: { url: link.target_url } };
  // The runtime follows 3xx itself, so a real 302 would 502: redirect from the page instead.
  const href = link.target_url.replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  const html = `<!doctype html><meta charset="utf-8"><meta name="referrer" content="no-referrer"><meta http-equiv="refresh" content="0;url=${href}"><a href="${href}">Continue</a>`;
  return { status: 200, headers: { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" }, body: html };
}
