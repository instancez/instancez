import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent, act } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { TableDetail } from "./TableDetail";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
import { renderWithChakra } from "../test/helpers";
import type { Config, ValidationError, Table } from "../lib/types";
import type { ConsoleBackend } from "../console/backend";

const baseConfig: Config = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
  tables: {
    todos: {
      fields: [
        { name: "id", type: "uuid", primary_key: true, required: true },
        { name: "title", type: "text", required: true },
      ],
      indexes: [{ columns: ["title"], unique: false, where: "" }],
      rls: [],
    },
  },
  auth: null,
  storage: {},
  rpc: {},
  functions: {},
  data: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
};

function renderTableDetail(config: Config, tableName: string, backend: ConsoleBackend = adminBackend) {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    dotenvWritable: false,
    refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
  };
  return renderWithChakra(
    <BackendProvider backend={backend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter initialEntries={[`/tables/${tableName}`]}>
          <DialogProvider>
            <Routes>
              <Route path="/tables/:name" element={<TableDetail />} />
            </Routes>
          </DialogProvider>
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
}

function renderTableDetailNew({
  tableName,
  seed,
  save,
  backend = adminBackend,
}: {
  tableName: string;
  seed: Table;
  save: ReturnType<typeof vi.fn>;
  backend?: ConsoleBackend;
}) {
  const ctx = {
    config: baseConfig,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    dotenvWritable: false,
    refresh: vi.fn(),
    save,
    updateConfig: vi.fn(),
  };
  return renderWithChakra(
    <BackendProvider backend={backend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter initialEntries={[{ pathname: "/tables/new", state: { tableName, seed } }]}>
          <DialogProvider>
            <Routes>
              <Route path="/tables/new" element={<TableDetail />} />
              <Route path="/tables/:name" element={<TableDetail />} />
            </Routes>
          </DialogProvider>
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
}

const SEED: Table = {
  fields: [
    { name: "id", type: "bigserial", primary_key: true },
    { name: "created_at", type: "timestamptz", default: "now()" },
  ],
  indexes: [],
  rls: [],
};

describe("TableDetail", () => {
  it("renders column names for a table with fields", () => {
    renderTableDetail(baseConfig, "todos");
    expect(screen.getAllByText("id").length).toBeGreaterThan(0);
    expect(screen.getAllByText("title").length).toBeGreaterThan(0);
  });

  it("shows not-found message when table does not exist", () => {
    renderTableDetail(baseConfig, "nonexistent");
    expect(screen.getByText("Table not found.")).toBeInTheDocument();
  });

  it("hides Add Index button when canWriteConfig is false", async () => {
    const readOnlyBackend: ConsoleBackend = {
      ...adminBackend,
      capabilities: { ...adminBackend.capabilities, canWriteConfig: false },
    };
    renderTableDetail(baseConfig, "todos", readOnlyBackend);
    await act(async () => {
      fireEvent.click(screen.getByRole("tab", { name: /indexes/i }));
    });
    expect(screen.queryByRole("button", { name: /add index/i })).not.toBeInTheDocument();
  });

  it("shows Add Index button when canWriteConfig is true", async () => {
    renderTableDetail(baseConfig, "todos");
    await act(async () => {
      fireEvent.click(screen.getByRole("tab", { name: /indexes/i }));
    });
    expect(screen.getByRole("button", { name: /add index/i })).toBeInTheDocument();
  });

  it("persists a new table only on save", async () => {
    const save = vi.fn().mockResolvedValue(true);
    renderTableDetailNew({ tableName: "orders", seed: SEED, save });
    // The seed fields should be visible in the editor
    expect(screen.getAllByText("id").length).toBeGreaterThan(0);
    // Nothing is persisted before the user explicitly clicks Save
    expect(save).not.toHaveBeenCalled();
    // Click Save
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
    });
    expect(save).toHaveBeenCalledTimes(1);
    const arg = save.mock.calls[0][0] as Config;
    expect(arg.tables["orders"]).toBeDefined();
  });

  it("discards in-memory table when leaving new mode without saving", () => {
    const save = vi.fn().mockResolvedValue(true);
    const { unmount } = renderTableDetailNew({ tableName: "invoices", seed: SEED, save });
    // Seed fields rendered but save never called
    expect(screen.getAllByText("id").length).toBeGreaterThan(0);
    expect(save).not.toHaveBeenCalled();
    // Simulate navigating away by unmounting
    unmount();
    // Save must still never have been called - no implicit persistence on exit
    expect(save).not.toHaveBeenCalled();
  });
});
