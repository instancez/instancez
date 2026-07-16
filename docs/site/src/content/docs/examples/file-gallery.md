---
title: File Gallery
description: Private image uploads, direct-to-S3 presigned URLs, and RLS on storage objects.
---

A private photo gallery where each user can upload images, list their own files, and get short-lived download links. Uploads go directly to S3, so the server never handles the bytes. A `photos` table tracks captions and ownership alongside the stored objects.

Want something you can run instead of read? [`docs/examples/gearstore`](https://github.com/instancez/instancez/tree/main/docs/examples/gearstore) is a full storefront project with `docker compose up --build`.

## instancez.yaml

The whole project lives in one file: the S3 provider, the bucket, and the metadata table.

```yaml
# instancez.yaml
version: 1

auth:
  jwt_expiry: 1h
  refresh_tokens: true
  allow_signup: true

providers:
  storage:
    type: s3
    bucket: "${S3_BUCKET}"
    region: "${S3_REGION}"
    access_key_id: "${S3_ACCESS_KEY_ID}"
    secret_access_key: "${S3_SECRET_ACCESS_KEY}"

storage:
  photos:
    public: false
    max_size: 10MB
    types:
      - image/*
    rls:
      - operations: [select, insert, update, delete]
        using: "uploaded_by = auth.uid()"
        with_check: "uploaded_by = auth.uid()"

tables:
  photos:
    fields:
      - name: id
        type: uuid
        default: uuid_v7()
        primary_key: true
      - name: user_id
        type: uuid
        required: true
      - name: object_key
        type: text
        required: true
      - name: caption
        type: text
      - name: created_at
        type: timestamptz
        default: now()
    rls:
      - operations: [select, insert, update, delete]
        using: "auth.uid() = user_id"
        with_check: "auth.uid() = user_id"
```

`public: false` means objects require a signed URL to download. The bucket policy scopes every operation to the uploader (`uploaded_by = auth.uid()`), and the `photos` table policy scopes each row to its owner the same way.

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

`id` is the object key assigned by instancez — this is what `.createSignedUrl()` and `.remove()` expect as the file path within the bucket. Store it wherever you track your user's files (a `photos` table, for example).

## List files

```js
const { data: files } = await supabase
  .storage
  .from('photos')
  .list('', { limit: 50 })
```

Results are ordered by name; `sortBy` isn't honored server-side yet, so sort client-side if you need a different order.

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

## Tracking metadata

The `photos` table in the config above stores captions and ownership beyond what the bucket itself tracks. Write a row after a successful upload, using the `id` returned by the sign step as the object key:

```js
await supabase
  .from('photos')
  .insert({ user_id: userId, object_key: id, caption: 'Sunset' })
```

## What to explore next

- Switch to `public: true` and use `.getPublicUrl()` to skip signed URLs for publicly shareable galleries
- Add `types: [video/*]` to the bucket to accept video uploads
- See [Storage](/instancez/build/storage/) for the full bucket and provider reference
