---
title: Storage
description: File upload and download with local or S3 backends. Bucket policies enforced by RLS.
---

instancez storage is wire-compatible with `@supabase/supabase-js` — the same storage client works unchanged.

Buckets are declared in `instancez.yaml`. The backend stores objects in a local directory or S3-compatible service. Authorization for object reads and writes is enforced by RLS policies on the `storage.objects` table, using the same `auth.uid()` helpers available on your own tables.

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
| `public` | bool | When `true`, objects can be downloaded without a JWT via `/storage/v1/object/public/<bucket>/<path>`. |
| `max_size` | string | Maximum object size. Accepts `KB`, `MB`, `GB` suffixes (e.g. `10MB`). Omit to allow any size. |
| `types` | list of strings | Allowed MIME types. Wildcards are supported (`image/*`). Omit to allow all types. |
| `rls` | list | RLS policies on `storage.objects`. Same syntax as table RLS. |

Buckets cannot be created or modified through the API — they are managed exclusively through `instancez.yaml`. The migrator creates or updates them on boot.

## Uploading

```js
import { createClient } from '@supabase/supabase-js'

const supabase = createClient('http://localhost:8080', 'YOUR_ANON_KEY')

const file = new File(['hello world'], 'hello.txt', { type: 'text/plain' })

const { data, error } = await supabase.storage
  .from('avatars')
  .upload('hello.txt', file)
```

To overwrite an existing object, pass `{ upsert: true }`:

```js
const { data, error } = await supabase.storage
  .from('avatars')
  .upload('hello.txt', file, { upsert: true })
```

Without `upsert: true`, uploading to a path that already exists returns a 409 error.

## Downloading

### Public buckets

Objects in buckets with `public: true` can be fetched without authentication. Use `getPublicUrl` to build the URL client-side:

```js
const { data } = supabase.storage
  .from('avatars')
  .getPublicUrl('hello.txt')

// data.publicUrl → http://localhost:8080/storage/v1/object/public/avatars/hello.txt
```

### Private buckets

For private buckets (or authenticated access to public ones), create a signed URL that expires after a given number of seconds:

```js
const { data, error } = await supabase.storage
  .from('documents')
  .createSignedUrl('report.pdf', 3600) // expires in 1 hour

// data.signedUrl → URL the browser can fetch without additional auth headers
```

To create signed URLs in bulk:

```js
const { data, error } = await supabase.storage
  .from('documents')
  .createSignedUrls(['report.pdf', 'invoice.pdf'], 3600)
```

## Listing objects

```js
const { data, error } = await supabase.storage
  .from('avatars')
  .list('', { limit: 100, offset: 0 })

// data → array of { name, id, ... }
```

To list objects under a subfolder prefix, pass the prefix as the first argument:

```js
const { data, error } = await supabase.storage
  .from('avatars')
  .list('subfolder/', { limit: 100 })
```

## Deleting

Remove one or more objects by path:

```js
const { data, error } = await supabase.storage
  .from('avatars')
  .remove(['hello.txt', 'subfolder/nested.txt'])
```

`data` is an array of the deleted object records.

## Signed upload URLs

For client-side uploads where you want to pre-authorize the upload server-side without sharing your service-role key, use a signed upload URL:

```js
// Server-side: generate a token
const { data, error } = await supabase.storage
  .from('avatars')
  .createSignedUploadUrl('user-photo.png')

// Client-side: upload to the signed URL (no auth headers needed)
const { data, error } = await supabase.storage
  .from('avatars')
  .uploadToSignedUrl('user-photo.png', data.token, file)
```

## Storage providers

### Local (default)

Files are stored on disk. Suitable for development and self-hosted deployments where a single-node setup is acceptable.

```yaml
providers:
  storage:
    type: local
    path: ./uploads   # optional, defaults to ./uploads
```

### S3-compatible

Any S3-compatible service works, including AWS S3, Cloudflare R2, MinIO, and Tigris.

```yaml
providers:
  storage:
    type: s3
    bucket: my-bucket-name
    region: us-east-1
    access_key_id: YOUR_ACCESS_KEY_ID
    secret_access_key: YOUR_SECRET_ACCESS_KEY
    endpoint: ""   # optional: set for non-AWS endpoints (e.g. Cloudflare R2)
```

Credentials can be injected via environment variables to avoid committing secrets:

```yaml
providers:
  storage:
    type: s3
    bucket: "${MY_S3_BUCKET}"
    region: "${MY_S3_REGION}"
    access_key_id: "${MY_S3_ACCESS_KEY_ID}"
    secret_access_key: "${MY_S3_SECRET_ACCESS_KEY}"
```

## What's next

- [Storage API reference](/reference/storage) — full endpoint listing with request and response shapes
