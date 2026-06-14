# Admin User CRUD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a top-level Users section to the dashboard with full CRUD backed by the existing Supabase-compatible `/auth/v1/admin/users` endpoints.

**Architecture:** A generic `Modal` component provides a reusable overlay shell; `UsersPage` + `UserModal` compose it. The backend already has all five admin user routes — only one Go line is added (`banned_until` in `buildUser`). Everything else is frontend-only: a new type, four API client functions, four `ConsoleBackend` interface methods, two new React components, and nav wiring.

**Tech Stack:** Go 1.23, React 18 + TypeScript, Chakra UI v3, Vitest + React Testing Library

---

### Task 1: Add `banned_until` to `buildUser`

**Files:**
- Modify: `internal/adapter/http/auth_handler.go` — `buildUser` return block (~line 1472)
- Modify: `internal/adapter/http/auth_handler_test.go` — `TestBuildUser_GoTrueFieldContract` (~line 79)

- [ ] **Step 1: Add the field to `buildUser`**

In `internal/adapter/http/auth_handler.go`, find the `return gin.H{...}` block inside `buildUser`. Add `"banned_until"` between `"last_sign_in_at"` and `"app_metadata"`:

```go
return gin.H{
    "id":                 userID,
    "aud":                "authenticated",
    "role":               "authenticated",
    "email":              email,
    "email_confirmed_at": emailConfirmedAt,
    "phone":              "",
    "confirmed_at":       confirmedAt,
    "last_sign_in_at":    lastSignInAt,
    "banned_until":       asTimeString(row["banned_until"]),
    "app_metadata":       appMeta,
    "user_metadata":      userMeta,
    "identities":         []any{},
    "created_at":         createdAt,
    "updated_at":         updatedAt,
}
```

- [ ] **Step 2: Update `TestBuildUser_GoTrueFieldContract`**

In `internal/adapter/http/auth_handler_test.go`, add `"banned_until": nil` to the `row` fixture and `"banned_until"` to `required`:

```go
row := map[string]any{
    "id":                 "11111111-2222-3333-4444-555555555555",
    "email":              "user@example.com",
    "email_verified":     true,
    "email_confirmed_at": time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
    "last_sign_in_at":    time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
    "banned_until":       nil,
    "raw_app_meta_data":  `{"provider":"email","providers":["email"]}`,
    "raw_user_meta_data": `{"display_name":"Alice"}`,
    "created_at":         time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
    "updated_at":         time.Date(2026, 4, 10, 0, 0, 0, 0, time.UTC),
}

required := []string{
    "id", "aud", "role", "email", "email_confirmed_at", "phone",
    "confirmed_at", "last_sign_in_at", "banned_until", "app_metadata",
    "user_metadata", "identities", "created_at", "updated_at",
}
```

- [ ] **Step 3: Run Go unit tests**

```bash
go test -race ./...
```

Expected: all pass, including `TestBuildUser_GoTrueFieldContract` and `TestBuildUser_UnverifiedHasEmptyConfirmedAt`.

- [ ] **Step 4: Commit**

```bash
git add internal/adapter/http/auth_handler.go internal/adapter/http/auth_handler_test.go
git commit -m "feat(auth): expose banned_until in buildUser response"
```

---

### Task 2: `AdminUser` type + API client functions

**Files:**
- Modify: `dashboard/src/lib/types.ts` — append `AdminUser`
- Modify: `dashboard/src/api/client.ts` — add `authAdminRequest` helper + four functions

- [ ] **Step 1: Add `AdminUser` to `dashboard/src/lib/types.ts`**

Append to the bottom of the file:

```ts
export interface AdminUser {
  id: string;
  email: string;
  email_confirmed_at: string;
  banned_until: string;
  last_sign_in_at: string;
  app_metadata: Record<string, unknown>;
  user_metadata: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}
```

- [ ] **Step 2: Add the `AdminUser` import to `dashboard/src/api/client.ts`**

The existing import at the top of `client.ts` is:

```ts
import type {
  Config,
  ConfigStatus,
  StatsResponse,
  DiffResponse,
  ValidationError,
} from "../lib/types";
```

Change it to:

```ts
import type {
  Config,
  ConfigStatus,
  StatsResponse,
  DiffResponse,
  ValidationError,
  AdminUser,
} from "../lib/types";
```

- [ ] **Step 3: Add `authAdminRequest` helper and four user functions to `dashboard/src/api/client.ts`**

Add after the existing `// Users` block (after `getUsers`):

```ts
// ── Auth admin user management (/auth/v1/admin/users) ────────────────────
// Uses the Supabase-compatible endpoint surface, not /_admin/users.

const AUTH_ADMIN_BASE = "/auth/v1/admin";

async function authAdminRequest<T>(path: string, options: RequestInit = {}): Promise<T> {
  const key = getAdminKey();
  if (!key) throw new Error("No admin key configured");

  const res = await fetch(`${AUTH_ADMIN_BASE}${path}`, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${key}`,
      ...options.headers,
    },
  });

  if (res.status === 401) {
    sessionStorage.removeItem("instancez_admin_key");
    window.location.reload();
    throw new Error("Unauthorized");
  }

  if (!res.ok) {
    const body = await res.json().catch(() => null);
    const err = new Error(body?.message || body?.error || `HTTP ${res.status}`);
    (err as any).status = res.status;
    (err as any).body = body;
    throw err;
  }

  return res.json();
}

export async function adminListUsers(
  page = 1,
  perPage = 50
): Promise<{ users: AdminUser[]; total: number }> {
  const key = getAdminKey();
  if (!key) throw new Error("No admin key configured");

  const res = await fetch(
    `${AUTH_ADMIN_BASE}/users?page=${page}&per_page=${perPage}`,
    { headers: { "Content-Type": "application/json", Authorization: `Bearer ${key}` } }
  );

  if (res.status === 401) {
    sessionStorage.removeItem("instancez_admin_key");
    window.location.reload();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    const body = await res.json().catch(() => null);
    const err = new Error(body?.message || body?.error || `HTTP ${res.status}`);
    (err as any).status = res.status;
    throw err;
  }

  const total = parseInt(res.headers.get("x-total-count") ?? "0", 10);
  const data = await res.json();
  return { users: data.users ?? [], total };
}

export async function adminCreateUser(
  email: string,
  password: string,
  emailConfirm: boolean
): Promise<AdminUser> {
  return authAdminRequest<AdminUser>("/users", {
    method: "POST",
    body: JSON.stringify({ email, password, email_confirm: emailConfirm }),
  });
}

export async function adminUpdateUser(
  id: string,
  patch: { email?: string; password?: string; ban_duration?: string; email_confirm?: boolean }
): Promise<AdminUser> {
  return authAdminRequest<AdminUser>(`/users/${encodeURIComponent(id)}`, {
    method: "PUT",
    body: JSON.stringify(patch),
  });
}

export async function adminDeleteUser(id: string): Promise<void> {
  await authAdminRequest<unknown>(`/users/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}
```

- [ ] **Step 4: Run dashboard tests to check for TypeScript errors**

```bash
cd dashboard && npm test
```

Expected: all existing tests pass. No new tests yet.

- [ ] **Step 5: Commit**

```bash
git add dashboard/src/lib/types.ts dashboard/src/api/client.ts
git commit -m "feat(dashboard): add AdminUser type and auth admin API client functions"
```

---

### Task 3: `ConsoleBackend` interface + `adminBackend` wiring

**Files:**
- Modify: `dashboard/src/console/backend.ts` — add four methods
- Modify: `dashboard/src/console/adminBackend.ts` — wire the four functions

- [ ] **Step 1: Add `AdminUser` import and four methods to `dashboard/src/console/backend.ts`**

Add `AdminUser` to the existing import at the top:

```ts
import type { Config, ConfigStatus, StatsResponse, DiffResponse, AdminUser } from "../lib/types";
import type { EnvVarsResponse, ConfigPreview } from "../api/client";
```

Then add four methods at the bottom of the `ConsoleBackend` interface, after `postFunctionDeps`:

```ts
  // user management
  listUsers(page?: number, perPage?: number): Promise<{ users: AdminUser[]; total: number }>;
  createUser(email: string, password: string, emailConfirm: boolean): Promise<AdminUser>;
  updateUser(
    id: string,
    patch: { email?: string; password?: string; ban_duration?: string; email_confirm?: boolean }
  ): Promise<AdminUser>;
  deleteUser(id: string): Promise<void>;
```

- [ ] **Step 2: Wire the four methods in `dashboard/src/console/adminBackend.ts`**

Add the four new imports to the existing `import * as api from "../api/client";` block (no change needed — `api.*` already covers all exports). Then add four entries to the `adminBackend` object:

```ts
import * as api from "../api/client";
import { fullCapabilities, type ConsoleBackend } from "./backend";

export const adminBackend: ConsoleBackend = {
  capabilities: fullCapabilities(),
  getConfig: () => api.getConfig(),
  getConfigStatus: () => api.getConfigStatus(),
  previewConfig: (config) => api.previewConfig(config),
  putConfig: (config, checksum) => api.putConfig(config, checksum),
  getEnvVars: (names) => api.getEnvVars(names),
  writeSecrets: (vars) => api.putDotenv(vars),
  getKeys: () => api.getKeys(),
  getStats: () => api.getStats(),
  getConfigDiff: () => api.getConfigDiff(),
  getFunctionCode: (name) => api.getFunctionCode(name),
  putFunctionCode: (name, content) => api.putFunctionCode(name, content),
  checkFunctionFile: (file) => api.checkFunctionFile(file),
  getFunctionDeps: () => api.getFunctionDeps(),
  postFunctionDeps: (add, remove) => api.postFunctionDeps(add, remove),
  listUsers: (page, perPage) => api.adminListUsers(page, perPage),
  createUser: (email, password, emailConfirm) => api.adminCreateUser(email, password, emailConfirm),
  updateUser: (id, patch) => api.adminUpdateUser(id, patch),
  deleteUser: (id) => api.adminDeleteUser(id),
};
```

- [ ] **Step 3: Run dashboard tests**

```bash
cd dashboard && npm test
```

Expected: all existing tests pass. TypeScript compilation confirms the interface is satisfied.

- [ ] **Step 4: Commit**

```bash
git add dashboard/src/console/backend.ts dashboard/src/console/adminBackend.ts
git commit -m "feat(dashboard): add user management methods to ConsoleBackend"
```

---

### Task 4: Generic `Modal` component

**Files:**
- Create: `dashboard/src/components/Modal.tsx`
- Create: `dashboard/src/components/Modal.test.tsx`

- [ ] **Step 1: Write the failing tests in `dashboard/src/components/Modal.test.tsx`**

```tsx
import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { Modal } from "./Modal";

describe("Modal", () => {
  it("renders title and children when open", () => {
    renderWithChakra(
      <Modal open onClose={vi.fn()} title="Test title">
        <Modal.Body>body content</Modal.Body>
      </Modal>
    );
    expect(screen.getByText("Test title")).toBeInTheDocument();
    expect(screen.getByText("body content")).toBeInTheDocument();
  });

  it("renders nothing when closed", () => {
    renderWithChakra(
      <Modal open={false} onClose={vi.fn()} title="Hidden">
        <Modal.Body>hidden content</Modal.Body>
      </Modal>
    );
    expect(screen.queryByText("Hidden")).not.toBeInTheDocument();
    expect(screen.queryByText("hidden content")).not.toBeInTheDocument();
  });

  it("calls onClose when × button clicked", () => {
    const onClose = vi.fn();
    renderWithChakra(
      <Modal open onClose={onClose} title="T">
        <Modal.Body>body</Modal.Body>
      </Modal>
    );
    fireEvent.click(screen.getByRole("button", { name: /close/i }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("calls onClose when backdrop clicked", () => {
    const onClose = vi.fn();
    renderWithChakra(
      <Modal open onClose={onClose} title="T">
        <Modal.Body>body</Modal.Body>
      </Modal>
    );
    fireEvent.click(screen.getByTestId("modal-backdrop"));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("calls onClose on Escape key", () => {
    const onClose = vi.fn();
    renderWithChakra(
      <Modal open onClose={onClose} title="T">
        <Modal.Body>body</Modal.Body>
      </Modal>
    );
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("renders Modal.Footer children", () => {
    renderWithChakra(
      <Modal open onClose={vi.fn()} title="T">
        <Modal.Body>body</Modal.Body>
        <Modal.Footer>
          <button>Save</button>
        </Modal.Footer>
      </Modal>
    );
    expect(screen.getByText("Save")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
cd dashboard && npm test -- Modal
```

Expected: FAIL — `Cannot find module './Modal'`

- [ ] **Step 3: Implement `dashboard/src/components/Modal.tsx`**

```tsx
import { useEffect } from "react";
import { X } from "lucide-react";
import { Box, HStack, Portal, Text } from "@chakra-ui/react";

interface ModalProps {
  open: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
}

function ModalRoot({ open, onClose, title, children }: ModalProps) {
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <Portal>
      <Box
        position="fixed"
        inset="0"
        zIndex="overlay"
        display="flex"
        alignItems="center"
        justifyContent="center"
        bg="blackAlpha.600"
        backdropFilter="blur(2px)"
        data-testid="modal-backdrop"
        onClick={(e) => {
          if (e.target === e.currentTarget) onClose();
        }}
      >
        <Box
          w="full"
          maxW="480px"
          mx="4"
          borderRadius="2xl"
          overflow="hidden"
          borderWidth="1px"
          borderColor="border"
          bg="bg.panel"
          boxShadow="lg"
        >
          <HStack
            justify="space-between"
            px="6"
            pt="5"
            pb="4"
            borderBottomWidth="1px"
            borderColor="border"
          >
            <Text fontSize="md" fontWeight="semibold" color="fg">
              {title}
            </Text>
            <Box
              as="button"
              onClick={onClose}
              aria-label="Close"
              p="1"
              borderRadius="md"
              color="fg.muted"
              _hover={{ color: "fg", bg: "bg.subtle" }}
              cursor="pointer"
            >
              <X size={14} />
            </Box>
          </HStack>
          {children}
        </Box>
      </Box>
    </Portal>
  );
}

function ModalBody({ children }: { children: React.ReactNode }) {
  return (
    <Box px="6" py="4">
      {children}
    </Box>
  );
}

function ModalFooter({ children }: { children: React.ReactNode }) {
  return (
    <HStack
      justify="flex-end"
      gap="2.5"
      px="6"
      pb="5"
      pt="3"
      borderTopWidth="1px"
      borderColor="border"
    >
      {children}
    </HStack>
  );
}

export const Modal = Object.assign(ModalRoot, {
  Body: ModalBody,
  Footer: ModalFooter,
});
```

- [ ] **Step 4: Run the tests to confirm they pass**

```bash
cd dashboard && npm test -- Modal
```

Expected: all 6 Modal tests pass.

- [ ] **Step 5: Commit**

```bash
git add dashboard/src/components/Modal.tsx dashboard/src/components/Modal.test.tsx
git commit -m "feat(dashboard): add generic Modal component"
```

---

### Task 5: `UsersPage` + `UserModal`

**Files:**
- Create: `dashboard/src/pages/Users.tsx`
- Create: `dashboard/src/pages/Users.test.tsx`

- [ ] **Step 1: Write the failing tests in `dashboard/src/pages/Users.test.tsx`**

```tsx
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { BackendProvider } from "../console/BackendContext";
import { DialogProvider } from "../components/Dialog";
import { UsersPage } from "./Users";
import type { ConsoleBackend } from "../console/backend";
import type { AdminUser } from "../lib/types";

const alice: AdminUser = {
  id: "u1",
  email: "alice@example.com",
  email_confirmed_at: "2026-01-01T00:00:00Z",
  banned_until: "",
  last_sign_in_at: "2026-06-01T00:00:00Z",
  app_metadata: {},
  user_metadata: {},
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

const bannedUser: AdminUser = {
  ...alice,
  id: "u2",
  email: "banned@example.com",
  banned_until: "2099-01-01T00:00:00Z",
};

function makeBackend(overrides: Partial<ConsoleBackend> = {}): ConsoleBackend {
  return {
    capabilities: {
      canWriteConfig: false,
      canWriteSecrets: false,
      canEditFunctionCode: false,
      canManageDeps: false,
      hasStats: false,
    },
    getConfig: vi.fn(),
    getConfigStatus: vi.fn(),
    previewConfig: vi.fn(),
    putConfig: vi.fn(),
    getEnvVars: vi.fn(),
    writeSecrets: vi.fn(),
    getKeys: vi.fn(),
    getStats: vi.fn(),
    getConfigDiff: vi.fn(),
    getFunctionCode: vi.fn(),
    putFunctionCode: vi.fn(),
    checkFunctionFile: vi.fn(),
    getFunctionDeps: vi.fn(),
    postFunctionDeps: vi.fn(),
    listUsers: vi.fn().mockResolvedValue({ users: [], total: 0 }),
    createUser: vi.fn().mockResolvedValue(alice),
    updateUser: vi.fn().mockResolvedValue(alice),
    deleteUser: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  } as unknown as ConsoleBackend;
}

function renderUsersPage(backend: ConsoleBackend) {
  return renderWithChakra(
    <BackendProvider backend={backend}>
      <MemoryRouter>
        <DialogProvider>
          <UsersPage />
        </DialogProvider>
      </MemoryRouter>
    </BackendProvider>
  );
}

describe("UsersPage", () => {
  it("shows empty state when no users", async () => {
    renderUsersPage(makeBackend());
    expect(await screen.findByText("No users yet")).toBeInTheDocument();
  });

  it("renders user rows", async () => {
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }),
    });
    renderUsersPage(backend);
    expect(await screen.findByText("alice@example.com")).toBeInTheDocument();
  });

  it("shows Banned badge for banned users", async () => {
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [bannedUser], total: 1 }),
    });
    renderUsersPage(backend);
    expect(await screen.findByText("Banned")).toBeInTheDocument();
  });

  it("opens create modal when Add user clicked", async () => {
    renderUsersPage(makeBackend());
    await screen.findByText("No users yet");
    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    expect(screen.getByText("Create user")).toBeInTheDocument();
  });

  it("opens edit modal when user row clicked", async () => {
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }),
    });
    renderUsersPage(backend);
    fireEvent.click(await screen.findByText("alice@example.com"));
    expect(screen.getByText("Edit user")).toBeInTheDocument();
  });

  it("calls createUser on form submit in create mode", async () => {
    const createUser = vi.fn().mockResolvedValue(alice);
    const backend = makeBackend({ createUser });
    renderUsersPage(backend);
    await screen.findByText("No users yet");

    fireEvent.click(screen.getByRole("button", { name: /add user/i }));
    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: "alice@example.com" },
    });
    fireEvent.change(screen.getByLabelText(/password/i), {
      target: { value: "secret123" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create user/i }));

    await waitFor(() => expect(createUser).toHaveBeenCalledWith("alice@example.com", "secret123", false));
  });

  it("calls updateUser on save in edit mode", async () => {
    const updateUser = vi.fn().mockResolvedValue(alice);
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }),
      updateUser,
    });
    renderUsersPage(backend);

    fireEvent.click(await screen.findByText("alice@example.com"));
    fireEvent.change(screen.getByLabelText(/email/i), {
      target: { value: "new@example.com" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() =>
      expect(updateUser).toHaveBeenCalledWith("u1", expect.objectContaining({ email: "new@example.com" }))
    );
  });

  it("calls deleteUser after confirm in edit mode", async () => {
    const deleteUser = vi.fn().mockResolvedValue(undefined);
    const backend = makeBackend({
      listUsers: vi.fn().mockResolvedValue({ users: [alice], total: 1 }),
      deleteUser,
    });
    renderUsersPage(backend);

    fireEvent.click(await screen.findByText("alice@example.com"));
    fireEvent.click(screen.getByRole("button", { name: /^delete$/i }));

    // The confirm dialog appears with a text input gated by the user's email.
    // Type the email to unlock the "Delete user" confirm button.
    const input = await screen.findByPlaceholderText("alice@example.com");
    fireEvent.change(input, { target: { value: "alice@example.com" } });
    fireEvent.click(await screen.findByRole("button", { name: /delete user/i }));

    await waitFor(() => expect(deleteUser).toHaveBeenCalledWith("u1"));
  });
});
```

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
cd dashboard && npm test -- Users
```

Expected: FAIL — `Cannot find module './Users'`

- [ ] **Step 3: Implement `dashboard/src/pages/Users.tsx`**

```tsx
import { useState, useEffect, useCallback } from "react";
import { Users as UsersIcon, Plus, Check, Minus } from "lucide-react";
import { Box, HStack, Text, VStack } from "@chakra-ui/react";
import { useBackend } from "../console/BackendContext";
import { useDialog } from "../components/Dialog";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { Modal } from "../components/Modal";
import { Button, Field, Input } from "../components/ui";
import { Toggle } from "../components/Toggle";
import type { AdminUser } from "../lib/types";

function formatDate(iso: string): string {
  if (!iso) return "Never";
  return new Date(iso).toLocaleDateString();
}

function isBanned(user: AdminUser): boolean {
  return !!user.banned_until;
}

// ── UserModal ─────────────────────────────────────────────────────────────

interface UserModalProps {
  user: AdminUser | null; // null = create mode
  onClose: () => void;
  onSaved: () => void;
}

function UserModal({ user, onClose, onSaved }: UserModalProps) {
  const backend = useBackend();
  const dialog = useDialog();
  const isEdit = user !== null;

  const [email, setEmail] = useState(user?.email ?? "");
  const [password, setPassword] = useState("");
  const [emailConfirm, setEmailConfirm] = useState(false);
  const [banned, setBanned] = useState(isEdit ? isBanned(user!) : false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  async function handleSubmit() {
    setError("");
    setSaving(true);
    try {
      if (isEdit) {
        const patch: { email?: string; password?: string; ban_duration?: string } = {};
        if (email !== user!.email) patch.email = email;
        if (password) patch.password = password;
        const wasBanned = isBanned(user!);
        if (banned !== wasBanned) patch.ban_duration = banned ? "infinity" : "none";
        await backend.updateUser(user!.id, patch);
      } else {
        if (!email) { setError("Email is required."); setSaving(false); return; }
        if (!password) { setError("Password is required."); setSaving(false); return; }
        await backend.createUser(email, password, emailConfirm);
      }
      onSaved();
      onClose();
    } catch (err: any) {
      setError(err?.body?.message || err?.message || "An error occurred.");
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!user) return;
    const ok = await dialog.confirm(`Delete user "${user.email}"?`, {
      message: "This will permanently remove the user and all their sessions.",
      confirmText: user.email,
      confirmLabel: "Delete user",
    });
    if (!ok) return;
    setSaving(true);
    try {
      await backend.deleteUser(user.id);
      onSaved();
      onClose();
    } catch (err: any) {
      setError(err?.body?.message || err?.message || "Failed to delete user.");
      setSaving(false);
    }
  }

  return (
    <Modal open onClose={onClose} title={isEdit ? "Edit user" : "Create user"}>
      <Modal.Body>
        <VStack gap="4" align="stretch">
          <Field label="Email" htmlFor="user-email">
            <Input
              id="user-email"
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="user@example.com"
            />
          </Field>
          <Field label={isEdit ? "New password" : "Password"} htmlFor="user-password">
            <Input
              id="user-password"
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={isEdit ? "Leave blank to keep current" : "Password"}
            />
          </Field>
          {!isEdit && (
            <Toggle
              checked={emailConfirm}
              onChange={setEmailConfirm}
              label="Confirm email automatically"
            />
          )}
          {isEdit && (
            <Toggle
              checked={banned}
              onChange={setBanned}
              label="Banned"
            />
          )}
          {error && (
            <Text fontSize="xs" color="fg.error">{error}</Text>
          )}
        </VStack>
      </Modal.Body>
      <Modal.Footer>
        {isEdit && (
          <Box flex="1">
            <Button variant="danger-ghost" size="sm" onClick={handleDelete} disabled={saving}>
              Delete
            </Button>
          </Box>
        )}
        <Button variant="ghost" onClick={onClose} disabled={saving}>
          Cancel
        </Button>
        <Button onClick={handleSubmit} loading={saving}>
          {isEdit ? "Save" : "Create user"}
        </Button>
      </Modal.Footer>
    </Modal>
  );
}

// ── UsersPage ─────────────────────────────────────────────────────────────

export function UsersPage() {
  const backend = useBackend();
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [modalOpen, setModalOpen] = useState(false);
  const [selectedUser, setSelectedUser] = useState<AdminUser | null>(null);

  const perPage = 50;

  const load = useCallback(async (p: number) => {
    setLoading(true);
    setLoadError("");
    try {
      const result = await backend.listUsers(p, perPage);
      setUsers(result.users);
      setTotal(result.total);
    } catch (err: any) {
      setLoadError(err?.message || "Failed to load users.");
    } finally {
      setLoading(false);
    }
  }, [backend]);

  useEffect(() => { load(page); }, [load, page]);

  function openCreate() {
    setSelectedUser(null);
    setModalOpen(true);
  }

  function openEdit(user: AdminUser) {
    setSelectedUser(user);
    setModalOpen(true);
  }

  function closeModal() {
    setModalOpen(false);
    setSelectedUser(null);
  }

  function onSaved() {
    load(page);
  }

  const addButton = (
    <Button onClick={openCreate}>
      <Plus size={14} />
      Add user
    </Button>
  );

  return (
    <Box pb="20">
      <HStack justify="space-between" gap="4" pb="6">
        <Text fontSize="sm" color="fg.muted">
          {loading ? "Loading…" : `${total} user${total !== 1 ? "s" : ""}`}
        </Text>
        {addButton}
      </HStack>

      {loadError && (
        <Text fontSize="sm" color="fg.error" pb="4">{loadError}</Text>
      )}

      {!loading && users.length === 0 && !loadError && (
        <EmptyState
          icon={UsersIcon}
          title="No users yet"
          description="Create your first user to get started."
          action={addButton}
        />
      )}

      {users.length > 0 && (
        <VStack gap="0" align="stretch" borderWidth="1px" borderRadius="xl" overflow="hidden">
          {/* Header row */}
          <HStack
            px="4"
            py="2"
            bg="bg.subtle"
            borderBottomWidth="1px"
            borderColor="border"
            fontSize="xs"
            fontWeight="medium"
            color="fg.muted"
          >
            <Text flex="1">Email</Text>
            <Text w="24" textAlign="center">Confirmed</Text>
            <Text w="32">Last sign-in</Text>
            <Text w="28">Created</Text>
            <Text w="20" textAlign="center">Status</Text>
          </HStack>
          {users.map((user) => (
            <HStack
              key={user.id}
              px="4"
              py="3"
              borderBottomWidth="1px"
              borderColor="border"
              _last={{ borderBottomWidth: 0 }}
              cursor="pointer"
              _hover={{ bg: "bg.subtle" }}
              onClick={() => openEdit(user)}
              fontSize="sm"
            >
              <Text flex="1" color="fg" fontFamily="mono" fontSize="xs">
                {user.email}
              </Text>
              <Box w="24" display="flex" justifyContent="center">
                {user.email_confirmed_at ? (
                  <Box as={Check} boxSize="3.5" color="green.500" />
                ) : (
                  <Box as={Minus} boxSize="3.5" color="fg.muted" />
                )}
              </Box>
              <Text w="32" color="fg.muted" fontSize="xs">
                {formatDate(user.last_sign_in_at)}
              </Text>
              <Text w="28" color="fg.muted" fontSize="xs">
                {formatDate(user.created_at)}
              </Text>
              <Box w="20" display="flex" justifyContent="center">
                {isBanned(user) && (
                  <StatusBadge variant="error">Banned</StatusBadge>
                )}
              </Box>
            </HStack>
          ))}
        </VStack>
      )}

      {total > perPage && (
        <HStack justify="center" gap="2" pt="6">
          <Button
            variant="outline"
            size="sm"
            disabled={page === 1}
            onClick={() => setPage((p) => p - 1)}
          >
            Previous
          </Button>
          <Text fontSize="sm" color="fg.muted">
            Page {page} of {Math.ceil(total / perPage)}
          </Text>
          <Button
            variant="outline"
            size="sm"
            disabled={page >= Math.ceil(total / perPage)}
            onClick={() => setPage((p) => p + 1)}
          >
            Next
          </Button>
        </HStack>
      )}

      {modalOpen && (
        <UserModal
          user={selectedUser}
          onClose={closeModal}
          onSaved={onSaved}
        />
      )}
    </Box>
  );
}
```

- [ ] **Step 4: Run the tests to confirm they pass**

```bash
cd dashboard && npm test -- Users
```

Expected: all Users tests pass. If the delete test is flaky due to dialog timing, wrap the confirm-button click in `await screen.findByRole(...)` instead of `screen.queryBy...`.

- [ ] **Step 5: Run the full dashboard test suite**

```bash
cd dashboard && npm test
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add dashboard/src/pages/Users.tsx dashboard/src/pages/Users.test.tsx
git commit -m "feat(dashboard): add UsersPage and UserModal"
```

---

### Task 6: Routes + Sidebar navigation

**Files:**
- Modify: `dashboard/src/console/routes.tsx` — add `usersRoutes()` + wire into `consoleRoutes()`
- Modify: `dashboard/src/components/Sidebar.tsx` — add "Users" nav item

- [ ] **Step 1: Add `usersRoutes` to `dashboard/src/console/routes.tsx`**

Add the lazy import near the other lazy imports at the top:

```ts
const UsersPage = lazy(() => import("../pages/Users").then(m => ({ default: m.UsersPage })));
```

Add the route export function after `providersRoutes`:

```ts
export const usersRoutes = (): RouteObject[] => [
  {
    index: true,
    element: <UsersPage />,
    handle: { title: "Users", description: "Manage authenticated users" } satisfies ConsoleRouteHandle,
  },
];
```

Update `consoleRoutes()` to include the users route after the auth route:

```ts
export function consoleRoutes(): RouteObject[] {
  return [
    ...overviewRoutes(),
    { path: "tables", children: tablesRoutes() },
    { path: "auth", children: authRoutes() },
    { path: "users", children: usersRoutes() },
    { path: "storage", children: storageRoutes() },
    { path: "rpc", children: rpcRoutes() },
    { path: "functions", children: functionsRoutes() },
    { path: "providers", children: providersRoutes() },
  ];
}
```

- [ ] **Step 2: Add "Users" to `dashboard/src/components/Sidebar.tsx`**

Add the `Users` icon to the lucide import:

```ts
import {
  LayoutDashboard,
  Table2,
  Shield,
  Users,
  HardDrive,
  Code2,
  Database,
  Plug,
} from "lucide-react";
```

Add the nav item after the Auth entry:

```ts
const NAV_ITEMS = [
  { to: "/", icon: LayoutDashboard, label: "Overview" },
  { to: "/tables", icon: Table2, label: "Tables" },
  { to: "/auth", icon: Shield, label: "Auth" },
  { to: "/users", icon: Users, label: "Users" },
  { to: "/storage", icon: HardDrive, label: "Storage" },
  { to: "/rpc", icon: Database, label: "Database Functions" },
  { to: "/functions", icon: Code2, label: "Code Functions" },
  { to: "/providers", icon: Plug, label: "Providers" },
] as const;
```

- [ ] **Step 3: Run the full test suite**

```bash
cd dashboard && npm test
```

Expected: all tests pass. Then verify the Go build still compiles:

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add dashboard/src/console/routes.tsx dashboard/src/components/Sidebar.tsx
git commit -m "feat(dashboard): add Users nav item and route"
```
