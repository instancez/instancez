# Admin User CRUD — Design Spec

**Date:** 2026-06-14

## Overview

Add a Users section to the dashboard so admins can view, create, edit, and delete users. Uses the existing Supabase-compatible admin endpoints at `/auth/v1/admin/users` — no new backend routes needed. Introduces a generic `Modal` component reusable by the platform shell.

---

## Backend changes

### Add `banned_until` to `buildUser`

`buildUser` in `internal/adapter/http/auth_handler.go` currently omits `banned_until` from its response. Add it so the dashboard can read and display ban status:

```go
"banned_until": asTimeString(row["banned_until"]),
```

This makes the response fully match what Supabase GoTrue returns and satisfies the supabase-js compat contract. No other backend changes are needed — the full admin user CRUD surface already exists at `/auth/v1/admin/users` and is exercised by `TestSupabaseJSCompat`.

---

## Types (`dashboard/src/lib/types.ts`)

New `AdminUser` type matching `buildUser`'s response shape:

```ts
export interface AdminUser {
  id: string;
  email: string;
  email_confirmed_at: string;
  banned_until: string;       // "" when not banned
  last_sign_in_at: string;
  app_metadata: Record<string, unknown>;
  user_metadata: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}
```

---

## API client (`dashboard/src/api/client.ts`)

Replace the existing `getUsers()` (which hits the weaker `/_admin/users`) with four functions targeting `/auth/v1/admin/users`. All use the same `request()` helper (admin key bearer auth).

```ts
adminListUsers(page?, perPage?) → { users: AdminUser[], total: number }
adminCreateUser(email, password, emailConfirm) → AdminUser
adminUpdateUser(id, patch: { email?, password?, ban_duration?, email_confirm? }) → AdminUser
adminDeleteUser(id) → void
```

`total` is read from the `x-total-count` response header (set by `handleAdminListUsers`).

The existing `getUsers()` function stays for now (nothing else calls it) but is no longer used by any page.

---

## `ConsoleBackend` interface (`dashboard/src/console/backend.ts`)

Add four flat methods (matching the existing style — no namespacing):

```ts
listUsers(page?: number, perPage?: number): Promise<{ users: AdminUser[]; total: number }>;
createUser(email: string, password: string, emailConfirm: boolean): Promise<AdminUser>;
updateUser(id: string, patch: { email?: string; password?: string; ban_duration?: string; email_confirm?: boolean }): Promise<AdminUser>;
deleteUser(id: string): Promise<void>;
```

Wired through `adminBackend.ts` as pass-throughs to the four new API client functions.

---

## Generic `Modal` component (`dashboard/src/components/Modal.tsx`)

A declarative, composable overlay — no opinion about content. Used by `UsersPage` for create/edit; available to the platform for its own modals.

**API:**

```tsx
<Modal open={open} onClose={onClose} title="Create user">
  <Modal.Body>…form fields…</Modal.Body>
  <Modal.Footer>…buttons…</Modal.Footer>
</Modal>
```

**Behaviour:**
- Renders via Chakra `Portal` so it escapes any positioned ancestor
- Closes on Escape key and backdrop click
- Animated: fade + slight scale on open/close (matches existing `Dialog.tsx` style)
- `title` prop renders a styled header with a close `×` button
- `Modal.Body` and `Modal.Footer` are simple layout wrappers (padding, border)
- No built-in form state, validation, or buttons — callers own all of that

**Visual style:** same tokens as `Dialog.tsx` — `bg.panel`, `border`, `borderRadius: 2xl`, `boxShadow: lg`. Max width `480px`, centred, `mx: 4` for mobile.

---

## Users page (`dashboard/src/pages/Users.tsx`)

Exported as `UsersPage`. Added to `consoleRoutes()` at path `users`.

### List view

A table of users with columns:

| Column | Source field |
|---|---|
| Email | `email` |
| Confirmed | `email_confirmed_at` (checkmark or dash) |
| Last sign-in | `last_sign_in_at` (short date via `toLocaleDateString()`, or "Never") |
| Created | `created_at` (short date via `toLocaleDateString()`) |
| Status | `banned_until` — shows "Banned" badge when non-empty |

- Loads on mount via `backend.listUsers(1, 50)`
- Pagination controls when `total > 50`
- "Add user" button (top-right, matches `DetailToolbar` pattern) opens `UserModal` in create mode
- Clicking a row opens `UserModal` in edit mode
- Empty state when no users (uses existing `EmptyState` component)
- Error state on load failure

### `UserModal` (local component, same file)

Opened for both create and edit. Uses the generic `Modal`.

**Create mode fields:**
- Email (required)
- Password (required)
- "Confirm email" checkbox (default: off)

**Edit mode fields:**
- Email (pre-populated, optional change)
- New password (optional — empty means no change)
- Ban toggle: "Banned" / "Active" — sends `ban_duration: "infinity"` to ban, `ban_duration: "none"` to unban

**Footer:**
- Edit mode: "Delete" button (left-aligned, destructive) + "Cancel" + "Save"
- Create mode: "Cancel" + "Create user"

Delete uses `useDialog().confirm` with `destructive: true` and `confirmText` set to the user's email (matches the existing delete pattern from `TableDetail`).

Form errors display inline below the relevant field (e.g. "A user with this email address has already been registered").

---

## Navigation

**`dashboard/src/components/Sidebar.tsx`** — add "Users" entry with `Users` icon from lucide-react, path `/users`. Inserted after "Auth".

**`dashboard/src/console/routes.tsx`** — add `usersRoutes()` export and include it in `consoleRoutes()`:

```ts
export const usersRoutes = (): RouteObject[] => [
  { index: true, element: <UsersPage />, handle: { title: "Users", description: "Manage authenticated users" } },
];

// in consoleRoutes():
{ path: "users", children: usersRoutes() },
```

---

## What this does NOT change

- No new Go endpoints. All user CRUD hits existing `/auth/v1/admin/users` routes.
- `/_admin/users` and its sub-routes (`/disable`, `/reset-password`) are left in place but unused by the dashboard going forward. Cleanup is a separate task.
- No changes to the Auth config page (`Auth.tsx`) — that remains the JWT/OAuth/email-template editor.
- `banned_until` addition to `buildUser` is the only backend change, and it's additive (no supabase-js compat risk).

---

## Test plan

- **Go unit:** `go test -race ./...` — no new Go code beyond the `buildUser` field addition.
- **Go integration:** `go test -tags=integration -race ./internal/adapter/http/...` — existing `TestSupabaseJSCompat` already exercises all five admin user endpoints; confirm it still passes.
- **Dashboard:** `npm test` in `dashboard/` — add vitest tests for `UsersPage` (list render, modal open/close, create/edit/delete flows) and `Modal` (open/close, Escape, backdrop click).
