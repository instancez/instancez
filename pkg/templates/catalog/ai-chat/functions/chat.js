const HISTORY_LIMIT = 40;
const MAX_MESSAGE = 8000;
const SYSTEM = "You are a helpful, concise assistant.";

async function refund(ctx) {
  const { error } = await ctx.supabase.rpc("refund_message_quota");
  if (error) console.error("refund failed", error.message);
}

export default async function handler(req, ctx) {
  const apiKey = ctx.env.ANTHROPIC_API_KEY;
  if (!apiKey) return { status: 503, body: { error: "Chat is not configured. Add INSTANCEZ_ENV_ANTHROPIC_API_KEY in Secrets." } };

  const userId = ctx.claims.sub;
  const message = typeof req.body?.message === "string" ? req.body.message.trim() : "";
  let conversationId = req.body?.conversation_id ?? null;
  if (!message) return { status: 400, body: { error: "message is required" } };
  if (message.length > MAX_MESSAGE) return { status: 400, body: { error: `message must be at most ${MAX_MESSAGE} characters` } };
  if (conversationId !== null && !(Number.isSafeInteger(conversationId) && conversationId > 0)) return { status: 400, body: { error: "conversation_id must be a positive integer" } };

  let history = [];
  if (conversationId !== null) {
    const { data: convo } = await ctx.supabase.from("conversations").select("id").eq("id", conversationId).maybeSingle();
    if (!convo) return { status: 404, body: { error: "Conversation not found" } };
    const { data: rows } = await ctx.supabase.from("messages").select("role, content").eq("conversation_id", conversationId).order("id", { ascending: false }).limit(HISTORY_LIMIT);
    history = (rows ?? []).reverse();
    while (history.length && history[0].role !== "user") history.shift();
  }

  const { data: quota, error: quotaErr } = await ctx.supabase.rpc("consume_message_quota");
  if (quotaErr) return { status: 500, body: { error: quotaErr.message } };
  if (!quota.allowed) return { status: 429, body: { error: "Daily message limit reached", cap: quota.cap } };

  const signal = ctx.signal ? AbortSignal.any([ctx.signal, AbortSignal.timeout(45000)]) : AbortSignal.timeout(45000);
  let reply;
  try {
    const res = await fetch("https://api.anthropic.com/v1/messages", {
      method: "POST",
      headers: { "x-api-key": apiKey, "anthropic-version": "2023-06-01", "content-type": "application/json" },
      body: JSON.stringify({
        model: ctx.env.ANTHROPIC_MODEL || "claude-haiku-4-5-20251001",
        max_tokens: 1024,
        system: SYSTEM,
        messages: [...history, { role: "user", content: message }],
      }),
      signal,
    });
    const json = await res.json().catch(() => ({}));
    if (!res.ok) {
      await refund(ctx);
      return { status: res.status === 429 ? 429 : 502, body: { error: json.error?.message ?? `Anthropic API error (${res.status})` } };
    }
    reply = (json.content ?? []).filter((b) => b.type === "text").map((b) => b.text).join("").trim();
    if (!reply) {
      await refund(ctx);
      return { status: 502, body: { error: "Empty reply from model" } };
    }
  } catch (e) {
    await refund(ctx);
    const timedOut = e?.name === "TimeoutError" || e?.name === "AbortError";
    return { status: timedOut ? 504 : 502, body: { error: timedOut ? "Model request timed out" : "Could not reach the Anthropic API" } };
  }

  if (conversationId === null) {
    const { data: convo, error } = await ctx.supabase.from("conversations").insert({ user_id: userId, title: Array.from(message).slice(0, 60).join("") }).select("id").single();
    if (error) console.error("could not create conversation", error.message);
    conversationId = convo?.id ?? null;
  } else {
    await ctx.supabase.from("conversations").update({ updated_at: new Date().toISOString() }).eq("id", conversationId);
  }
  if (conversationId !== null) {
    const { error: msgErr } = await ctx.serviceClient.from("messages").insert([
      { conversation_id: conversationId, user_id: userId, role: "user", content: message },
      { conversation_id: conversationId, user_id: userId, role: "assistant", content: reply },
    ]);
    if (msgErr) console.error("could not store messages", msgErr.message);
  }
  return { status: 200, body: { conversation_id: conversationId, reply, used: quota.used, cap: quota.cap } };
}
