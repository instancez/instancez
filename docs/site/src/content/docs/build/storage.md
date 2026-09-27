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

`createSignedUrls` runs the same `select` check for each path. Paths you can't read come back with an `error` and a null `signedURL`. Expiry is capped at 7 days (604800 seconds), which is the S3 presign limit. Larger values are clamped, and zero or negative values default to one hour.

`createSignedUploadUrl`'s response `url` includes `?token=`, which is where supabase-js reads it from. `bucket.info(path)` is served at `/object/info/<bucket>/<path>` (also reachable as `/object/info/authenticated/<bucket>/<path>`); `info/authenticated/<bucket>` with no path returns 400. A bucket literally named `public`, `authenticated` or `info` is shadowed on `GET` by these routes and can't be downloaded from directly — pick a different name.

### What each operation checks

| Operation | Policy that must allow it |
|---|---|
| `remove`, `emptyBucket` | `delete` on each object. Objects you can't delete are skipped and their bytes are kept. `emptyBucket` isn't admin-only: it removes what your `delete` policy allows, as in Supabase. |
| `move` | `update` on the source row, with the destination passing `with_check`. Moving onto an existing object returns 409. |
| `copy` | `select` on the source and `insert` on the destination. The caller owns the copy. |
| `update` (PUT) | `update` on the existing object. |

Uploads check RLS twice: once before the body is written to disk, then again when the metadata row is written. The first check runs your `insert`/`update` policy with `size = 0`, since the real size isn't known until the body is spooled — a `with_check` policy that requires `size > 0` rejects every upload with 403.

Object keys containing a `..` segment, a NUL byte, or nothing at all are rejected with 400.

### Downloads

Every download sends `X-Content-Type-Options: nosniff`. Objects in private buckets are served with `Cache-Control: private, max-age=3600`, so shared caches and CDNs don't keep them. HTML, SVG, XML and JavaScript files are sent with `Content-Disposition: attachment`, so an uploaded page can't run script on your API's origin.

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

These endpoints run as the calling user, so the bucket's RLS policies apply: `insert` to sign an upload, `select` to sign a download, `delete` to delete. An object you can't see returns 404.

## What's next

- [RLS](/instancez/build/rls/) — write the policies that gate `storage.objects` access
- [Functions](/instancez/build/functions/) — process uploads server-side with `ctx.serviceClient`

