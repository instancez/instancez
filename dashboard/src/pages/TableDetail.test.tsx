import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent, act, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { TableDetail } from "./TableDetail";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import type { SaveOptions } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
import { renderWithChakra } from "../test/helpers";
import type { Config, ValidationError, Table } from "../lib/types";
import type { ConsoleBackend } from "../console/backend";

function assertDefined<T>(value: T | undefined, message: string): asserts value is T {
  if (value === undefined) {
    throw new Error(message);
  }
}

const baseConfig: Config = {
  version: 1,
  project: { name: "P", description: "" },
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
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    cors: { origins: [] },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
  },
  database: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
};

function renderTableDetail(config: Config, tableName: string, backend: ConsoleBackend = adminBackend) {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    dotenvWritable: false, oauthCallbackBase: "",
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
  save: (updated: Config, opts?: SaveOptions) => Promise<boolean>;
  backend?: ConsoleBackend;
}) {
  const ctx = {
    config: baseConfig,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    dotenvWritable: false, oauthCallbackBase: "",
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

const fkConfig: Config = {
  ...baseConfig,
  tables: {
    notes: {
      fields: [
        { name: "id", type: "bigserial", primary_key: true },
        { name: "owner", type: "", foreign_key: { references: "auth.users.id", on_delete: "cascade" } },
        { name: "peer", type: "", foreign_key: { references: "notes.id", on_delete: "cascade" } },
        { name: "typed", type: "uuid", foreign_key: { references: "auth.users.id", on_delete: "cascade" } },
      ],
      indexes: [],
      rls: [],
    },
  },
};

describe("TableDetail foreign keys", () => {
  async function openFields() {
    renderTableDetail(fkConfig, "notes");
    fireEvent.click(screen.getByRole("tab", { name: "Fields" }));
    await screen.findAllByText("owner");
    return screen.getAllByRole("combobox") as HTMLSelectElement[];
  }

  it("offers auth.users.id, not users.id, and selects the existing value", async () => {
    const selects = await openFields();
    const fk = selects.find((s) => s.value === "auth.users.id");
    assertDefined(fk, "owner FK select missing");
    const opts = Array.from(fk.options).map((o) => o.value);
    expect(opts).toContain("auth.users.id");
    expect(opts).not.toContain("users.id");
  });

  it("labels untyped FKs with the referenced type and keeps inferred selected", async () => {
    const selects = await openFields();
    const labels = selects.map((s) => s.options[s.selectedIndex]?.textContent);
    expect(labels).toContain("inferred (uuid)");
    expect(labels).toContain("inferred (bigint)");
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });

  it("pins the resolved type when picked explicitly", async () => {
    const save = vi.fn().mockResolvedValue(true);
    const seed: Table = {
      fields: [{ name: "owner", type: "", foreign_key: { references: "auth.users.id", on_delete: "cascade" } }],
      indexes: [],
      rls: [],
    };
    renderTableDetailNew({ tableName: "t", seed, save });
    await screen.findAllByText("owner");
    const sel = (screen.getAllByRole("combobox") as HTMLSelectElement[]).find((s) => s.value === "");
    assertDefined(sel, "type select missing");
    fireEvent.change(sel, { target: { value: "uuid" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
    });
    const saved = save.mock.calls[0]?.[0] as Config;
    expect(saved.tables.t?.fields[0]?.type).toBe("uuid");
  });

  it("clears an explicit FK type back to inferred as an empty type", async () => {
    const save = vi.fn().mockResolvedValue(true);
    const seed: Table = {
      fields: [{ name: "owner", type: "uuid", foreign_key: { references: "auth.users.id", on_delete: "cascade" } }],
      indexes: [],
      rls: [],
    };
    renderTableDetailNew({ tableName: "t", seed, save });
    await screen.findAllByText("owner");
    const sel = (screen.getAllByRole("combobox") as HTMLSelectElement[]).find((s) => s.value === "uuid");
    assertDefined(sel, "type select missing");
    fireEvent.change(sel, { target: { value: "" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
    });
    const saved = save.mock.calls[0]?.[0] as Config;
    const field = saved.tables.t?.fields[0];
    assertDefined(field, "saved field missing");
    expect(field.type).toBe("");
  });

  it("shows an explicit type that is not in the preset list", async () => {
    const cfg: Config = {
      ...baseConfig,
      tables: {
        t: {
          fields: [{ name: "owner", type: "varchar(63)", foreign_key: { references: "auth.users.id", on_delete: "cascade" } }],
          indexes: [],
          rls: [],
        },
      },
    };
    renderTableDetail(cfg, "t");
    fireEvent.click(screen.getByRole("tab", { name: "Fields" }));
    await screen.findAllByText("owner");
    const values = (screen.getAllByRole("combobox") as HTMLSelectElement[]).map((s) => s.value);
    expect(values).toContain("varchar(63)");
  });

  it("does not mark an untyped FK dirty when inferred is picked again", async () => {
    renderTableDetail(
      { ...baseConfig, tables: { t: { fields: [{ name: "owner", type: "", foreign_key: { references: "nope.id", on_delete: "cascade" } }], indexes: [], rls: [] } } },
      "t",
    );
    fireEvent.click(screen.getByRole("tab", { name: "Fields" }));
    await screen.findAllByText("owner");
    const sel = (screen.getAllByRole("combobox") as HTMLSelectElement[]).find((s) => s.value === "");
    assertDefined(sel, "type select missing");
    fireEvent.change(sel, { target: { value: "" } });
    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
  });
});

describe("TableDetail", () => {
  it("renders column names on the Fields tab", async () => {
    renderTableDetail(baseConfig, "todos");
    fireEvent.click(screen.getByRole("tab", { name: "Fields" }));
    await screen.findAllByText("title");
    expect(screen.getAllByText("id").length).toBeGreaterThan(0);
    expect(screen.getAllByText("title").length).toBeGreaterThan(0);
  });

  it("shows not-found message when table does not exist", () => {
    renderTableDetail(baseConfig, "nonexistent");
    expect(screen.getByText("Table not found.")).toBeInTheDocument();
  });

  it("opens on the Data tab and queries the table ordered by primary key", async () => {
    const runQuery = vi.fn(async () => ({ columns: ["id", "title"], rows: [["1", "buy milk"]], row_count: 1 }));
    renderTableDetail(baseConfig, "todos", { ...adminBackend, runQuery });
    expect(await screen.findByText("buy milk")).toBeInTheDocument();
    expect(runQuery).toHaveBeenCalledWith('SELECT * FROM "todos" ORDER BY "id" LIMIT 21 OFFSET 0');
  });

  it("does not render or fetch a migration preview", () => {
    const getConfigDiff = vi.fn();
    renderTableDetail(baseConfig, "todos", { ...adminBackend, getConfigDiff });
    expect(screen.queryByText("Migration Preview")).not.toBeInTheDocument();
    expect(getConfigDiff).not.toHaveBeenCalled();
  });

  it("has no Data tab for a table that is not saved yet", () => {
    renderTableDetailNew({ tableName: "orders", seed: SEED, save: vi.fn() });
    expect(screen.queryByRole("tab", { name: "Data" })).not.toBeInTheDocument();
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
    renderTableDetailNew({ tableName: "orders", seed: { ...SEED, rls_enabled: true }, save });
    // The seed fields should be visible in the editor
    expect(screen.getAllByText("id").length).toBeGreaterThan(0);
    // Nothing is persisted before the user explicitly clicks Save
    expect(save).not.toHaveBeenCalled();
    // Click Save
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
    });
    expect(save).toHaveBeenCalledTimes(1);
    const arg = save.mock.calls[0]![0] as Config;
    const orders = arg.tables["orders"];
    assertDefined(orders, "orders table missing");
    expect(orders.rls_enabled).toBe(true);
  });

  it("keeps rls_enabled when a policy is added before saving", async () => {
    const save = vi.fn().mockResolvedValue(true);
    renderTableDetailNew({ tableName: "orders", seed: { ...SEED, rls_enabled: true }, save });
    await act(async () => {
      fireEvent.click(screen.getByRole("tab", { name: /rls/i }));
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /add rls policy/i }));
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /save/i }));
    });
    const call = save.mock.calls[0];
    assertDefined(call, "save was not called");
    const saved = (call[0] as Config).tables["orders"];
    assertDefined(saved, "orders table missing");
    expect(saved.rls_enabled).toBe(true);
    expect(saved.rls).toHaveLength(1);
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
