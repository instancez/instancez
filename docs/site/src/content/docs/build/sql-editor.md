---
title: SQL Editor
description: Run arbitrary SQL against your database from the dashboard, on the privileged owner connection.
---

The dashboard's Database section has a SQL Editor tab for running arbitrary SQL directly against your database, the same role Supabase's SQL editor plays.

## What it does

- Runs on the privileged owner connection: DDL-capable, bypasses RLS. Equivalent to Supabase's SQL editor `postgres` role.
- In `readwrite` dashboard mode, the whole editor buffer runs as one transaction. Multiple statements are allowed; any error rolls back the whole buffer.
- In `readonly` dashboard mode, only a single statement runs per execution, inside a `READ ONLY` transaction — this also blocks a multi-statement buffer from smuggling a write past the read-only guard.
- Shows the last result set from the buffer. Results are capped at 1000 rows.

## Permissions & modes

- Admin-only, gated by the same admin/secret key as the rest of the dashboard.
- In `readonly` dashboard mode, queries run in a `READ ONLY` transaction and are limited to one statement, so writes are rejected.
- In `disabled` dashboard mode, the dashboard isn't served at all, so the SQL Editor is unreachable.

## Reserved schemas caveat

The `auth` and `storage` schemas are not write-protected in the SQL Editor, same as Supabase. The migrator reapplies these schemas from `instancez.yaml` on every boot, so manual changes made there through the SQL Editor can be overwritten, and they drift from your YAML source of truth in the meantime.

## What's next

- [RLS](/instancez/build/rls/) — the policies the SQL Editor bypasses
- [Tables / Schema](/instancez/build/schema/) — the YAML source of truth the migrator reapplies
