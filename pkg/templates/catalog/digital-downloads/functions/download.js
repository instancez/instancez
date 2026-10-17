// Returns a 60-second signed download URL, only if the caller has a paid purchase.
export default async function handler(req, ctx) {
  const { product_id } = req.body ?? {};
  if (!Number.isSafeInteger(product_id) || product_id < 1) return { status: 400, body: { error: "product_id (integer) is required" } };

  const { data: purchases, error: purchaseErr } = await ctx.supabase
    .from("purchases").select("id").eq("product_id", product_id).eq("user_id", ctx.claims.sub).eq("status", "paid").limit(1);
  if (purchaseErr) return { status: 500, body: { error: purchaseErr.message } };
  const purchase = purchases?.[0];
  if (!purchase) return { status: 403, body: { error: "Purchase required" } };

  const { data: product, error: productErr } = await ctx.serviceClient
    .from("products").select("file_path, file_name").eq("id", product_id).maybeSingle();
  if (productErr) return { status: 500, body: { error: productErr.message } };
  if (!product?.file_path) return { status: 404, body: { error: "File not found" } };

  const { data, error } = await ctx.serviceClient.storage
    .from("product_files")
    .createSignedUrl(product.file_path, 60, product.file_name ? { download: product.file_name } : undefined);
  if (error || !data?.signedUrl) return { status: 500, body: { error: error?.message ?? "Could not sign URL" } };

  const { error: logErr } = await ctx.serviceClient.from("downloads").insert({ purchase_id: purchase.id });
  if (logErr) ctx.log.warn("download not logged", { error: logErr.message });
  return { status: 200, body: { url: data.signedUrl, expires_in: 60 } };
}
