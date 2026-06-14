---
title: Client Compatibility
description: Which supabase-js (and other Supabase client) features instancez supports.
---

instancez implements the Supabase wire protocol, so any Supabase client library works out of the box — with one exception.

## supabase-js

| Feature | Status | Notes |
|---|---|---|
| **Database — `supabase.from()`** | ✅ Full | Select, insert, update, upsert, delete. All PostgREST operators (`eq`, `gt`, `like`, `in`, `contains`, `order`, `limit`, `range`, embeds, `Prefer: return=…`, CSV, count). |
| **Auth — `supabase.auth.*`** | ✅ Full | Email + password, magic link, OTP, OAuth (Google, GitHub), anonymous sign-in, `updateUser`, identity linking, session refresh. |
| **Auth Admin — `supabase.auth.admin.*`** | ✅ Full | `createUser`, `listUsers`, `getUserById`, `updateUserById`, `deleteUser`, `signOut`, `generateLink`, `inviteUserByEmail`, `deleteFactor`. |
| **MFA — `supabase.auth.mfa.*`** | ✅ Full | TOTP enroll, challenge, verify, unenroll, listFactors. |
| **Storage — `supabase.storage.*`** | ✅ Full | Upload, download, move, copy, remove, list. Public URLs, signed URLs, signed upload URLs. Bucket management. |
| **Edge Functions — `supabase.functions.invoke()`** | ✅ Full | Calls code functions at `/functions/v1/<name>`. |
| **RPC — `supabase.rpc()`** | ✅ Full | Calls SQL functions declared under `rpc:` in `instancez.yaml`. |
| **Realtime — `supabase.channel()`** | ❌ Not supported | instancez has no pub/sub listener. There is no plan to add one — use Postgres LISTEN/NOTIFY via a code function for event-driven patterns instead. |

## Other client libraries

All other official Supabase clients implement the same wire protocol and carry the same support matrix above — the Realtime SDK surface is the only gap.

| Client | Language |
|---|---|
| [`supabase-py`](https://github.com/supabase/supabase-py) | Python |
| [`supabase-swift`](https://github.com/supabase/supabase-swift) | Swift / iOS / macOS |
| [`supabase-flutter`](https://github.com/supabase/supabase-flutter) | Flutter / Dart |
| [`supabase-kt`](https://github.com/supabase-community/supabase-kt) | Kotlin / Android |
| [`@supabase/ssr`](https://github.com/supabase/ssr) | SSR frameworks (Next.js, SvelteKit, etc.) |

The integration test suite runs `@supabase/supabase-js` against a real instancez instance on every commit. Other clients are tested by community contributors — if you find a gap, [open an issue](https://github.com/instancez/instancez/issues).
