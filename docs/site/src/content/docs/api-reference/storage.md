---
title: Storage API
description: /storage/v1/* endpoint reference.
---

Storage buckets are declared in `instancez.yaml` under `storage:`. Runtime create/update/delete of buckets is not supported — those endpoints return `400 not_supported`.

All endpoints except public downloads and signed-URL uploads require `Authorization: Bearer <jwt>` (or the admin `apikey` header).

## Bucket endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/storage/v1/bucket` | List all configured buckets. |
| `GET` | `/storage/v1/bucket/:id` | Get a single bucket by ID. |
| `POST` | `/storage/v1/bucket` | Not supported. Returns `400 not_supported`. |
| `PUT` | `/storage/v1/bucket/:id` | Not supported. Returns `400 not_supported`. |
| `DELETE` | `/storage/v1/bucket/:id` | Not supported. Returns `400 not_supported`. |
| `POST` | `/storage/v1/bucket/:id/empty` | Delete all objects in the bucket. |

### GET /bucket

Response: array of bucket objects.

```json
[
  {
    "id": "avatars",
    "name": "avatars",
    "public": false,
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z"
  }
]
```

### POST /bucket/:id/empty

Deletes all objects from the bucket. The bucket itself remains.

Response `200`:
```json
{ "message": "Successfully emptied" }
```

## Object upload

### POST /object/:bucket/*path — upload (create)

Upload a new object. Fails with `409` if the object already exists (use `x-upsert: true` to overwrite).

| Header | Description |
|--------|-------------|
| `Authorization: Bearer <jwt>` | Required. |
| `Content-Type` | MIME type of the file. Validated against `storage.<bucket>.types` if set. |
| `x-upsert: true` | Optional. Insert or replace on conflict. |

Request body: raw file bytes, or `multipart/form-data` with the file in any named field.

Response `200`:
```json
{ "Key": "avatars/profiles/alice.png", "Id": "profiles/alice.png" }
```

| Status | Cause |
|--------|-------|
| `200` | Uploaded. |
| `403` | RLS policy denied the write. |
| `409` | Object already exists and `x-upsert` was not set. |
| `413` | File exceeds the bucket's `max_size`. |
| `422` | MIME type not in bucket's `types` allowlist. |

### PUT /object/:bucket/*path — update

Replace an existing object's bytes. Fails with `404` if the object does not exist (or RLS hides it).

Same headers and body shape as POST. Returns the same `200` shape on success.

### PUT /object/upload/sign/:bucket/*path — upload via signed URL (no auth)

Upload to a path pre-authorized by a signed upload token. No JWT required.

| Query param | Description |
|-------------|-------------|
| `token` | Required. Token returned by `POST /object/upload/sign/:bucket/*path`. |

Request body: raw file bytes.

Response `200`:
```json
{ "Key": "avatars/tmp/upload.png", "path": "tmp/upload.png", "fullPath": "avatars/tmp/upload.png" }
```

| Status | Cause |
|--------|-------|
| `200` | Uploaded. |
| `400` | Token missing or invalid. |
| `413` | File exceeds the bucket's `max_size`. |

## Object download

### GET /object/*all — download dispatcher

A single catch-all route dispatches downloads based on path prefix:

| Path pattern | Auth required | Description |
|--------------|---------------|-------------|
| `GET /object/public/:bucket/*path` | No | Download from a public bucket. Returns `400 not_public` for private buckets. |
| `GET /object/authenticated/:bucket/*path` | Yes | Authenticated download. |
| `GET /object/:bucket/*path` | Yes | Authenticated download (default form). |

Response: raw file bytes with the stored `Content-Type` and `Cache-Control: public, max-age=3600`.

Image transforms are applied when query params are present on image downloads.

| Status | Cause |
|--------|-------|
| `200` | File bytes. |
| `400` | Bucket is not public (public path on private bucket). |
| `404` | Bucket or object not found. |

### HEAD /object/:bucket/*path — exists check

Returns `200` if the object exists and the caller's role can see it, `404` otherwise. No body.

## Object list

### POST /object/list/:bucket

List objects in a bucket.

Request body (JSON):

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `prefix` | `string` | `""` | Filter by path prefix. |
| `limit` | `number` | `100` | Max results. |
| `offset` | `number` | `0` | Pagination offset. |
| `search` | `string` | `""` | Substring filter on object name. |

Response `200`: array of object metadata objects.

```json
[
  {
    "name": "alice.png",
    "id": "profiles/alice.png",
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z",
    "metadata": null
  }
]
```

### POST /object/list-v2/:bucket

Cursor-based list with folder simulation.

Request body (JSON):

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `prefix` | `string` | `""` | Path prefix filter. |
| `limit` | `number` | `100` | Max results per page. |
| `cursor` | `string` | `""` | Cursor from previous page's `next_cursor`. |
| `with_delimiter` | `boolean` | `false` | When true, stops at `/` and groups into virtual folders. |
| `sortBy.column` | `string` | `"name"` | Sort column: `"name"`, `"created_at"`, or `"updated_at"`. |
| `sortBy.order` | `string` | `"ASC"` | `"ASC"` or `"DESC"`. |

Response `200`:

```json
{
  "has_next": true,
  "next_cursor": "profiles/bob.png",
  "folders": [{ "name": "profiles/", "key": "profiles/" }],
  "objects": [{ "name": "alice.png", "id": "alice.png", "created_at": "...", "updated_at": "...", "metadata": null }]
}
```

## Object delete

### DELETE /object/:bucket

Delete one or more objects.

Request body (JSON):

```json
{ "prefixes": ["profiles/alice.png", "profiles/bob.png"] }
```

Response `200`: array of deleted object records.

```json
[{ "name": "profiles/alice.png", "bucket_id": "avatars" }]
```

Objects not found or hidden by RLS are silently skipped.

## Object move and copy

### POST /object/move

Move an object within or across buckets.

Request body (JSON):

| Field | Type | Description |
|-------|------|-------------|
| `bucketId` | `string` | Source bucket. |
| `sourceKey` | `string` | Source object path. |
| `destinationKey` | `string` | Destination object path. |
| `destinationBucket` | `string` | Destination bucket (defaults to `bucketId`). |

Response `200`: `{ "message": "Successfully moved" }`

### POST /object/copy

Copy an object within or across buckets. Same request shape as move.

Response `200`: `{ "Key": "<destBucket>/<destKey>" }`

## Signed URLs

### POST /object/sign/:bucket/*path — create signed download URL

Generate a time-limited signed URL for downloading a private object.

Request body (JSON):

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `expiresIn` | `number` | `3600` | Expiry in seconds. |

Response `200`:
```json
{ "signedURL": "https://..." }
```

### POST /object/sign/:bucket — create signed download URLs (batch)

Request body (JSON):

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `expiresIn` | `number` | `3600` | Expiry in seconds. |
| `paths` | `string[]` | required | Object paths within the bucket. |

Response `200`: array of `{ "path": "...", "signedURL": "..." }` (or `{ "path": "...", "error": "..." }` on failure).

### POST /object/upload/sign/:bucket/*path — create signed upload URL

Obtain a pre-authorized upload token valid for 2 hours.

No request body required.

Response `200`:

```json
{
  "url": "/storage/v1/object/upload/sign/avatars/tmp/upload.png",
  "token": "<hmac-token>",
  "path": "tmp/upload.png"
}
```

Use `PUT /object/upload/sign/:bucket/*path?token=<token>` to upload without a JWT.

## Object info

### GET /object/info/authenticated/:bucket/*path

Return metadata for an object without downloading it.

Response `200`:

```json
{
  "id": "abc123",
  "name": "profiles/alice.png",
  "size": 48210,
  "content_type": "image/png",
  "created_at": "2024-01-01T00:00:00Z",
  "updated_at": "2024-01-01T00:00:00Z",
  "metadata": null
}
```

## Authorization

All object reads and writes go through RLS on `storage.objects`. The effective Postgres role is derived from the caller's JWT (`anon`, `authenticated`, or `service_role`). Declare RLS policies on the bucket in `instancez.yaml`:

```yaml
storage:
  avatars:
    public: false
    max_size: 5MB
    types: [image/png, image/jpeg, image/webp]
    rls:
      - operations: [select]
        check: "auth.uid() IS NOT NULL"
      - operations: [insert]
        check: "auth.uid() IS NOT NULL"
```

Signed-URL uploads bypass JWT auth but run the metadata write as `service_role` — equivalent to an S3 presigned PUT.
