# Console Package Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the dashboard a consumable console package (`@instancez/console`) whose pages talk to a `ConsoleBackend` interface, so the embedded instance dashboard and the instancez-platform web app render the same UI over different write semantics — including a first-class secrets write-back interface.

**Architecture:** Introduce a `ConsoleBackend` interface + React context inside `dashboard/src/console/`. The default implementation (`adminBackend`) delegates to the existing `api/client.ts` module functions, so all existing `vi.mock("../api/client")` test setups keep working unchanged. Pages/components migrate from direct `api/client` imports to `useBackend()`. A `capabilities` object on the backend gates surfaces per consumer. Finally `dashboard/package.json` gains an `exports` entry so the platform web app can depend on it via `file:`.

**Tech Stack:** React 18, Vite, vitest + testing-library, Tailwind (preset export). No new runtime deps.

**Companion plan:** `instancez-platform/v2/docs/superpowers/plans/2026-06-12-platform-console-mount.md` (secrets store + endpoints + app-page mount). Execute this plan first.

---

### Task 1: Define `ConsoleBackend` + `Capabilities` types

**Files:**
- Create: `dashboard/src/console/backend.ts`
- Test: `dashboard/src/console/backend.test.ts`

- [ ] **Step 1: Write the failing test** (a conformance helper that any backend implementation must pass; here it just type-checks and validates the capability defaults helper)

```ts
// dashboard/src/console/backend.test.ts
import { describe, it, expect } from "vitest";
import { fullCapabilities, type ConsoleBackend } from "./backend";

describe("ConsoleBackend types", () => {
  it("fullCapabilities enables every surface", () => {
    const caps = fullCapabilities();
    expect(caps.canWriteConfig).toBe(true);
    expect(caps.canWriteSecrets).toBe(true);
    expect(caps.canEditFunctionCode).toBe(true);
    expect(caps.canManageDeps).toBe(true);
    expect(caps.hasStats).toBe(true);
  });

  it("a stub satisfies the interface", () => {
    // Compile-time check that the interface is implementable.
    const stub: ConsoleBackend = {
      capabilities: fullCapabilities(),
      getConfig: async () => ({ version: 1 }) as any,
      getConfigStatus: async () => ({ dotenv_writable: false }) as any,
      previewConfig: async () => ({ current: "", proposed: "" }),
      putConfig: async () => ({ message: "" }),
      getEnvVars: async () => ({ vars: {} }),
      writeSecrets: async () => ({ message: "" }),
      getKeys: async () => ({ anon_key: "" }),
      getStats: async () => ({ tables: {}, storage: {} }) as any,
      getConfigDiff: async () => ({ statements: [], is_destructive: false }) as any,
      getFunctionCode: async () => ({ content: "", file: "" }),
      putFunctionCode: async () => ({ message: "" }),
      checkFunctionFile: async () => ({ exists: true }),
      getFunctionDeps: async () => ({ dependencies: {}, has_lock: false, readonly: true }),
      postFunctionDeps: async () => ({ dependencies: {}, has_lock: false, readonly: true }),
    };
    expect(stub.capabilities.canWriteConfig).toBe(true);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd dashboard && npx vitest run src/console/backend.test.ts`
Expected: FAIL — `Failed to resolve import "./backend"`

- [ ] **Step 3: Write the implementation**

```ts
// dashboard/src/console/backend.ts
import type { Config, ConfigStatus, StatsResponse, DiffResponse } from "../lib/types";
import type { EnvVarsResponse, ConfigPreview } from "../api/client";

/** What this consumer/deployment supports. Pages gate surfaces on these. */
export interface Capabilities {
  /** Config edits can be saved (instance: dashboard readwrite; platform: version+deploy). */
  canWriteConfig: boolean;
  /** Secrets can be written back (instance: dotenv file; platform: secret store + env sync). */
  canWriteSecrets: boolean;
  /** Function source files are editable (instance: local FS; platform: version artifact). */
  canEditFunctionCode: boolean;
  /** npm dependencies can be changed (instance: npm on host; platform: build pipeline). */
  canManageDeps: boolean;
  /** Live stats (row counts, storage usage) are available. */
  hasStats: boolean;
}

export function fullCapabilities(): Capabilities {
  return {
    canWriteConfig: true,
    canWriteSecrets: true,
    canEditFunctionCode: true,
    canManageDeps: true,
    hasStats: true,
  };
}

/**
 * Everything the console UI needs from "the backend". The instance dashboard
 * implements this against the admin API; instancez-platform implements it
 * against platform APIs (config saves create a new version, secrets go to the
 * platform store and sync to the runtime environment).
 *
 * Method shapes intentionally mirror api/client.ts so the admin adapter is a
 * pass-through and existing tests that mock ../api/client stay valid.
 */
export interface ConsoleBackend {
  capabilities: Capabilities;

  // config
  getConfig(): Promise<Config>;
  getConfigStatus(): Promise<ConfigStatus>;
  previewConfig(config: Omit<Config, "_checksum">): Promise<ConfigPreview>;
  putConfig(
    config: Omit<Config, "_checksum">,
    checksum: string
  ): Promise<{ message: string; config_source?: string }>;

  // secrets — THE write-back interface. Values are write-only; reads are
  // existence-only via getEnvVars.
  getEnvVars(names?: string[]): Promise<EnvVarsResponse>;
  writeSecrets(vars: Record<string, string>): Promise<{ message: string }>;

  // keys / stats / diff
  getKeys(): Promise<{ anon_key: string }>;
  getStats(): Promise<StatsResponse>;
  getConfigDiff(): Promise<DiffResponse>;

  // code functions
  getFunctionCode(name: string): Promise<{ content: string; file: string }>;
  putFunctionCode(name: string, content: string): Promise<{ message: string }>;
  checkFunctionFile(file: string): Promise<{ exists: boolean }>;
  getFunctionDeps(): Promise<{ dependencies: Record<string, string>; has_lock: boolean; readonly: boolean }>;
  postFunctionDeps(
    add: string[],
    remove: string[]
  ): Promise<{ dependencies: Record<string, string>; has_lock: boolean; readonly: boolean }>;
}
```

Note: if `getKeys`, `getStats`, `getConfigDiff`, `getFunctionDeps`, or `postFunctionDeps` have different return shapes in `dashboard/src/api/client.ts`, mirror the client's actual signatures — the client module is the source of truth for shapes in this task.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd dashboard && npx vitest run src/console/backend.test.ts`
Expected: PASS (2 tests)

- [ ] **Step 5: Commit**

```bash
git add dashboard/src/console/backend.ts dashboard/src/console/backend.test.ts
git commit -m "feat(console): define ConsoleBackend interface and capabilities"
```

---

### Task 2: `adminBackend` adapter + `BackendProvider` context

**Files:**
- Create: `dashboard/src/console/adminBackend.ts`
- Create: `dashboard/src/console/BackendContext.tsx`
- Test: `dashboard/src/console/adminBackend.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
// dashboard/src/console/adminBackend.test.tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { adminBackend } from "./adminBackend";
import { BackendProvider, useBackend } from "./BackendContext";
import * as api from "../api/client";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<typeof api>();
  return { ...real, getEnvVars: vi.fn().mockResolvedValue({ vars: { X: { set: true } } }) };
});

describe("adminBackend", () => {
  it("delegates to the api/client module (so vi.mock keeps intercepting)", async () => {
    const resp = await adminBackend.getEnvVars(["X"]);
    expect(resp.vars.X.set).toBe(true);
    expect(api.getEnvVars).toHaveBeenCalledWith(["X"]);
  });

  it("advertises full capabilities", () => {
    expect(adminBackend.capabilities.canWriteSecrets).toBe(true);
  });

  it("useBackend defaults to adminBackend and can be overridden", () => {
    function Probe() {
      const b = useBackend();
      return <span>{b.capabilities.canWriteConfig ? "yes" : "no"}</span>;
    }
    render(<Probe />);
    expect(screen.getByText("yes")).toBeInTheDocument();

    const custom = { ...adminBackend, capabilities: { ...adminBackend.capabilities, canWriteConfig: false } };
    render(
      <BackendProvider backend={custom}>
        <Probe />
      </BackendProvider>
    );
    expect(screen.getByText("no")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd dashboard && npx vitest run src/console/adminBackend.test.tsx`
Expected: FAIL — imports unresolved

- [ ] **Step 3: Write the implementation**

```ts
// dashboard/src/console/adminBackend.ts
import * as api from "../api/client";
import { fullCapabilities, type ConsoleBackend } from "./backend";

/**
 * The instance-dashboard backend: a pass-through to the admin API client.
 * IMPORTANT: delegate via the module namespace (api.fn(...)) — not by
 * destructured references — so test suites that vi.mock("../api/client")
 * keep intercepting calls.
 */
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
};
```

```tsx
// dashboard/src/console/BackendContext.tsx
import { createContext, useContext, type ReactNode } from "react";
import type { ConsoleBackend } from "./backend";
import { adminBackend } from "./adminBackend";

const BackendContext = createContext<ConsoleBackend>(adminBackend);

/** Consumers (the platform app) inject their backend here; the instance
 * dashboard relies on the adminBackend default and needs no provider. */
export function BackendProvider({ backend, children }: { backend: ConsoleBackend; children: ReactNode }) {
  return <BackendContext.Provider value={backend}>{children}</BackendContext.Provider>;
}

export function useBackend(): ConsoleBackend {
  return useContext(BackendContext);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd dashboard && npx vitest run src/console/adminBackend.test.tsx`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add dashboard/src/console/
git commit -m "feat(console): adminBackend adapter and BackendProvider context"
```

---

### Task 3: Route `useConfig` through the backend

**Files:**
- Modify: `dashboard/src/hooks/useConfig.ts`
- Test: existing `dashboard/src/hooks/useConfig.test.tsx` must stay green unchanged

`useConfigState` currently imports `getConfig`, `getConfigStatus`, `putConfig`, `previewConfig` from `../api/client`. Replace those four call sites with `backend.*` obtained via `useBackend()`.

- [ ] **Step 1: Make the change**

In `dashboard/src/hooks/useConfig.ts`:
- Add `import { useBackend } from "../console/BackendContext";`
- Remove the `getConfig, getConfigStatus, putConfig, previewConfig` import from `../api/client`.
- Inside `useConfigState()`, first line: `const backend = useBackend();`
- Replace `getConfig()` → `backend.getConfig()`, `getConfigStatus()` → `backend.getConfigStatus()`, `previewConfig(body)` → `backend.previewConfig(body)`, `putConfig(body, checksum)` → `backend.putConfig(body, checksum)`.
- Add `backend` to the dependency arrays of `refresh` and `save` (`useCallback`).

- [ ] **Step 2: Run the existing hook tests unchanged**

Run: `cd dashboard && npx vitest run src/hooks/useConfig.test.tsx`
Expected: PASS (3 tests) — they mock `../api/client`, and adminBackend delegates through the module, so the mocks still intercept.

- [ ] **Step 3: Run the full dashboard suite**

Run: `cd dashboard && npm test`
Expected: all tests pass (196+ at time of writing).

- [ ] **Step 4: Commit**

```bash
git add dashboard/src/hooks/useConfig.ts
git commit -m "refactor(console): useConfig consumes ConsoleBackend"
```

---

### Task 4: Migrate all remaining direct `api/client` call sites in pages/components

**Files:**
- Modify (each follows the identical pattern below):

| File | Functions to migrate |
|---|---|
| `dashboard/src/pages/Providers.tsx` | `getEnvVars`, `putDotenv`→`writeSecrets` |
| `dashboard/src/pages/Auth.tsx` | `getEnvVars`, `putDotenv`→`writeSecrets` |
| `dashboard/src/pages/Overview.tsx` | `getStats`, `getConfig` (download handler) |
| `dashboard/src/pages/Functions.tsx` | `getFunctionDeps`, `postFunctionDeps` |
| `dashboard/src/pages/FunctionDetail.tsx` | `getFunctionCode`, `putFunctionCode`, `checkFunctionFile` |
| `dashboard/src/pages/TableDetail.tsx` | `getConfigDiff` |
| `dashboard/src/components/ApiKeys.tsx` | `getKeys` (inside `useAnonKey`) |

**Pattern (identical for every file):**
1. Remove the named import from `../api/client` (or `./api/client` relative path as appropriate).
2. Add `import { useBackend } from "../console/BackendContext";` (adjust relative path: `./BackendContext` becomes `../console/BackendContext` from `pages/`, `../console/BackendContext` from `components/`).
3. Inside the component (or hook), `const backend = useBackend();`
4. Replace each call: `getEnvVars(x)` → `backend.getEnvVars(x)`; `putDotenv(v)` → `backend.writeSecrets(v)`; etc.
5. Where a call sits inside a `useCallback`, add `backend` to its deps array.

- [ ] **Step 1: Migrate `Providers.tsx` per the pattern; run `npx vitest run src/pages/Providers.test.tsx` — expect PASS unchanged**
- [ ] **Step 2: Migrate `Auth.tsx`; run `npx vitest run src/pages/Auth.test.tsx` — expect PASS unchanged**
- [ ] **Step 3: Migrate `Overview.tsx`; run `npx vitest run src/pages/Overview.test.tsx` — expect PASS unchanged**
- [ ] **Step 4: Migrate `Functions.tsx`; run `npx vitest run src/pages/Functions.test.tsx` — expect PASS unchanged**
- [ ] **Step 5: Migrate `FunctionDetail.tsx`; run `npx vitest run src/pages/FunctionDetail.test.tsx` — expect PASS unchanged**
- [ ] **Step 6: Migrate `TableDetail.tsx`; run `npx vitest run src/pages/TableDetail.test.tsx` — expect PASS unchanged**
- [ ] **Step 7: Migrate `ApiKeys.tsx` (`useAnonKey` becomes a hook that calls `useBackend()` then `backend.getKeys()`); run `npx vitest run src/components/ApiKeys.test.tsx src/components/ConnectExamples.test.tsx 2>/dev/null || npx vitest run src/components/ApiKeys.test.tsx` — expect PASS unchanged**
- [ ] **Step 8: Verify zero remaining page/component imports**

Run: `cd dashboard && grep -rn "from \"../api/client\"\|from \"./api/client\"" src/pages src/components | grep -v test`
Expected: no output. (`src/hooks`, `src/console`, and `src/api` itself are the only importers left.)

- [ ] **Step 9: Full suite + build**

Run: `cd dashboard && npm test && npm run build`
Expected: all tests pass; tsc/vite build green.

- [ ] **Step 10: Commit**

```bash
git add dashboard/src
git commit -m "refactor(console): all pages consume ConsoleBackend instead of api/client"
```

---

### Task 5: Capability gating

**Files:**
- Modify: `dashboard/src/pages/FunctionDetail.tsx` (code editor + file check sections)
- Modify: `dashboard/src/pages/Functions.tsx` (deps manager section)
- Modify: `dashboard/src/pages/Providers.tsx`, `dashboard/src/pages/Auth.tsx` (secrets inputs)
- Test: `dashboard/src/console/capabilities.test.tsx` (new)

Pages currently gate on runtime signals (`dotenvWritable`, `getFunctionCode` failure). Combine with capabilities: a surface renders writable only when `capability && runtime signal`.

- [ ] **Step 1: Write the failing test**

```tsx
// dashboard/src/console/capabilities.test.tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { BackendProvider } from "./BackendContext";
import { adminBackend } from "./adminBackend";
import { ProvidersPage } from "../pages/Providers";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<any>();
  return { ...real, getEnvVars: vi.fn().mockResolvedValue({ vars: {} }) };
});

const config = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [], tables: {}, auth: null, storage: {}, rpc: {}, functions: {}, data: {},
  providers: {
    email: { type: "resend", api_key: "${INSTANCEZ_RESEND_API_KEY}", default_from_email: "" },
    storage: null,
  },
  server: {
    port: 8080, max_body_size: "10MB", max_limit: 1000, docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
} as unknown as Config;

function renderWithCaps(canWriteSecrets: boolean) {
  const backend = {
    ...adminBackend,
    capabilities: { ...adminBackend.capabilities, canWriteSecrets },
  };
  const ctx = {
    config, loading: false, error: null, checksum: "abc", saving: false,
    saveErrors: [] as ValidationError[], dotenvWritable: true,
    refresh: vi.fn(), save: vi.fn().mockResolvedValue(true), updateConfig: vi.fn(),
  };
  render(
    <BackendProvider backend={backend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter>
          <ProvidersPage />
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
}

describe("capability gating", () => {
  it("hides secret inputs when the backend cannot write secrets, even if dotenvWritable", () => {
    renderWithCaps(false);
    expect(screen.queryByLabelText("INSTANCEZ_RESEND_API_KEY")).not.toBeInTheDocument();
  });

  it("shows secret inputs when the backend can write secrets", () => {
    renderWithCaps(true);
    expect(screen.getByLabelText("INSTANCEZ_RESEND_API_KEY")).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify the first case fails**

Run: `cd dashboard && npx vitest run src/console/capabilities.test.tsx`
Expected: FAIL — input rendered despite `canWriteSecrets: false`

- [ ] **Step 3: Implement gating**

In `Providers.tsx` and `Auth.tsx`, where `dotenvWritable` is read from `useConfig()`, derive:

```ts
const backend = useBackend(); // already present after Task 4
const canWriteSecrets = backend.capabilities.canWriteSecrets && dotenvWritable;
```

and pass `canWriteSecrets` (instead of `dotenvWritable`) to every `VarRow canWrite=` prop and the staged-dotenv save logic.

In `Functions.tsx`, the deps manager's `canEdit` becomes `backend.capabilities.canManageDeps && deps !== null && !deps.readonly`.

In `FunctionDetail.tsx`, the Code section renders only when `backend.capabilities.canEditFunctionCode && code !== null`; the file-existence check in `handleSave` runs only when `backend.capabilities.canEditFunctionCode` (the platform validates file paths in its own pipeline).

- [ ] **Step 4: Run the new test and the full suite**

Run: `cd dashboard && npx vitest run src/console/capabilities.test.tsx && npm test`
Expected: PASS, full suite green.

- [ ] **Step 5: Commit**

```bash
git add dashboard/src
git commit -m "feat(console): capability gating for secrets, code editing, and deps"
```

---

### Task 6: `consoleRoutes()` + `ConsoleProvider` (mountable subtree)

**Files:**
- Create: `dashboard/src/console/ConsoleProvider.tsx`
- Create: `dashboard/src/console/routes.tsx`
- Create: `dashboard/src/console/index.ts`
- Modify: `dashboard/src/components/Layout.tsx` (consume ConsoleProvider)
- Test: `dashboard/src/console/routes.test.tsx`

- [ ] **Step 1: Write the failing test** — mounting the console standalone under a MemoryRouter with an injected backend renders the Providers page:

```tsx
// dashboard/src/console/routes.test.tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useRoutes } from "react-router-dom";
import { consoleRoutes } from "./routes";
import { ConsoleProvider } from "./ConsoleProvider";
import { adminBackend } from "./adminBackend";
import * as api from "../api/client";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<typeof api>();
  return {
    ...real,
    getConfig: vi.fn().mockResolvedValue({
      version: 1, project: { name: "P", description: "" },
      extensions: [], tables: {}, auth: null, storage: {}, rpc: {}, functions: {},
      data: {}, providers: { email: null, storage: null },
      server: {
        port: 8080, max_body_size: "10MB", max_limit: 1000, docs_ui: true,
        cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
        timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
        db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
      },
      _checksum: "abc",
    }),
    getConfigStatus: vi.fn().mockResolvedValue({ dotenv_writable: false }),
    getEnvVars: vi.fn().mockResolvedValue({ vars: {} }),
  };
});

function Console() {
  return useRoutes(consoleRoutes());
}

describe("consoleRoutes", () => {
  it("mounts the providers page standalone with an injected backend", async () => {
    render(
      <MemoryRouter initialEntries={["/providers"]}>
        <ConsoleProvider backend={adminBackend}>
          <Console />
        </ConsoleProvider>
      </MemoryRouter>
    );
    await waitFor(() => expect(screen.getByText("Email Provider")).toBeInTheDocument());
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd dashboard && npx vitest run src/console/routes.test.tsx`
Expected: FAIL — modules unresolved

- [ ] **Step 3: Implement**

`ConsoleProvider` composes what `Layout.tsx` does today minus the shell chrome: `BackendProvider` + `ConfigContext.Provider` (via `useConfigState`) + `DialogProvider` + `ConfirmSaveDialog` + `SaveToast`:

```tsx
// dashboard/src/console/ConsoleProvider.tsx
import { type ReactNode } from "react";
import { BackendProvider } from "./BackendContext";
import type { ConsoleBackend } from "./backend";
import { ConfigContext, useConfigState } from "../hooks/useConfig";
import { DialogProvider } from "../components/Dialog";
import { ConfirmSaveDialog } from "../components/ConfirmSaveDialog";
import { SaveToast } from "../components/SaveToast";

function ConfigShell({ children }: { children: ReactNode }) {
  const configState = useConfigState();
  return (
    <ConfigContext.Provider value={configState}>
      <DialogProvider>
        {children}
        <SaveToast />
        {configState.pendingSave && (
          <ConfirmSaveDialog
            current={configState.pendingSave.current}
            proposed={configState.pendingSave.proposed}
            dotenvChanges={configState.pendingSave.dotenvChanges}
            saving={configState.saving}
            onConfirm={configState.confirmPendingSave}
            onCancel={configState.cancelPendingSave}
          />
        )}
      </DialogProvider>
    </ConfigContext.Provider>
  );
}

export function ConsoleProvider({ backend, children }: { backend: ConsoleBackend; children: ReactNode }) {
  return (
    <BackendProvider backend={backend}>
      <ConfigShell>{children}</ConfigShell>
    </BackendProvider>
  );
}
```

`routes.tsx` exports **per-area route fragments** using **relative paths**, plus `consoleRoutes()` composing them all. Fragments are the unit a host app mounts per tab — each keeps its own list→detail navigation working wherever it's nested:

```tsx
// dashboard/src/console/routes.tsx
import type { RouteObject } from "react-router-dom";
import { Overview } from "../pages/Overview";
import { Tables } from "../pages/Tables";
import { TableDetail } from "../pages/TableDetail";
import { AuthPage } from "../pages/Auth";
import { Storage } from "../pages/Storage";
import { StorageDetail } from "../pages/StorageDetail";
import { Rpc } from "../pages/Rpc";
import { RpcDetail } from "../pages/RpcDetail";
import { Functions } from "../pages/Functions";
import { FunctionDetail } from "../pages/FunctionDetail";
import { ProvidersPage } from "../pages/Providers";

// Per-area fragments: a host mounts one of these under its own tab route,
// e.g. useRoutes(tablesRoutes()) under apps/:id/tables/*.
export const overviewRoutes = (): RouteObject[] => [{ index: true, element: <Overview /> }];
export const tablesRoutes = (): RouteObject[] => [
  { index: true, element: <Tables /> },
  { path: ":name", element: <TableDetail /> },
];
export const authRoutes = (): RouteObject[] => [{ index: true, element: <AuthPage /> }];
export const storageRoutes = (): RouteObject[] => [
  { index: true, element: <Storage /> },
  { path: ":name", element: <StorageDetail /> },
];
export const rpcRoutes = (): RouteObject[] => [
  { index: true, element: <Rpc /> },
  { path: ":name", element: <RpcDetail /> },
];
export const functionsRoutes = (): RouteObject[] => [
  { index: true, element: <Functions /> },
  { path: ":name", element: <FunctionDetail /> },
];
export const providersRoutes = (): RouteObject[] => [{ index: true, element: <ProvidersPage /> }];

// The full console (used by the embedded instance dashboard).
export function consoleRoutes(): RouteObject[] {
  return [
    ...overviewRoutes(),
    { path: "tables", children: tablesRoutes() },
    { path: "auth", children: authRoutes() },
    { path: "storage", children: storageRoutes() },
    { path: "rpc", children: rpcRoutes() },
    { path: "functions", children: functionsRoutes() },
    { path: "providers", children: providersRoutes() },
  ];
}
```

One navigation detail: detail pages call `navigate("/tables")`-style absolute paths (e.g. after delete) — change those to **relative** navigation (`navigate("..")`) so they work under any mount point. Grep: `grep -rn 'navigate("/' dashboard/src/pages` and convert each to the relative equivalent; the dashboard's own tests must stay green after the change.

Check the actual exported component names in each page file before writing this (e.g. `Rpc.tsx` may export `Rpc` or `RpcPage`) and the actual route paths in the app's current router (`dashboard/src/App.tsx` or `main.tsx`) — mirror them exactly.

```ts
// dashboard/src/console/index.ts — the package's public surface
export type { ConsoleBackend, Capabilities } from "./backend";
export { fullCapabilities } from "./backend";
export { adminBackend } from "./adminBackend";
export { BackendProvider, useBackend } from "./BackendContext";
export { ConsoleProvider } from "./ConsoleProvider";
export {
  consoleRoutes,
  overviewRoutes,
  tablesRoutes,
  authRoutes,
  storageRoutes,
  rpcRoutes,
  functionsRoutes,
  providersRoutes,
} from "./routes";
export { EmbeddedChromeProvider } from "./chrome"; // Task 7
// Leaf components platform surfaces reuse outside the console mount:
export { DiffViewer } from "../components/DiffViewer";
export { ConfirmSaveDialog } from "../components/ConfirmSaveDialog";
export { VarRow } from "../components/VarRow";
```

Refactor `Layout.tsx` to use `ConsoleProvider` (it keeps the sidebar/navbar shell, drops its inline ConfigContext/ConfirmSaveDialog wiring — that moved into ConsoleProvider; pass `adminBackend`). The internal route table in the app should be replaced by `consoleRoutes()` nested under the Layout route, preserving current URLs.

- [ ] **Step 4: Run the new test, then the full suite + build**

Run: `cd dashboard && npx vitest run src/console/routes.test.tsx && npm test && npm run build`
Expected: all green. Manually verify `inz dev` dashboard still navigates identically (`npm run dev` smoke or rely on suite).

- [ ] **Step 5: Commit**

```bash
git add dashboard/src
git commit -m "feat(console): mountable consoleRoutes + ConsoleProvider; Layout consumes them"
```

---

### Task 7: Embedded chrome mode (no stitched look in host apps)

**Files:**
- Create: `dashboard/src/console/chrome.tsx`
- Modify: `dashboard/src/components/PageHeader.tsx`
- Modify: page root padding (see step 3)
- Test: `dashboard/src/console/chrome.test.tsx`

When console pages render inside a host app's own tabs, the console's big page headers and page-level padding are what make it look bolted-on. An `EmbeddedChromeProvider` switches pages into a naked-content-pane mode: `PageHeader` collapses to a slim bar that keeps only what the host can't supply (back link on detail pages, the delete/actions row) and drops the title/description; the page root horizontal padding collapses so the host controls gutters.

- [ ] **Step 1: Write the failing test**

```tsx
// dashboard/src/console/chrome.test.tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { EmbeddedChromeProvider } from "./chrome";
import { PageHeader } from "../components/PageHeader";

describe("embedded chrome", () => {
  it("renders the full header by default", () => {
    render(
      <MemoryRouter>
        <PageHeader title="Tables" description="All your tables" />
      </MemoryRouter>
    );
    expect(screen.getByText("Tables")).toBeInTheDocument();
    expect(screen.getByText("All your tables")).toBeInTheDocument();
  });

  it("collapses title/description when embedded but keeps actions", () => {
    const onDelete = vi.fn();
    render(
      <MemoryRouter>
        <EmbeddedChromeProvider>
          <PageHeader title="todos" description="should hide" backTo="/tables" onDelete={onDelete} />
        </EmbeddedChromeProvider>
      </MemoryRouter>
    );
    expect(screen.queryByText("should hide")).not.toBeInTheDocument();
    // back affordance and delete action survive — match PageHeader's actual
    // accessible names (read PageHeader.tsx first and adjust the queries)
    expect(screen.getByRole("button", { name: /delete/i })).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run: `cd dashboard && npx vitest run src/console/chrome.test.tsx` — expect FAIL**

- [ ] **Step 3: Implement**

```tsx
// dashboard/src/console/chrome.tsx
import { createContext, useContext, type ReactNode } from "react";

const EmbeddedContext = createContext(false);

/** Host apps (instancez-platform) wrap console fragments in this so pages
 * render as naked content panes: slim headers, no page-level gutters. */
export function EmbeddedChromeProvider({ children }: { children: ReactNode }) {
  return <EmbeddedContext.Provider value={true}>{children}</EmbeddedContext.Provider>;
}

export function useEmbeddedChrome(): boolean {
  return useContext(EmbeddedContext);
}
```

In `PageHeader.tsx`: `const embedded = useEmbeddedChrome();` — when embedded, render the slim variant (back link + actions row only; skip the `<h1>`/description block, reduce vertical padding). In each page's root `div`, replace the hardcoded `px-8` with a shared helper class driven by the same hook — simplest implementation: a tiny `usePageGutters()` returning `embedded ? "px-0" : "px-8"`, applied in the pages' content wrappers (`grep -rn '"px-8' dashboard/src/pages` for the list).

- [ ] **Step 4: Run: `cd dashboard && npx vitest run src/console/chrome.test.tsx && npm test` — expect PASS, full suite green (default mode unchanged)**

- [ ] **Step 5: Commit**

```bash
git add dashboard/src
git commit -m "feat(console): embedded chrome mode for host-app integration"
```

---

### Task 8: Package exports for `file:` consumption

**Files:**
- Modify: `dashboard/package.json`
- Create: `dashboard/tailwind.preset.js` (export of the existing tailwind theme)
- Test: build-level verification (no unit test)

- [ ] **Step 1: Add package metadata**

In `dashboard/package.json` add (keep existing fields):

```json
{
  "name": "@instancez/console",
  "exports": {
    "./console": "./src/console/index.ts",
    "./styles.css": "./src/index.css",
    "./tailwind-preset": "./tailwind.preset.js"
  },
  "peerDependencies": {
    "react": "^18.0.0",
    "react-dom": "^18.0.0",
    "react-router-dom": "^6.0.0"
  }
}
```

Source-level export (`.ts` directly) is deliberate: the platform's Vite transpiles it, no build step needed for `file:` consumption. Check `react-router-dom`'s actual major version in `dashboard/package.json` dependencies and match the peer range.

- [ ] **Step 2: Extract the tailwind preset** — move the `theme`/`plugins` content of `dashboard/tailwind.config.js` into `dashboard/tailwind.preset.js` (module.exports the shared part); `tailwind.config.js` becomes `{ presets: [require("./tailwind.preset.js")], content: [...] }`.

- [ ] **Step 3: Verify the dashboard still builds and tests pass**

Run: `cd dashboard && npm test && npm run build`
Expected: green (the preset refactor must not change emitted CSS behavior).

- [ ] **Step 4: Commit**

```bash
git add dashboard/package.json dashboard/tailwind.preset.js dashboard/tailwind.config.js
git commit -m "feat(console): package exports + tailwind preset for external consumers"
```

---

### Task 9: Full verification

- [ ] Run `cd dashboard && npm test && npm run build` — all green
- [ ] Run `go build ./... && go test -race ./...` from repo root — all green (no Go changes expected; this guards the embedded-assets build)
- [ ] Run `grep -rn "from \"../api/client\"" dashboard/src/pages dashboard/src/components | grep -v test` — empty
- [ ] Commit any stragglers; the platform plan (`instancez-platform/v2/docs/superpowers/plans/2026-06-12-platform-console-mount.md`) can now execute.
