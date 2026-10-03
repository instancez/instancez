---
title: Storage
description: File upload and download with local or S3 backends. Bucket policies enforced by RLS.
---

Buckets are declared in `instancez.yaml`. Objects are stored locally or in S3. Authorization is enforced by RLS policies using the same `auth.uid()` helpers available on your own tables.

## Declaring buckets

```yaml
storage:
  avatars:
    public: true
    max_size: 5MB
    types:
      - image/*
    rls:
      - operations: [select]
        using: "auth.uid() IS NOT NULL"
      - operations: [insert]
        with_check: "auth.uid() IS NOT NULL"
      - operations: [update]
        using: "auth.uid() IS NOT NULL"
        with_check: "auth.uid() IS NOT NULL"
      - operations: [delete]
        using: "auth.uid() IS NOT NULL"

  documents:
    public: false
    max_size: 10MB
    rls:
      - operations: [select, insert, update, delete]
        using: "auth.uid() IS NOT NULL"
        with_check: "auth.uid() IS NOT NULL"
```

| Key | Type | Description |
|---|---|---|
| `public` | bool | When `true`, anyone can download an object via `/storage/v1/object/public/<bucket>/<path>`; that route skips RLS, as in Supabase. It grants no `select`: listing, `exists`, `info`, signing, update and delete still follow `rls`. |
| `max_size` | string | Maximum object size. Accepts `KB`, `MB`, `GB` suffixes. Omit to use the default 50MB limit. |
| `types` | list | Allowed MIME types. Wildcards supported (`image/*`). Omit to allow all types. |
| `rls` | list | RLS policies on `storage.objects`. Same syntax as table RLS. |

Buckets are managed exclusively through `instancez.yaml` — the migrator creates or updates them on boot.

`storage.objects` is one table shared by every bucket, so a bucket's policies are scoped to `bucket_id` under the hood. A restrictive policy (`type: restrictive`, see [RLS](/instancez/build/rls/)) only narrows access within its own bucket — it never affects other buckets' rows.

## Using from a Supabase client

instancez exposes the same storage API as Supabase. Any Supabase client library works — examples below use `@supabase/supabase-js`:

```js
// Upload
await supabase.storage.from('avatars').upload('photo.png', file)
await supabase.storage.from('avatars').upload('photo.png', file, { upsert: true })

// Upload with user metadata, returned by info()
await supabase.storage.from('avatars').upload('photo.png', file, { metadata: { alt: 'me' } })

// Public URL (public buckets), optionally resized
const { data } = supabase.storage.from('avatars').getPublicUrl('photo.png')
const { data } = supabase.storage.from('avatars').getPublicUrl('photo.png', { transform: { width: 200 } })

// Signed URL (private buckets, expires in seconds)
const { data } = await supabase.storage.from('documents').createSignedUrl('report.pdf', 3600)
// Same, but the browser saves it as a file
const { data } = await supabase.storage.from('documents').createSignedUrl('report.pdf', 3600, { download: 'q3.pdf' })

// Signed URL for a resized image
const { data } = await supabase.storage.from('documents').createSignedUrl('scan.png', 3600, { transform: { width: 400 } })

// List one folder level, or page through every key
const { data } = await supabase.storage.from('avatars').list('', { limit: 100 })
const { data } = await supabase.storage.from('avatars').listV2({ prefix: 'users/', with_delimiter: true })

// Delete
await supabase.storage.from('avatars').remove(['photo.png'])
```

Uploading to an existing path without `upsert: true` returns a 409 error.

`list()` is index-bounded through a generated `name_lower` column that boot adds to `storage.objects`, with two indexes. The rewrite takes an exclusive lock, so each boot step runs with an 8s statement timeout and boot skips both when the table is over 128 MB or locked while boot reads its size (a warning includes the SQL). Tables under 128 MB can still time out on slow disks, so run the SQL when the warning appears. A concurrent first boot may wait for another instance's heal, bounded by the heal timeout. Without the column, `list()` still returns the same results. It seeks the index on the first letters of the prefix and reads every object under those letters, so cost follows the seeked letters, not the whole prefix; an empty or very short prefix on a large bucket is slow. On a large table, run this in a quiet window (drop an `INVALID` index first):

```sql
ALTER TABLE storage.objects ADD COLUMN name_lower TEXT COLLATE "C" GENERATED ALWAYS AS (lower(name)) STORED;
CREATE INDEX CONCURRENTLY IF NOT EXISTS objects_bucket_name_c_idx ON storage.objects (bucket_id, name COLLATE "C");
CREATE INDEX CONCURRENTLY IF NOT EXISTS objects_bucket_name_lower_c_idx ON storage.objects (bucket_id, name_lower, (name COLLATE "C"));
```

Signed URLs are authorized when they are created, not when they are redeemed. `createSignedUrl` checks the bucket's `select` policy before returning a download URL, and `createSignedUploadUrl` checks the `insert` policy before returning an upload token. If you cannot read or write an object directly, you cannot get a signed URL for it either. Redeeming the token needs no further auth (the token is the grant), so the check happens when the URL is minted.

`createSignedUrls` runs the same `select` check for each path, up to 1000 paths per call: an empty list or more than 1000 returns 400. Paths you can't read come back with an `error` and a null `signedURL`. Expiry is capped at 7 days (604800 seconds), matching the S3 presign limit. Larger values are clamped, and zero or negative values default to one hour.

A signed download URL looks like Supabase's: the API returns a relative `signedURL` of `/object/sign/<bucket>/<path>?token=...`, and supabase-js turns it into `<api url>/storage/v1/object/sign/...`. Opening it needs no apikey or JWT. The token is bound to that bucket, path and expiry, so it can't read any other object, and a signed upload token can't be used in its place. A tampered, expired or mismatched token returns 400 `invalid_token`; an object deleted since signing returns 404. The path match is case-sensitive; a trailing or doubled slash is cleaned first, so it still reads the signed object and nothing else. `download: true` sends `Content-Disposition: attachment`, and `download: 'name'` adds the filename, quoted or RFC 2231-encoded as needed so it can't inject headers. On S3, the URL answers with a 302 to a presigned S3 URL that lasts at most 60 seconds (never past the token's own expiry), so the bytes don't pass through instancez. On the local provider, instancez streams the object. Rotating the JWT signing key invalidates every outstanding signed download URL, the same as signed upload tokens. Callers get 400 and must request a new URL. `createSignedUrl(path, exp, { transform })` returns `/render/image/sign/...`; the transform is bound into the token, so changing `width`/`height` on the URL has no effect, and a transform token can't be redeemed at `/object/sign`. A signed render streams through instancez and sends `Expires` instead of `Cache-Control`.

`createSignedUploadUrl`'s response `url` includes `?token=`, which is where supabase-js reads it from. `upload`, `update` and signed uploads store storage-js `metadata` (multipart field or `x-metadata` header) as the object's user metadata. Every write replaces it, and a write without metadata stores `{}`, as in Supabase. Malformed metadata returns 400 `invalid_metadata`, and more than 1 MiB returns 413. `copy` carries it over. `bucket.info(path)` returns Supabase's shape: `metadata` is the user metadata, and `created_at`, `updated_at` and `last_modified` are ISO 8601. It is served at `/object/info/<bucket>/<path>` (also reachable as `/object/info/authenticated/<bucket>/<path>`); `info/authenticated/<bucket>` with no path returns 400. Bucket names `public`, `sign`, `authenticated`, `info`, `upload`, `list`, `move` and `copy` fail validation, since the storage router reads them as `/object/<segment>` routes.

### What each operation checks

A row must be visible under a `select` policy before `update` or `delete` can find it: Postgres checks the `WHERE` clause that locates a row against `select`, separately from the write's own policy. So most operations below need `select` plus the listed policy, not the listed policy alone. This applies to public buckets too: as in Supabase, `public: true` grants no `select` policy.

| Operation | Policy that must allow it |
|---|---|
| `remove`, `emptyBucket` | `select` and `delete` on each object. An object you can't see or delete is dropped from the batch silently, with no error and no count; its bytes are kept. `emptyBucket` isn't admin-only: it removes what your `select`+`delete` policies allow, as in Supabase. |
| `move` | `select` and `update` on the source row, with the destination passing `with_check`. **A policy that grants `update` but not `delete` still lets a caller move an object, which removes the source, same as a delete.** A hidden or missing source returns 404 (unlike `remove`, this fails loudly). Moving onto an existing object returns 409. |
| `copy` | `select` on the source and `insert` on the destination. Copying onto an existing destination object also needs `update` on that row, since copy always upserts. The caller owns the copy. |
| `update` (PUT) | `select` and `update` on the existing object. A hidden or missing object returns 404, like `move`. |

A request with only the publishable key runs as `anon` under the bucket's `rls:` policies, as in Supabase. That covers list, list-v2, info, exists, sign (single and batch), download, upload, update, `createSignedUploadUrl`, remove, move and copy. A policy that doesn't check `auth.uid()` (such as `using: "true"`) admits guests. A bucket without `rls:` gives `anon` nothing and never queries the database: `list()` returns `[]`, reads and signs return 404 (`createSignedUrls` gives each path an `error`), writes return 403, `remove` returns `[]`, and move or copy touching that bucket returns 404. `emptyBucket` and the bucket routes still need a user JWT. Keys are validated before the access check, so a malformed key returns 400 even where the object would be 404.

A project that declares no `rls:` on any bucket leaves `storage.objects` RLS off entirely: signed-in operations run on route-level auth alone, with no per-row check. Only the `public/` GET routes and signed-URL redemption (`GET /object/sign/...` and `/render/image/sign/...`, where the token is the grant) skip auth.

Object keys with a `..` segment, a NUL byte, or nothing at all return 400 on the single-object routes (upload, download, sign, info, move, copy). `remove` drops bad keys from its batch silently instead; `createSignedUrls` reports a per-path `error` instead of failing the whole call.

A direct upload (`upload`/`update`) checks RLS twice, in this order: the MIME check runs first, then an early probe (a short, always-rolled-back transaction) runs your `insert`/`update` policy with `size = 0` (the real size isn't known until the body is read), so a `with_check` policy that requires `size > 0` rejects every upload with 403. Only after the probe passes does the body spool to a temporary file, and the real write transaction runs last, re-checking RLS when it writes the metadata row. A signed upload (`uploadToSignedUrl`) checks RLS only once, when `createSignedUploadUrl` mints the token; redemption runs as `service_role` and does not check again, since the token itself is the grant. That mint-time probe uses `size = 0` and an empty content type, so a `with_check` policy bounding either `size` or `mime` rejects every `createSignedUploadUrl` call, the same gotcha as the direct-upload probe above.

The server needs a writable temp directory sized for concurrent uploads × `max_size` (on Lambda, that's `/tmp`). `uploadToSignedUrl` enforces the same bucket MIME allowlist (422) and `max_size` (413) as a normal upload, and returns 500 if the metadata write fails after the bytes are stored.

### Downloads

If an object's row exists but the local provider's file is gone, downloads return 404 `not_found`; other read errors return 500.

`HEAD /object/public/<bucket>/<path>` returns `Content-Type`, `Content-Length`, `Last-Modified` and `Cache-Control` (plus `ETag` when the object has one) without the body. `GET /object/info/public/<bucket>/<path>` returns the info JSON for a public bucket, skipping RLS like the public download. Both return 404 for a private or missing bucket, and `HEAD` on `info/public` is 404, as in Supabase.

For an object served by instancez (every `/object/...` route, including a signed URL on the local provider), the response sends `X-Content-Type-Options: nosniff`. Only the `public/` route sends `Cache-Control: public, max-age=3600`; every other route, including an authenticated download of a public bucket, sends `Cache-Control: private, max-age=3600`, since RLS on that route can still be per-caller. HTML, SVG, XML, JavaScript and `multipart/*` responses are sent with `Content-Disposition: attachment`, so an uploaded page can't run script on your API's origin. On S3, the presigned URL a signed URL redirects to asks S3 to send the same `Content-Type`, `Content-Disposition` (attachment for HTML/SVG/XML/JS/multipart, or whatever `download` asked for) and `Cache-Control` (private unless the bucket is public). The 302 itself carries `nosniff`, but S3 has no override for `X-Content-Type-Options`, so the S3 response the browser renders doesn't. Every download route also honors `?download` and `?download=<name>`, which is how `getPublicUrl(path, { download })` works.

### Image transformations

Served at `/object/public/...?width=`, `/render/image/public/...`, `/render/image/authenticated/...` (RLS) and `/render/image/sign/...`. `format` accepts `origin`, `png`, `jpeg`. A signed transform also rejects `quality` outside 20-100 and `resize` other than `cover`, `contain` or `fill` with 400 `invalid_transform`.

`width` and `height` accept 1-2500. Larger values are clamped, and negative values return 400. The source image must be 25MB or smaller and 50 megapixels or fewer, or the request returns 413.

## Storage providers

### Local (default)

```yaml
providers:
  storage:
    type: local
    path: ./uploads   # optional, defaults to ./uploads
```

### S3-compatible

Works with AWS S3, Cloudflare R2, MinIO, Tigris, and any S3-compatible service.

```yaml
providers:
  storage:
    type: s3
    bucket: "${MY_S3_BUCKET}"
    region: "${MY_S3_REGION}"
    access_key_id: "${MY_S3_ACCESS_KEY_ID}"
    secret_access_key: "${MY_S3_SECRET_ACCESS_KEY}"
    endpoint: ""   # optional: set for non-AWS endpoints (e.g. Cloudflare R2)
```

## Direct upload (serverless)

When using the S3 provider, you can upload files directly to S3 without routing bytes through instancez. Call `POST /storage/<bucket>/sign` to get a presigned upload URL, then `PUT` the file straight to S3:

```js
const { id, upload_url } = await fetch('/storage/avatars/sign', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${jwt}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({ content_type: file.type, size: file.size }),
}).then(r => r.json())

await fetch(upload_url, { method: 'PUT', headers: { 'Content-Type': file.type }, body: file })
```

Use `GET /storage/<bucket>/<id>` to get a presigned download URL later.

These endpoints run as the calling user, so the bucket's RLS policies apply: `insert` to sign an upload, `select` to sign a download (except on a public bucket, where anyone can sign a download, like `/object/public`), and `select` plus `delete` to delete (the `DELETE ... RETURNING` under RLS needs `select` to find the row, the same as `remove`). An object you can't see returns 404. A request with a present but invalid `Authorization: Bearer` token gets 401, even against a public bucket's download route: a bad token is always an error, not a silent fall-back to anonymous access.

## What's next

- [RLS](/instancez/build/rls/) — write the policies that gate `storage.objects` access
- [Functions](/instancez/build/functions/) — process uploads server-side with `ctx.serviceClient`

