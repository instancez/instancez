---
title: Supabase SDK Compatibility
description: Which Supabase client SDK features instancez supports.
---

instancez implements the Supabase wire protocol. Any official Supabase SDK works against it — with the gaps noted below.

## supabase-js feature matrix

| Feature | Status | Notes |
|---|---|---|
| **Database — `supabase.from()`** | ✅ Full | `select`, `insert`, `update`, `upsert`, `delete`. All PostgREST filter operators (`eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `like`, `ilike`, `is`, `in`, `contains`, `containedBy`, `overlaps`, …). Embeds (`!inner`, `!left`, FK hints). `order`, `limit`, `offset`, Range-header pagination. `Prefer: return`, `count`, `resolution`, `missing`, `max-affected`, `tx`. CSV responses (`Accept: text/csv`). HEAD requests. |
| **Auth — `supabase.auth.*`** | ✅ Full | Email + password, magic link / OTP, anonymous sign-in, session refresh, `updateUser`, `resetPasswordForEmail`, identity linking/unlinking (`linkIdentity` needs the frontend on the API's origin, see [Auth](/build/auth/)), PKCE. |
| **OAuth — `signInWithOAuth`** | ⚠️ Google and GitHub only | The `provider` field accepts `google` and `github`. Other providers return a 400. |
| **Auth Admin — `supabase.auth.admin.*`** | ✅ Full | `createUser`, `listUsers` (paginated), `getUserById`, `updateUserById`, `deleteUser`, `inviteUserByEmail`, `generateLink`, `signOut` (user), `deleteFactor`. |
| **MFA — `supabase.auth.mfa.*`** | ⚠️ TOTP only | `enroll`, `challenge`, `verify`, `challengeAndVerify`, `unenroll`, `listFactors` and `getAuthenticatorAssuranceLevel` all work for TOTP, with Supabase's `aal`/`amr` JWT claims. Phone/SMS factors are not supported. |
| **Storage — `supabase.storage.*`** | ✅ Full | Upload, download, move, copy, remove, list. Public URLs. Signed URLs (download and upload). Bucket management (create, update, delete, empty). Image transforms: resize (`cover`, `contain`, `fill`), quality, format (`jpeg`, `png`); WebP and AVIF output are not supported. |
| **Edge Functions — `supabase.functions.invoke()`** | ✅ Full | Calls code functions at `/functions/v1/<name>`. |
| **RPC — `supabase.rpc()`** | ✅ Full | Calls SQL functions declared under `rpc:` in `instancez.yaml`. |
| **Realtime — `supabase.channel()`** | ❌ Not supported yet | instancez has no pub/sub listener. For event-driven patterns in the meantime, use a code function with Postgres LISTEN/NOTIFY or a webhook receiver. |

## Auth behavior differences from GoTrue

instancez's auth server matches Supabase's GoTrue on the wire, with a few deliberate differences:

- A banned user gets 403 `user_banned` on every path, including the password grant. GoTrue returns 400 there and 403 elsewhere.
- A TOTP code is rejected if its 30-second step was already used on that factor, even within the normal replay window GoTrue allows.
- Linking an OAuth identity to an account that has a password but isn't verified is refused (422 `email_exists`). GoTrue strips the password and links instead; on an instance with `verify_email: false`, every password user is unverified, so GoTrue's behavior would silently drop their password.
- `/otp` with `allow_signup: false` returns an empty 200 for an unknown address, not GoTrue's 422, so the response never reveals whether the account exists.
- `/resend` returns 429 `over_email_send_rate_limit` on a repeat within 60 seconds, matching GoTrue. The inconsistency is within instancez itself: `/otp` and `/recover` stay silent with an empty 200 on the same cooldown, so only `/resend` reveals whether the account exists.
- An access token issued before a ban or a password change stays valid until it expires; it isn't revoked early.
- JWTs are only accepted if signed `RS256` or `HS256`.

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
