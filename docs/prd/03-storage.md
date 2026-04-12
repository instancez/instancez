# Storage — Feature PRD

## Overview

Storage provides file upload, download, and management via configurable buckets. Files are stored in an external object storage provider (S3, GCS, MinIO) and tracked in a single `_objects` metadata table. All uploads and downloads flow through **signed URLs** — Ultrabase never proxies file bytes.

---

## YAML Syntax

Bucket names are direct keys under `storage:` (no `buckets:` wrapper). The storage provider is configured in the top-level `providers:` section, not here.

```yaml
storage:
  avatars:
    max_size: 2MB
    types: [image/*]
    public: true
    rls:
      - operations: [insert, delete]
        check: "uploaded_by = auth.uid()"

  attachments:
    max_size: 10MB
    types: [image/*, application/pdf, text/*]
    rls:
      - operations: [select, insert]
        check: "auth.is_authenticated()"
      - operations: [delete]
        check: "uploaded_by = auth.uid()"
```

### Bucket Properties

| Property | Type | Default | Description |
|---|---|---|---|
| `max_size` | size | — | Per-file upload size limit (e.g. `2MB`, `50MB`) |
| `types` | string[] | — | Allowed MIME types; supports wildcards (`image/*`) |
| `public` | bool | false | Bypasses RLS for SELECT (public downloads) |
| `rls` | policy[] | — | Per-bucket RLS policies (same syntax as table RLS) |

### What's NOT in YAML

- **Provider config** — in `providers:` section
- **Quotas** — no total quota tracking in v1
- **Retention** — no auto-delete in v1
- **Image transformations** — deferred

---

## Metadata Table

A single `_objects` table stores metadata for all files across all buckets:

```sql
CREATE TABLE _objects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bucket_id TEXT NOT NULL,
    size BIGINT NOT NULL,
    mime TEXT NOT NULL,
    uploaded_by BIGINT REFERENCES users(id),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata JSONB
);
```

- `bucket_id` = bucket name (e.g. `"avatars"`)
- `id` = server-generated UUID (this is the object key)
- No original filename stored — objects identified by UUID only

### RLS on `_objects`

Per-bucket `rls:` blocks compile into Postgres RLS policies on `_objects`, each scoped by `bucket_id`:

```sql
-- Public bucket: bypass auth for SELECT
CREATE POLICY avatars_select ON _objects
    FOR SELECT USING (bucket_id = 'avatars');

-- Upload policy
CREATE POLICY avatars_insert ON _objects
    FOR INSERT WITH CHECK (bucket_id = 'avatars' AND uploaded_by = auth.uid());
```

Same auth helpers available: `auth.uid()`, `auth.is_authenticated()`.

---

## API Endpoints

### Sign for Upload — `POST /api/storage/:bucket/sign`

```json
// Request
{ "mime": "image/png", "size": 204800 }

// Response (200)
{
  "id": "a1b2c3d4-...",
  "upload_url": "https://s3.amazonaws.com/bucket/a1b2c3d4-...?X-Amz-Signature=...",
  "expires_at": "2026-04-05T12:15:00Z"
}
```

**Flow:**
1. Validate: bucket exists, MIME matches `types`, size within `max_size`
2. Evaluate RLS INSERT policy
3. Generate UUID, insert `_objects` row
4. Generate presigned PUT URL from storage provider
5. Return URL + object ID to client

Client then PUTs the file directly to `upload_url`.

### Sign for Download — `POST /api/storage/:bucket/:id/sign`

```json
// Response (200)
{
  "url": "https://s3.amazonaws.com/bucket/a1b2c3d4-...?...",
  "expires_at": "2026-04-05T12:15:00Z"
}
```

- Private buckets: evaluates RLS SELECT policy
- Public buckets (`public: true`): skips auth check, still generates signed URL (provider buckets are private at the provider level)

### Delete — `DELETE /api/storage/:bucket/:id`

Deletes the file from the storage provider AND the `_objects` row. Evaluates RLS DELETE policy. Returns `204 No Content`.

### Object Metadata — `GET /api/storage/:bucket/:id`

Returns metadata without generating a download URL. Subject to RLS SELECT policy.

```json
{
  "id": "a1b2c3d4-...",
  "bucket_id": "attachments",
  "size": 245760,
  "mime": "application/pdf",
  "uploaded_by": 42,
  "uploaded_at": "2026-04-05T12:00:00Z"
}
```

---

## Integration with Tables

### File Reference Fields

Table fields reference storage objects using a `text` type with a `ref` hint:

```yaml
tables:
  todos:
    fields:
      attachment:
        type: text
        ref: storage.attachments
        on_delete: cascade        # cascade | keep (default: keep)
```

- Plain `text` column holding the object UUID
- `ref: storage.<bucket>` is a metadata hint for the framework
- `on_delete: cascade` — delete the file when the parent row is deleted
- `on_delete: keep` — file persists in storage (default)
- On create/update: framework validates the UUID exists in `_objects` with the correct `bucket_id`

### Multiple Files Per Record

Use a separate join table:

```yaml
tables:
  todo_attachments:
    fields:
      id: { type: bigserial, primary_key: true }
      todo_id:
        foreign_key:
          references: todos.id
          on_delete: cascade
      file_id:
        type: text
        ref: storage.attachments
        on_delete: cascade
```

---

## Storage Provider Interface

```go
type StorageProvider interface {
    SignUpload(ctx context.Context, key string, contentType string, expiry time.Duration) (string, error)
    SignDownload(ctx context.Context, key string, expiry time.Duration) (string, error)
    Delete(ctx context.Context, key string) error
    EnsureBucket(ctx context.Context, bucket string) error
}
```

No `Upload` or `Download` methods — Ultrabase never proxies file bytes.

---

## Access Control Summary

| Operation | Public Bucket | Private Bucket |
|---|---|---|
| Download (sign) | No auth required | Auth + RLS SELECT |
| Upload (sign) | Auth + RLS INSERT | Auth + RLS INSERT |
| Delete | Auth + RLS DELETE | Auth + RLS DELETE |

Admin key (`ULTRABASE_ADMIN_KEY`) bypasses all RLS checks.

---

## Bucket Setup

- Buckets declared in `storage:` are **auto-created on startup** via `EnsureBucket`
- Provider buckets must be **private at the provider level** — Ultrabase RLS is the only gate
- All access routes through Ultrabase-generated signed URLs

---

## Edge Cases

1. **Upload not completed:** Client obtains signed URL but never uploads. `_objects` row exists with no file. Background cleanup or manual admin action can reconcile.
2. **Concurrent uploads:** Each upload generates a unique UUID — no conflicts.
3. **MIME wildcards:** `image/*` matches `image/png`, `image/jpeg`, etc.
4. **Dangling references:** When a file is deleted via storage API, table text fields holding that ID are not automatically nullified. Application should handle this.
5. **Object keys:** Bare UUIDs — no path prefixes, no file extensions.
