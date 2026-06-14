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
      - operations: [insert, update, delete]
        check: "auth.uid() IS NOT NULL"

  documents:
    public: false
    max_size: 10MB
    rls:
      - operations: [select, insert, update, delete]
        check: "auth.uid() IS NOT NULL"
```

| Key | Type | Description |
|---|---|---|
| `public` | bool | When `true`, objects are downloadable without a JWT via `/storage/v1/object/public/<bucket>/<path>`. |
| `max_size` | string | Maximum object size. Accepts `KB`, `MB`, `GB` suffixes. Omit to allow any size. |
| `types` | list | Allowed MIME types. Wildcards supported (`image/*`). Omit to allow all types. |
| `rls` | list | RLS policies on `storage.objects`. Same syntax as table RLS. |

Buckets are managed exclusively through `instancez.yaml` — the migrator creates or updates them on boot.

## Using from a Supabase client

instancez implements the Supabase storage wire protocol. Any Supabase client library works — examples below use `@supabase/supabase-js`:

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

See the [Storage API reference](/api-reference/storage/) for the full endpoint listing.

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

## What's next

- [Storage API reference](/api-reference/storage/) — full endpoint listing with request and response shapes
