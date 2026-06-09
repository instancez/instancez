# Dashboard Consistency Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the dashboard accurately model `rpc:` (SQL) vs `functions:` (code), hide Settings, drop row-count clutter, and ship a storage bucket in the `init` scaffold.

**Architecture:** Frontend (React 19 + Vite + TS, tested with vitest). The admin API round-trips `domain.Config` verbatim, so TS `Config` must carry both `rpc` and `functions` with their true shapes. Plus one Go scaffold change in `internal/cli/init.go`.

**Tech Stack:** React, react-router-dom v7, Tailwind v4, lucide-react, vitest; Go (cobra) for `init`.

Run `npm` commands from `dashboard/`. Run `go` commands from repo root.

---

### Task 1: Types — split `rpc` and `functions`

**Files:**
- Modify: `dashboard/src/lib/types.ts`

- [ ] **Step 1: Rename `FunctionDef` → `RpcFunction`, add `CodeFunction`, update `Config`**

In `dashboard/src/lib/types.ts`:

Change the `Config` interface's function fields:
```ts
  storage: Record<string, Bucket>;
  rpc: Record<string, RpcFunction>;
  functions: Record<string, CodeFunction>;
  seeds: Record<string, Record<string, unknown>[]>;
```

Rename the existing `FunctionDef` interface to `RpcFunction` (body unchanged):
```ts
export interface RpcFunction {
  description: string;
  auth_required: boolean;
  language: string;
  volatility: string;
  security: string;
  args: FuncArg[];
  body: string;
  returns: FuncReturn;
}
```

Add, after it:
```ts
export interface CodeFunction {
  runtime: string;
  file: string;
  auth_required: boolean;
  timeout?: string;
  env?: Record<string, string>;
}
```

- [ ] **Step 2: Typecheck**

Run: `cd dashboard && npx tsc -b --noEmit`
Expected: errors only in files that reference `FunctionDef` / `config.functions` as SQL (those are fixed in later tasks). Note them; do not fix here.

- [ ] **Step 3: Commit**

```bash
git add dashboard/src/lib/types.ts
git commit -m "feat(dashboard): split rpc and functions config types"
```

---

### Task 2: SQL RPC pages (rename existing Functions editor → Rpc)

The current `Functions.tsx`/`FunctionDetail.tsx` are already the SQL RPC editor; they only point at the wrong key.

**Files:**
- Rename: `dashboard/src/pages/Functions.tsx` → `dashboard/src/pages/Rpc.tsx`
- Rename: `dashboard/src/pages/FunctionDetail.tsx` → `dashboard/src/pages/RpcDetail.tsx`

- [ ] **Step 1: Move the files**

```bash
git mv dashboard/src/pages/Functions.tsx dashboard/src/pages/Rpc.tsx
git mv dashboard/src/pages/FunctionDetail.tsx dashboard/src/pages/RpcDetail.tsx
```

- [ ] **Step 2: Rewrite `Rpc.tsx`**

- Rename the export: `export function Functions()` → `export function Rpc()`.
- Replace every `config.functions` with `config.rpc` (read list, add, save). Use `config.rpc || {}` for the read.
- Update `import type { FunctionDef ... }` usages: change to `RpcFunction`.
- Navigation paths `/functions/...` → `/rpc/...`.
- Copy: PageHeader title `"Database Functions"`, description `${n} SQL function${...}`; EmptyState title `"No SQL functions yet"`, description `"Create Postgres functions exposed at /rest/v1/rpc."`; button label `"Add Function"`.

Concretely, the list/add body becomes:
```tsx
  const functions = Object.entries(config.rpc || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  async function addFunction() {
    const name = await dialog.prompt("Function name:");
    if (!name?.trim()) return;
    const fnName = name.trim().toLowerCase().replace(/\s+/g, "_");
    const updated = {
      ...config!,
      rpc: {
        ...(config!.rpc || {}),
        [fnName]: {
          description: "",
          auth_required: true,
          language: "plpgsql",
          volatility: "volatile",
          security: "invoker",
          args: [],
          body: "BEGIN\n  -- function body\nEND;",
          returns: { type: "void" },
        },
      },
    };
    const ok = await save(updated);
    if (ok) navigate(`/rpc/${fnName}`);
  }
```
and `navigate(\`/rpc/${name}\`)` in the row click.

- [ ] **Step 3: Rewrite `RpcDetail.tsx`**

- Rename export `FunctionDetail` → `RpcDetail`.
- `import type { RpcFunction, FuncArg }`; change `useState<FunctionDef | null>` → `useState<RpcFunction | null>`, `(prev: FunctionDef)` → `(prev: RpcFunction)`.
- All `config.functions` → `config.rpc || {}` for read; on save/delete write back under `rpc`:
```tsx
  useEffect(() => {
    if (config && name && (config.rpc || {})[name]) {
      setFn(structuredClone((config.rpc || {})[name]!));
      setDirty(false);
    }
  }, [config, name]);

  async function handleSave() {
    if (!config || !fn || !name) return;
    const updated = { ...config, rpc: { ...(config.rpc || {}), [name]: fn } };
    await save(updated);
    setDirty(false);
  }

  async function deleteFunction() {
    if (!config || !name) return;
    if (!(await dialog.confirm(`Delete function "${name}"?`, { message: "This will permanently remove the function endpoint.", confirmText: name }))) return;
    const { [name]: _omit, ...rest } = config.rpc || {};
    const updated = { ...config, rpc: rest };
    const ok = await save(updated);
    if (ok) navigate("/rpc");
  }
```
- Back button `navigate("/functions")` → `navigate("/rpc")`.
- The `/rest/v1/rpc/${name}` test pane stays unchanged (correct for SQL RPC).

- [ ] **Step 4: Typecheck**

Run: `cd dashboard && npx tsc -b --noEmit`
Expected: remaining errors only in `App.tsx` (imports) — fixed in Task 4.

- [ ] **Step 5: Commit**

```bash
git add dashboard/src/pages/Rpc.tsx dashboard/src/pages/RpcDetail.tsx
git commit -m "feat(dashboard): repoint SQL function editor at config.rpc"
```

---

### Task 3: Code (edge) function pages (new, metadata-only)

**Files:**
- Create: `dashboard/src/pages/Functions.tsx`
- Create: `dashboard/src/pages/FunctionDetail.tsx`
- Create: `dashboard/src/pages/Functions.test.tsx`

- [ ] **Step 1: Write `Functions.tsx` (list)**

```tsx
import { useNavigate } from "react-router-dom";
import { Code2 } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { PageHeader } from "../components/PageHeader";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";

export function Functions() {
  const { config } = useConfig();
  const navigate = useNavigate();
  if (!config) return null;

  const functions = Object.entries(config.functions || {}).sort(([a], [b]) =>
    a.localeCompare(b)
  );

  return (
    <div>
      <PageHeader
        title="Edge Functions"
        description={`${functions.length} code function${functions.length !== 1 ? "s" : ""}`}
      />
      <div className="px-8">
        {functions.length === 0 ? (
          <EmptyState
            icon={Code2}
            title="No edge functions"
            description="Declare a function in ultrabase.yaml with a runtime and a .js file under functions/ (served at /functions/v1/<name>)."
          />
        ) : (
          <div className="space-y-2">
            {functions.map(([name, fn]) => (
              <button
                key={name}
                onClick={() => navigate(`/functions/${name}`)}
                className="w-full flex items-center justify-between px-5 py-3.5 rounded-lg border border-border bg-surface hover:bg-surface-hover hover:border-border-hover transition-colors cursor-pointer text-left group"
              >
                <div className="flex items-center gap-3">
                  <Code2 size={16} className="text-muted-foreground group-hover:text-foreground transition-colors" />
                  <span className="text-sm font-mono font-medium text-foreground">{name}</span>
                  <span className="text-xs font-mono text-muted-foreground">{fn.file}</span>
                </div>
                <div className="flex items-center gap-2">
                  <StatusBadge variant="info">{fn.runtime || "node"}</StatusBadge>
                  {fn.auth_required && <StatusBadge variant="info">auth</StatusBadge>}
                  {fn.timeout && <StatusBadge variant="muted">{fn.timeout}</StatusBadge>}
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Write `FunctionDetail.tsx` (metadata editor)**

```tsx
import { useParams, useNavigate } from "react-router-dom";
import { useState, useEffect } from "react";
import { ArrowLeft, Trash2, Plus } from "lucide-react";
import { useConfig } from "../hooks/useConfig";
import { useDialog } from "../components/Dialog";
import { PageHeader } from "../components/PageHeader";
import { SaveBar } from "../components/SaveBar";
import type { CodeFunction } from "../lib/types";

export function FunctionDetail() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { config, save, saving, saveErrors } = useConfig();
  const dialog = useDialog();
  const [fn, setFn] = useState<CodeFunction | null>(null);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (config && name && (config.functions || {})[name]) {
      setFn(structuredClone((config.functions || {})[name]!));
      setDirty(false);
    }
  }, [config, name]);

  function updateFn(updater: (prev: CodeFunction) => CodeFunction) {
    setFn((prev) => {
      if (!prev) return prev;
      setDirty(true);
      return updater(prev);
    });
  }

  async function handleSave() {
    if (!config || !fn || !name) return;
    const updated = { ...config, functions: { ...(config.functions || {}), [name]: fn } };
    await save(updated);
    setDirty(false);
  }

  async function deleteFunction() {
    if (!config || !name) return;
    if (!(await dialog.confirm(`Delete function "${name}"?`, { message: "Removes the config entry. The .js file is left on disk.", confirmText: name }))) return;
    const { [name]: _omit, ...rest } = config.functions || {};
    const ok = await save({ ...config, functions: rest });
    if (ok) navigate("/functions");
  }

  if (!config || !fn || !name) {
    return (
      <div className="p-8">
        <p className="text-sm text-muted-foreground">Function not found.</p>
      </div>
    );
  }

  const envEntries = Object.entries(fn.env || {});

  return (
    <div className="pb-20">
      <PageHeader
        title={name}
        description="Code function served at /functions/v1/"
        actions={
          <div className="flex items-center gap-2">
            <button
              onClick={() => navigate("/functions")}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
            >
              <ArrowLeft size={14} />
              Back
            </button>
            <button
              onClick={deleteFunction}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-destructive/30 text-sm text-destructive hover:bg-destructive/10 transition-colors cursor-pointer"
            >
              <Trash2 size={14} />
              Delete
            </button>
          </div>
        }
      />

      <div className="px-8 space-y-6 max-w-3xl">
        <p className="text-sm text-muted-foreground">
          Edit the handler source in <span className="font-mono text-foreground">{fn.file}</span>.
        </p>

        <div className="grid grid-cols-2 gap-4">
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Runtime</label>
            <input
              type="text"
              value={fn.runtime || ""}
              onChange={(e) => updateFn((f) => ({ ...f, runtime: e.target.value }))}
              placeholder="node"
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">File</label>
            <input
              type="text"
              value={fn.file || ""}
              onChange={(e) => updateFn((f) => ({ ...f, file: e.target.value }))}
              placeholder="functions/name.js"
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-muted-foreground mb-1">Timeout</label>
            <input
              type="text"
              value={fn.timeout || ""}
              onChange={(e) => updateFn((f) => ({ ...f, timeout: e.target.value }))}
              placeholder="30s"
              className="w-full px-3 py-2 rounded-lg border border-border bg-input text-sm font-mono text-foreground focus:outline-none focus:border-ring transition-colors"
            />
          </div>
          <div className="flex items-end">
            <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer pb-2">
              <input
                type="checkbox"
                checked={fn.auth_required}
                onChange={(e) => updateFn((f) => ({ ...f, auth_required: e.target.checked }))}
                className="rounded border-border"
              />
              Auth required
            </label>
          </div>
        </div>

        <div>
          <div className="flex items-center justify-between mb-3">
            <label className="text-sm font-medium text-foreground">Environment</label>
            <button
              onClick={async () => {
                const key = await dialog.prompt("Env variable name:");
                if (!key?.trim()) return;
                updateFn((f) => ({ ...f, env: { ...(f.env || {}), [key.trim()]: "" } }));
              }}
              className="inline-flex items-center gap-1 px-2 py-1 rounded border border-dashed border-border text-xs text-muted-foreground hover:text-foreground hover:border-border-hover transition-colors cursor-pointer"
            >
              <Plus size={12} />
              Add Var
            </button>
          </div>
          {envEntries.length > 0 ? (
            <div className="space-y-2">
              {envEntries.map(([key, val]) => (
                <div key={key} className="flex items-center gap-3 px-3 py-2 rounded-lg border border-border bg-primary">
                  <span className="text-sm font-mono text-foreground min-w-[140px]">{key}</span>
                  <input
                    type="text"
                    value={val}
                    onChange={(e) =>
                      updateFn((f) => ({ ...f, env: { ...(f.env || {}), [key]: e.target.value } }))
                    }
                    className="flex-1 px-2 py-1 rounded border border-border bg-input text-xs font-mono text-foreground focus:outline-none focus:border-ring"
                  />
                  <button
                    onClick={() =>
                      updateFn((f) => {
                        const next = { ...(f.env || {}) };
                        delete next[key];
                        return { ...f, env: next };
                      })
                    }
                    className="p-1 rounded hover:bg-destructive/10 text-muted-foreground hover:text-destructive transition-colors cursor-pointer"
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No environment variables.</p>
          )}
        </div>
      </div>

      <SaveBar onSave={handleSave} saving={saving} errors={saveErrors} dirty={dirty} />
    </div>
  );
}
```

- [ ] **Step 3: Write `Functions.test.tsx`**

```tsx
import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Functions } from "./Functions";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
  tables: {},
  auth: null,
  storage: {},
  rpc: {},
  functions: {
    todos: { runtime: "node", file: "functions/todos.js", auth_required: true, timeout: "30s" },
  },
  seeds: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080, max_body_size: "10MB", max_limit: 1000, docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
} as unknown as Config;

function renderFunctions(config: Config) {
  const ctx = {
    config, loading: false, error: null, checksum: "abc", saving: false,
    saveErrors: [] as ValidationError[], refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true), updateConfig: vi.fn(),
  };
  return render(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter><Functions /></MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("Functions (edge)", () => {
  it("lists code functions with file and runtime", () => {
    renderFunctions(baseConfig);
    expect(screen.getByText("Edge Functions")).toBeInTheDocument();
    expect(screen.getByText("todos")).toBeInTheDocument();
    expect(screen.getByText("functions/todos.js")).toBeInTheDocument();
    expect(screen.getByText("node")).toBeInTheDocument();
  });

  it("shows empty state when no functions", () => {
    renderFunctions({ ...baseConfig, functions: {} });
    expect(screen.getByText("No edge functions")).toBeInTheDocument();
  });
});
```

- [ ] **Step 4: Run the new test**

Run: `cd dashboard && npx vitest run src/pages/Functions.test.tsx`
Expected: 2 passing.

- [ ] **Step 5: Commit**

```bash
git add dashboard/src/pages/Functions.tsx dashboard/src/pages/FunctionDetail.tsx dashboard/src/pages/Functions.test.tsx
git commit -m "feat(dashboard): add edge (code) functions surface"
```

---

### Task 4: Sidebar + App routing

**Files:**
- Modify: `dashboard/src/components/Sidebar.tsx`
- Modify: `dashboard/src/App.tsx`

- [ ] **Step 1: Sidebar — two function entries, drop Settings**

In `Sidebar.tsx` imports, replace `Settings` with `Database`:
```tsx
import {
  LayoutDashboard,
  Table2,
  Shield,
  HardDrive,
  Code2,
  Database,
  Plug,
  ExternalLink,
} from "lucide-react";
```

Replace `NAV_ITEMS`:
```tsx
const NAV_ITEMS = [
  { to: "/", icon: LayoutDashboard, label: "Overview" },
  { to: "/tables", icon: Table2, label: "Tables" },
  { to: "/auth", icon: Shield, label: "Auth" },
  { to: "/storage", icon: HardDrive, label: "Storage" },
  { to: "/rpc", icon: Database, label: "Database Functions" },
  { to: "/functions", icon: Code2, label: "Edge Functions" },
  { to: "/providers", icon: Plug, label: "Providers" },
] as const;
```

- [ ] **Step 2: App.tsx — wire routes, drop Settings**

Replace the lazy imports block (lines 12-21) so it includes Rpc/RpcDetail and the new Functions/FunctionDetail, and drops Settings:
```tsx
const Overview = lazy(() => import("./pages/Overview").then((m) => ({ default: m.Overview })));
const Tables = lazy(() => import("./pages/Tables").then((m) => ({ default: m.Tables })));
const TableDetail = lazy(() => import("./pages/TableDetail").then((m) => ({ default: m.TableDetail })));
const AuthPage = lazy(() => import("./pages/Auth").then((m) => ({ default: m.AuthPage })));
const Storage = lazy(() => import("./pages/Storage").then((m) => ({ default: m.Storage })));
const StorageDetail = lazy(() => import("./pages/StorageDetail").then((m) => ({ default: m.StorageDetail })));
const Rpc = lazy(() => import("./pages/Rpc").then((m) => ({ default: m.Rpc })));
const RpcDetail = lazy(() => import("./pages/RpcDetail").then((m) => ({ default: m.RpcDetail })));
const Functions = lazy(() => import("./pages/Functions").then((m) => ({ default: m.Functions })));
const FunctionDetail = lazy(() => import("./pages/FunctionDetail").then((m) => ({ default: m.FunctionDetail })));
const ProvidersPage = lazy(() => import("./pages/Providers").then((m) => ({ default: m.ProvidersPage })));
```

Replace the route list (the `<Route ...>` lines for functions/settings) with:
```tsx
            <Route path="rpc" element={<Rpc />} />
            <Route path="rpc/:name" element={<RpcDetail />} />
            <Route path="functions" element={<Functions />} />
            <Route path="functions/:name" element={<FunctionDetail />} />
            <Route path="providers" element={<ProvidersPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
```
(Remove the `<Route path="settings" ... />` line and the `SettingsPage` lazy import.)

- [ ] **Step 3: Typecheck**

Run: `cd dashboard && npx tsc -b --noEmit`
Expected: PASS (no errors).

- [ ] **Step 4: Commit**

```bash
git add dashboard/src/components/Sidebar.tsx dashboard/src/App.tsx
git commit -m "feat(dashboard): nav for database vs edge functions, drop settings nav"
```

---

### Task 5: Remove Settings page; move "Download YAML" to Overview

**Files:**
- Delete: `dashboard/src/pages/Settings.tsx`, `dashboard/src/pages/Settings.test.tsx`
- Modify: `dashboard/src/pages/Overview.tsx`

- [ ] **Step 1: Delete Settings files**

```bash
git rm dashboard/src/pages/Settings.tsx dashboard/src/pages/Settings.test.tsx
```

- [ ] **Step 2: Add Download button to Overview header**

In `Overview.tsx`, add imports:
```tsx
import { getStats, getStatus, getConfig } from "../api/client";
import { downloadYamlFromConfig } from "../lib/downloadYaml";
```
Add a handler inside the component (after `loadData`):
```tsx
  async function handleDownload() {
    const cfg = await getConfig();
    downloadYamlFromConfig(cfg);
  }
```
Replace the PageHeader `actions` with a two-button group (Download + Refresh):
```tsx
        actions={
          <div className="flex items-center gap-2">
            <button
              onClick={handleDownload}
              className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
            >
              Download config as YAML
            </button>
            <button
              onClick={loadData}
              disabled={loading}
              className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg border border-border text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
            >
              <RefreshCw size={14} className={loading ? "animate-spin" : ""} />
              Refresh
            </button>
          </div>
        }
```

- [ ] **Step 3: Typecheck + run Overview test (still expected to fail on row-count test — fixed in Task 6)**

Run: `cd dashboard && npx tsc -b --noEmit`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add dashboard/src/pages/Overview.tsx
git commit -m "feat(dashboard): remove Settings page, move YAML download to Overview"
```

---

### Task 6: Overview — drop row-count metrics

**Files:**
- Modify: `dashboard/src/pages/Overview.tsx`
- Modify: `dashboard/src/pages/Overview.test.tsx`

- [ ] **Step 1: Update the test first (TDD)**

In `Overview.test.tsx`, replace the "shows table detail rows with row counts" test with:
```tsx
  it("shows table detail rows with field counts", async () => {
    renderOverview();
    await waitFor(() => {
      expect(screen.getByText("todos")).toBeInTheDocument();
      expect(screen.getByText("2 fields")).toBeInTheDocument();
    });
  });
```
Add `rpc: {}` to `baseConfig` (alongside `functions: {}`) so it matches the `Config` type.

- [ ] **Step 2: Run test — expect the field-count test to pass, but ensure no row-count assertions remain**

Run: `cd dashboard && npx vitest run src/pages/Overview.test.tsx`
Expected: still references "42 total rows"? No — current page still renders row counts, so this passes already. Proceed to remove rendering.

- [ ] **Step 3: Remove `totalRows` and row-count rendering in `Overview.tsx`**

Delete the `totalRows` computation:
```tsx
  const totalRows = stats
    ? Object.values(stats.tables).reduce((sum, t) => sum + t.row_count, 0)
    : 0;
```
Remove the Tables card subtitle paragraph:
```tsx
            <p className="mt-1 text-xs text-muted-foreground">
              {formatNumber(totalRows)} total rows
            </p>
```
In the Tables detail list, remove the trailing row-count span:
```tsx
                  <span className="text-sm text-muted-foreground tabular-nums">
                    {formatNumber(stats.tables[name]?.row_count ?? 0)} rows
                  </span>
```
Remove `formatNumber` from the utils import (keep `formatBytes`):
```tsx
import { formatBytes } from "../lib/utils";
```

- [ ] **Step 4: Run tests + typecheck**

Run: `cd dashboard && npx tsc -b --noEmit && npx vitest run src/pages/Overview.test.tsx`
Expected: typecheck PASS; Overview tests PASS.

- [ ] **Step 5: Commit**

```bash
git add dashboard/src/pages/Overview.tsx dashboard/src/pages/Overview.test.tsx
git commit -m "feat(dashboard): drop row-count metrics from Overview"
```

---

### Task 7: Init scaffold — add storage bucket

**Files:**
- Modify: `internal/cli/init.go` (`scaffoldYAML`)
- Modify: `internal/cli/init_test.go`

- [ ] **Step 1: Add a failing test assertion**

In `internal/cli/init_test.go`, in the test that parses the scaffold (where `cfg.Functions["todos"]` is asserted, around line 101), add:
```go
	bucket, ok := cfg.Storage["avatars"]
	require.True(t, ok, "scaffold should declare the avatars storage bucket")
	assert.True(t, bucket.Public, "avatars bucket should be public")
	assert.Equal(t, "5MB", bucket.MaxSize)
```

- [ ] **Step 2: Run it — expect failure**

Run: `go test ./internal/cli/ -run TestInit -count=1`
Expected: FAIL — `cfg.Storage["avatars"]` not found.

- [ ] **Step 3: Add the storage block to `scaffoldYAML`**

In `init.go`, insert before the `# Code functions:` comment (after the `tables:` block's closing rls lines) :
```go
# Storage buckets: file uploads served at /storage/v1/object/<bucket>/<path>.
# Access is governed by RLS, exactly like tables.
storage:
  avatars:
    public: true          # objects are world-readable by URL
    max_size: 5MB
    types: [image/*]
    rls:
      # Only signed-in users may upload, replace, or remove avatars.
      - operations: [insert, update, delete]
        check: "auth.is_authenticated()"

```
(Place it as a literal segment inside the `scaffoldYAML` format string, between the `tables:` block and the `functions:` block.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cli/ -run TestInit -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/init.go internal/cli/init_test.go
git commit -m "feat(cli): scaffold a storage bucket in the init example"
```

---

### Task 8: Full verification

- [ ] **Step 1: Dashboard suite**

Run: `cd dashboard && npm test`
Expected: all green. If any test referenced old `/functions` SQL behavior or `Settings`, fix it.

- [ ] **Step 2: Go build + cli tests**

Run: `go build ./... && go test -race ./internal/cli/...`
Expected: PASS.

- [ ] **Step 3: Grep for stragglers**

Run: `cd dashboard && grep -rn "FunctionDef\|SettingsPage\|/settings\|total rows" src`
Expected: no matches.

- [ ] **Step 4: Final commit (only if fixes were needed)**

```bash
git add -A && git commit -m "test(dashboard): finalize consistency pass"
```

---

## Self-Review

- **Spec coverage:** Types (T1) ✓; SQL RPC surface (T2) ✓; code-function surface (T3) ✓; sidebar/routing (T4) ✓; remove Settings + relocate download (T5) ✓; Overview row-count removal (T6) ✓; init storage scaffold (T7) ✓; verification incl. CLAUDE.md non-negotiables (T8) ✓.
- **Placeholders:** none — all steps carry concrete code/commands.
- **Type consistency:** `RpcFunction`/`CodeFunction`/`config.rpc`/`config.functions` used identically across T1–T6; nil-map guards (`|| {}`) applied everywhere config maps are read.
