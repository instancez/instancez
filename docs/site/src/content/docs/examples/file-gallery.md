---
title: File Gallery
description: Private image uploads, direct-to-S3 presigned URLs, and RLS on storage objects.
---

A private photo gallery where each user can upload images, list their own files, and get short-lived download links. Uploads go directly to S3 — the server never handles the bytes.

## Storage config

```yaml
# instancez.yaml
storage:
  photos:
    public: false
    max_size: 10MB
    types:
      - image/*
    rls:
      - operations: [select, insert, update, delete]
        check: "auth.uid() IS NOT NULL"

providers:
  storage:
    type: s3
    bucket: "${S3_BUCKET}"
    region: "${S3_REGION}"
    access_key_id: "${S3_ACCESS_KEY_ID}"
    secret_access_key: "${S3_SECRET_ACCESS_KEY}"
```

`public: false` means objects require a signed URL to download. The RLS policy requires a valid JWT for every operation.

## Upload directly to S3

Bypass the instancez server entirely for the file bytes — only the sign request goes through it:

```js
async function uploadPhoto(file, jwt) {
  // Step 1: get a presigned upload URL
  const { id, upload_url } = await fetch('/api/storage/photos/sign', {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${jwt}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ content_type: file.type, size: file.size }),
  }).then(r => r.json())

  // Step 2: PUT the file straight to S3
  await fetch(upload_url, {
    method: 'PUT',
    headers: { 'Content-Type': file.type },
    body: file,
  })

  return id  // store this to reference the object later
}
```

`id` is the object key assigned by instancez. Store it wherever you track your user's files (a `photos` table, for example).

## List files

```js
const { data: files } = await supabase
  .storage
  .from('photos')
  .list('', { limit: 50, sortBy: { column: 'created_at', order: 'desc' } })
```

## Download with a signed URL

```js
const { data } = await supabase
  .storage
  .from('photos')
  .createSignedUrl(fileName, 3600)  // expires in 1 hour

// data.signedUrl is a short-lived S3 URL — use it in <img src> or an anchor
```

## Delete

```js
await supabase.storage.from('photos').remove([fileName])
```

## Tracking metadata (optional)

If you need to store captions, tags, or ownership beyond what the bucket provides, add a `photos` table and write to it after a successful upload:

```yaml
tables:
  photos:
    columns:
      id:         { type: uuid, default: gen_random_uuid(), primary_key: true }
      user_id:    { type: uuid, nullable: false }
      object_key: { type: text, nullable: false }
      caption:    { type: text }
      created_at: { type: timestamptz, default: now() }
    rls:
      - operations: [select, insert, update, delete]
        check: "auth.uid() = user_id"
```

```js
await supabase
  .from('photos')
  .insert({ user_id: userId, object_key: id, caption: 'Sunset' })
```

## What to explore next

- Switch to `public: true` and use `.getPublicUrl()` to skip signed URLs for publicly shareable galleries
- Add `types: [video/*]` to the bucket to accept video uploads
- See [Storage](/instancez/build/storage/) for the full bucket and provider reference
