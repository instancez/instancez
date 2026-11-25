---
title: SQL Editor
description: Run arbitrary SQL against your database from the dashboard, on the privileged owner connection.
---

The dashboard's Database section has a SQL Editor tab for running arbitrary SQL directly against your database, the same role Supabase's SQL editor plays.

## What it does

- Runs on the privileged owner connection: DDL-capable, bypasses RLS. Equivalent to Supabase's SQL editor `postgres` role.
- **Run** (or Ctrl/Cmd+Enter) sends the selected text, or the statement under the cursor when nothing is selected.
- In `readwrite` dashboard mode, the text sent runs as one transaction. A selection may hold several statements; any error rolls them all back.
- In `readonly` dashboard mode, only a single statement runs per execution, inside a `READ ONLY` transaction — this also blocks a multi-statement selection from smuggling a write past the read-only guard.
- Shows the last result set, with the row count and how long the query took. Results are capped at 1000 rows.

## Editing

- Tables, columns and functions from your config autocomplete with their types, and are colored by kind. Tab accepts a suggestion; otherwise it indents.
- Postgres syntax errors are underlined as you type. The parser runs in the browser and loads the first time you open the editor.
- **Format** (or Shift+Alt+F) reformats the query. Ctrl/Cmd+Z undoes it.
- The query is saved as a draft in your browser, per project, and comes back when you reopen the editor.
- Click a column header to sort the results, click a cell to copy it, or use the row's copy button to copy the row as JSON.

## Permissions & modes

- Admin-only, gated by the same admin/secret key as the rest of the dashboard.
- In `readonly` dashboard mode, queries run in a `READ ONLY` transaction and are limited to one statement, so writes are rejected.
- In `disabled` dashboard mode, the dashboard isn't served at all, so the SQL Editor is unreachable.

## Reserved schemas caveat

The `auth` and `storage` schemas are not write-protected in the SQL Editor, same as Supabase. The migrator reapplies these schemas from `instancez.yaml` on every boot, so manual changes made there through the SQL Editor can be overwritten, and they drift from your YAML source of truth in the meantime.

## What's next

- [RLS](/instancez/build/rls/) — the policies the SQL Editor bypasses
- [Tables / Schema](/instancez/build/schema/) — the YAML source of truth the migrator reapplies
