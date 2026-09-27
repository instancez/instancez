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
| `public` | bool | When `true`, objects are downloadable without a JWT via `/storage/v1/object/public/<bucket>/<path>`. |
| `max_size` | string | Maximum object size. Accepts `KB`, `MB`, `GB` suffixes. Omit to use the default 50MB limit. |
| `types` | list | Allowed MIME types. Wildcards supported (`image/*`). Omit to allow all types. |
| `rls` | list | RLS policies on `storage.objects`. Same syntax as table RLS. |

Buckets are managed exclusively through `instancez.yaml` — the migrator creates or updates them on boot.

## Using from a Supabase client

instancez exposes the same storage API as Supabase. Any Supabase client library works — examples below use `@supabase/supabase-js`:

```js
// Upload
await supabase.storage.from('avatars').upload('photo.png', file)
await supabase.storage.from('avatars').upload('photo.png', file, { upsert: true })

// Public URL (public buckets)
const { data } = supabase.storage.from('avatars').getPublicUrl('photo.png')

// Signed URL (private buckets, expires in seconds)
const { data } = await supabase.storage.from('documents').createSignedUrl('report.pdf', 3600)

// List
const { data } = await supabase.storage.from('avatars').list('', { limit: 100 })

// Delete
await supabase.storage.from('avatars').remove(['photo.png'])
```

Uploading to an existing path without `upsert: true` returns a 409 error.

Signed URLs are authorized when they are created, not when they are redeemed. `createSignedUrl` checks the bucket's `select` policy before returning a download URL, and `createSignedUploadUrl` checks the `insert` policy before returning an upload token. If you cannot read or write an object directly, you cannot get a signed URL for it either. Redeeming the token needs no further auth (the token is the grant), so the check happens when the URL is minted.

`createSignedUrls` runs the same `select` check for each path, up to 1000 paths per call — an empty list or more than 1000 returns 400. Paths you can't read come back with an `error` and a null `signedURL`. Expiry is capped at 7 days (604800 seconds), which is the S3 presign limit. Larger values are clamped, and zero or negative values default to one hour.

`createSignedUploadUrl`'s response `url` includes `?token=`, which is where supabase-js reads it from. `bucket.info(path)` is served at `/object/info/<bucket>/<path>` (also reachable as `/object/info/authenticated/<bucket>/<path>`); `info/authenticated/<bucket>` with no path returns 400. A bucket literally named `public`, `authenticated` or `info` can't be reached through `.download()` or `.exists()` (the bare `/object/<bucket>/<path>` route reads the name as a route marker instead) — `.getPublicUrl()` and `.info()` are unaffected. Pick a different name to avoid the ambiguity.

### What each operation checks

Postgres requires a `select` policy to find a row at all — an `update` or `delete` policy alone can grant the write but still match zero rows, because the `WHERE` clause that locates the row has nothing to read it with. So most operations below need `select` plus the operation-specific policy, not the specific policy alone.

| Operation | Policy that must allow it |
|---|---|
| `remove`, `emptyBucket` | `select` (to find the row) and `delete` on each object. Objects you can't delete are skipped and their bytes are kept. `emptyBucket` isn't admin-only: it removes what your `select`+`delete` policies allow, as in Supabase. |
| `move` | `select` and `update` on the source row, with the destination passing `with_check`. **A policy that grants `update` but not `delete` still lets a caller move an object — which removes the source, same as a delete.** Moving onto an existing object returns 409. |
| `copy` | `select` on the source and `insert` on the destination. Copying onto an existing destination object also needs `update` on that row (copy always upserts). The caller owns the copy. |
| `update` (PUT) | `select` and `update` on the existing object. |

A `public: true` bucket gets an implicit unconditional `select` policy, so the operations above work there even without declaring `select` explicitly in `rls:`. A bucket that is not public and declares only `insert`/`update`/`delete` (no `select`) will see remove/move/update silently match nothing.

Uploads check RLS twice: once before the body is written to disk, then again when the metadata row is written. The first check runs your `insert`/`update` policy with `size = 0`, since the real size isn't known until the body is spooled — a `with_check` policy that requires `size > 0` rejects every upload with 403.

Object keys containing a `..` segment, a NUL byte, or nothing at all are rejected with 400.

Uploads spool to a temporary file before touching the database, so the server needs a writable temp directory sized for concurrent uploads × `max_size` (on Lambda, that's `/tmp`). `uploadToSignedUrl` enforces the same bucket MIME allowlist (422) and `max_size` (413) as a normal upload, and returns 500 if the metadata write fails after the bytes are stored.

### Downloads

Every download sends `X-Content-Type-Options: nosniff`. Only the `public/` route sends `Cache-Control: public, max-age=3600`; every other route — including an authenticated download of a public bucket — sends `Cache-Control: private, max-age=3600`, since RLS on that route can still be per-caller. HTML, SVG, XML, JavaScript and `multipart/*` responses are sent with `Content-Disposition: attachment`, so an uploaded page can't run script on your API's origin.

### Image transformations

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

When using the S3 provider, you can upload files directly to S3 without routing bytes through instancez. Call `POST /api/storage/<bucket>/sign` to get a presigned upload URL, then `PUT` the file straight to S3:

```js
const { id, upload_url } = await fetch('/api/storage/avatars/sign', {
  method: 'POST',
  headers: { 'Authorization': `Bearer ${jwt}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({ content_type: file.type, size: file.size }),
}).then(r => r.json())

await fetch(upload_url, { method: 'PUT', headers: { 'Content-Type': file.type }, body: file })
```

Use `GET /api/storage/<bucket>/<id>` to get a presigned download URL later.

These endpoints run as the calling user, so the bucket's RLS policies apply: `insert` to sign an upload, `select` to sign a download, `delete` to delete. An object you can't see returns 404. A request with a present but invalid `Authorization: Bearer` token gets 401, even against a public bucket's download route — a bad token is always an error, not a silent fall-back to anonymous access.

## What's next

- [RLS](/instancez/build/rls/) — write the policies that gate `storage.objects` access
- [Functions](/instancez/build/functions/) — process uploads server-side with `ctx.serviceClient`

