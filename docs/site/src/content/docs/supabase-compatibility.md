---
title: Supabase SDK Compatibility
description: Which Supabase client SDK features instancez supports.
---

instancez implements the Supabase wire protocol. Any official Supabase SDK works against it — with the gaps noted below.

## supabase-js feature matrix

| Feature | Status | Notes |
|---|---|---|
| **Database — `supabase.from()`** | ✅ Full | `select`, `insert`, `update`, `upsert`, `delete`. All PostgREST filter operators (`eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `like`, `ilike`, `is`, `in`, `contains`, `containedBy`, `overlaps`, …). Embeds (`!inner`, `!left`, FK hints). `order`, `limit`, `offset`, Range-header pagination. `Prefer: return`, `count`, `resolution`, `missing`, `max-affected`, `tx`. CSV responses (`Accept: text/csv`). HEAD requests. |
| **Auth — `supabase.auth.*`** | ✅ Full | Email + password, magic link / OTP, anonymous sign-in, session refresh, `updateUser`, `resetPasswordForEmail`, identity linking/unlinking, PKCE. |
| **OAuth — `signInWithOAuth`** | ⚠️ Google and GitHub only | The `provider` field accepts `google` and `github`. Other providers return a 422. |
| **Auth Admin — `supabase.auth.admin.*`** | ✅ Full | `createUser`, `listUsers` (paginated), `getUserById`, `updateUserById`, `deleteUser`, `inviteUserByEmail`, `generateLink`, `signOut` (user), `deleteFactor`. |
| **MFA — `supabase.auth.mfa.*`** | ⚠️ TOTP only | `enroll`, `challenge`, `verify`, `unenroll`, `listFactors` all work for TOTP. Phone/SMS factors are not supported. |
| **Storage — `supabase.storage.*`** | ✅ Full | Upload, download, move, copy, remove, list. Public URLs. Signed URLs (download and upload). Bucket management (create, update, delete, empty). Image transforms: resize (`cover`, `contain`, `fill`), quality, format (`jpeg`, `png`); WebP and AVIF output are not supported. |
| **Edge Functions — `supabase.functions.invoke()`** | ✅ Full | Calls code functions at `/functions/v1/<name>`. |
| **RPC — `supabase.rpc()`** | ✅ Full | Calls SQL functions declared under `rpc:` in `instancez.yaml`. |
| **Realtime — `supabase.channel()`** | ❌ Not supported | instancez has no pub/sub listener — this is architectural, not a roadmap item. For event-driven patterns, use a code function with Postgres LISTEN/NOTIFY or a webhook receiver. |

## Direct storage upload (no SDK needed)

When using the S3 provider, you can bypass the SDK entirely and upload files straight to S3 via a presigned URL — useful in serverless environments where routing bytes through the server is expensive:

```js
// Get a presigned upload URL
const { id, upload_url } = await fetch('/api/storage/avatars/sign', {
  method: 'POST',
  headers: { Authorization: `Bearer ${jwt}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({ content_type: file.type, size: file.size }),
}).then(r => r.json())

// Upload directly to S3 — instancez is not in this path
await fetch(upload_url, { method: 'PUT', headers: { 'Content-Type': file.type }, body: file })
```

See [Storage](/instancez/build/storage/) for the full spec.

The integration test suite runs `@supabase/supabase-js` against a live instancez instance on every commit. If you find a gap, [open an issue](https://github.com/instancez/instancez/issues).
