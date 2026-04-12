# Events — Feature PRD

## Overview

The `on:` section defines event-driven triggers. When data changes (captured via PostgreSQL WAL) or a schedule fires (cron), Ultrabase dispatches actions — webhook HTTP calls, emails, or other delivery mechanisms.

This replaces the previous separate `webhooks:` and `notifications:` sections with a unified system.

---

## YAML Syntax

```yaml
on:
  welcome_email:
    events: [users.insert]
    email:
      to: "{{data.email}}"
      subject: "Welcome to {{project.name}}!"
      body_file: templates/welcome.html

  todo_webhook:
    events: [todos.insert]
    webhook:
      url: "${SLACK_WEBHOOK_URL}"
      headers:
        Content-Type: application/json
      retry:
        max: 3
        backoff: exponential

  daily_digest:
    schedule: "0 9 * * *"
    email:
      to_query: "SELECT email FROM users WHERE email_verified = true"
      data_query: |
        SELECT COUNT(*) FILTER (WHERE status = 'pending') as pending,
               COUNT(*) FILTER (WHERE status = 'done' AND done_at > NOW() - INTERVAL '1 day') as completed_today
        FROM todos WHERE user_id = $1
      subject: "Your daily digest — {{project.name}}"
      body: |
        You have {{data.pending}} pending tasks.
        {{data.completed_today}} completed in the last 24 hours.

  audit_deletes:
    events: ["*.delete"]
    webhook:
      url: "${AUDIT_WEBHOOK_URL}"
      retry:
        max: 5
        backoff: exponential
```

### Structure

Each entry under `on:` has:
1. **Trigger** — either `events:` (WAL-based) or `schedule:` (cron)
2. **One or more actions** — sub-keys like `webhook:`, `email:` (presence IS the action type)
3. Multiple actions per trigger are supported (both `email:` and `webhook:` on the same entry)

---

## Change Capture (WAL-Based CDC)

### How It Works

Ultrabase creates a PostgreSQL **logical replication slot** and consumes the write-ahead log (WAL) to detect data changes.

- `REPLICA IDENTITY FULL` is set on every user table — full before+after rows always available
- Events are **post-commit** (small delay, usually <10ms)
- **At-least-once delivery** — handlers must be idempotent
- No actor/request context in events — use data fields (`created_by`, `updated_by`) when attribution is needed

### Event Naming

Format: `{table}.{operation}`

| Pattern | Matches |
|---|---|
| `todos.insert` | New todo created |
| `todos.update` | Todo updated |
| `todos.delete` | Todo deleted |
| `users.insert` | User registered |
| `*.delete` | Any table delete |
| `*.*` | Any change |

### Startup Behavior

**Fail fast** if:
- `wal_level` is not `logical`
- Slot-create permission is missing
- `REPLICA IDENTITY FULL` cannot be applied

Clear error with documentation link explaining managed-DB configuration (RDS param groups, Cloud SQL flags, etc.).

---

## Event Payload

```json
{
  "id": "evt_a1b2c3d4",
  "event": "todos.update",
  "table": "todos",
  "operation": "update",
  "timestamp": "2026-04-05T10:42:15Z",
  "data": { "id": 1, "title": "Updated task", "status": "done" },
  "old_data": { "id": 1, "title": "Updated task", "status": "pending" }
}
```

| Operation | `data` | `old_data` |
|---|---|---|
| insert | Full new row | `null` |
| update | Full new row | Full previous row |
| delete | `null` | Full previous row |

Both `data` and `old_data` contain complete rows (REPLICA IDENTITY FULL).

---

## Event Delivery

- **No conditional filters** — every matching event fires all declared actions. Receivers filter client-side if needed.
- **No payload size caps** — full rows sent. Users must keep row sizes reasonable.
- **Per-action retry config:**

```yaml
retry:
  max: 3                  # max attempts (default: 3)
  backoff: exponential    # exponential | linear (default: exponential)
```

Exponential backoff schedule: immediate → +30s → +2m → +8m → +30m.

### Dead Letter

After `retry.max` attempts, the event moves to dead-letter state in the `_events` table. Admin endpoints:

```
GET  /api/_admin/events/dead         # list dead-lettered events
POST /api/_admin/events/:id/retry    # re-queue a specific event
POST /api/_admin/events/purge        # clear old delivered events
```

---

## Webhook Action

```yaml
webhook:
  url: "${SLACK_WEBHOOK_URL}"
  headers:
    Authorization: "Bearer ${WEBHOOK_SECRET}"
    Content-Type: application/json
  retry:
    max: 3
    backoff: exponential
```

### Signature

Every webhook request includes HMAC-SHA256 signature headers:

```
X-Ultrabase-Signature: sha256=a1b2c3d4...
X-Ultrabase-Timestamp: 1712016000
X-Ultrabase-Event: todos.insert
```

Signature computed over: `HMAC-SHA256(secret, "timestamp.body")`.

### Response Handling

- `2xx` → delivered successfully
- `4xx` (except 429) → permanent failure, no retry
- `429` or `5xx` → transient failure, retry with backoff
- Timeout (10s default) → transient failure, retry

### URL and Header Interpolation

`${ENV_VAR}` references are resolved at startup. `{{template}}` variables are resolved per-event.

---

## Email Action

```yaml
email:
  to: "{{data.email}}"
  subject: "Welcome to {{project.name}}!"
  body: "Hi {{data.display_name}}, welcome!"
  # OR
  body_file: templates/welcome.html
```

Requires an email provider in `providers:`.

### Cron Email (with recipients query)

```yaml
email:
  to_query: "SELECT email, id as user_id FROM users WHERE email_verified = true"
  data_query: "SELECT COUNT(*) as pending FROM todos WHERE user_id = $1 AND status = 'pending'"
  subject: "Your digest"
  body: "You have {{data.pending}} pending tasks."
```

- `to_query` returns recipients (must include `email` column)
- `data_query` provides per-recipient data (`$1` = user_id from `to_query`)
- `condition` (optional): `"data.pending > 0"` — only send if condition is true

---

## Template Substitution

Mustache-style `{{...}}` in strings within action configs:

| Variable | Source |
|---|---|
| `{{data.field}}` | New record data (or query result for cron) |
| `{{old_data.field}}` | Previous record (update/delete) |
| `{{event}}` | Event name (e.g. `todos.insert`) |
| `{{table}}` | Table name |
| `{{operation}}` | `insert`, `update`, `delete` |
| `{{timestamp}}` | Event timestamp |
| `{{project.name}}` | Project name from YAML |

Invalid/missing variables render as empty string with a warning log.

---

## Cron Triggers

```yaml
on:
  daily_report:
    schedule: "0 9 * * *"    # 9 AM UTC daily
    email: { ... }
```

Standard cron syntax, **UTC only**:

```
┌───────── minute (0-59)
│ ┌─────── hour (0-23)
│ │ ┌───── day of month (1-31)
│ │ │ ┌─── month (1-12)
│ │ │ │ ┌─ day of week (0-6, Sun=0)
│ │ │ │ │
* * * * *
```

If a cron job takes longer than its interval, the next run is skipped (no overlap).

---

## Auth Events

- **Table-level events** (`users.insert`, `users.update`, `users.delete`) flow through WAL like any table — fully available in `on:` triggers.
- **Application-level auth actions** (password reset requested, email verification sent) are **NOT** dispatched through WAL. The auth handler calls the email handler directly. These are not durable — if the process crashes mid-action, the user simply retries.

---

## WAL Operational Guardrails

| Guardrail | Default | Description |
|---|---|---|
| `max_slot_wal_keep_size` | 5 GB | Bounds disk usage; may cause event gaps if consumer is offline too long |
| Slot lag metric | — | Exposed via `/metrics` (Prometheus gauge) |
| Warning threshold | 1 GB | Log warning when slot lag exceeds this |
| Critical threshold | 5 GB | Critical log entry |
| `ultrabase slot reset` | — | CLI command to drop + recreate slot in emergencies |

### Embedded Topology

The WAL consumer runs **embedded in the main process** — the single binary runs HTTP server + WAL consumer + dispatcher. May split into a separate worker later if scaling demands it.

---

## Edge Cases

1. **Webhook URL down:** Events retry with backoff, eventually dead-letter.
2. **Event ordering:** Events are delivered in order per table. Cross-table ordering is not guaranteed.
3. **Circular webhooks:** If a webhook URL points back at Ultrabase, it could trigger infinite loops. Prevention: webhook requests include `X-Ultrabase-Webhook: true` header; Ultrabase ignores CRUD events triggered by webhook-marked requests.
4. **Template errors:** Invalid template variables render as empty string with a warning log.
5. **Missing email provider:** If an `email:` action is configured but no email provider exists, startup warns (does not block).
6. **WAL consumer offline:** If the consumer is down, WAL accumulates. When it reconnects, it processes the backlog. If `max_slot_wal_keep_size` is exceeded, the slot is invalidated — events in the gap are lost.
