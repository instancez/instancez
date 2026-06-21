import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { Tables } from "./Tables";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
import type { Config, ValidationError } from "../lib/types";
import type { ConsoleBackend } from "../console/backend";

// Hoisted spy so the vi.mock factory can close over it
const mockNavigate = vi.hoisted(() => vi.fn());

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

// Stub useDialog so prompt resolves without DOM interaction
vi.mock("../components/Dialog", async () => {
  const actual = await vi.importActual<typeof import("../components/Dialog")>("../components/Dialog");
  return {
    ...actual,
    useDialog: () => ({
      prompt: vi.fn().mockResolvedValue("orders"),
      confirm: vi.fn().mockResolvedValue(true),
      alert: vi.fn().mockResolvedValue(undefined),
    }),
  };
});

const baseConfig: Config = {
  version: 1,
  project: { name: "Test", description: "" },
  extensions: [],
  tables: {},
  auth: null,
  storage: {},
  rpc: {},
  functions: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
  },
  database: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
};

function renderTables(config: Config, backend: ConsoleBackend = adminBackend) {
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
        <MemoryRouter>
          <DialogProvider>
            <Tables />
          </DialogProvider>
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
}

describe("Tables", () => {
  it("shows empty state when no tables", () => {
    renderTables(baseConfig);
    expect(screen.getByText("No tables yet")).toBeInTheDocument();
  });

  it("renders table list sorted alphabetically", () => {
    const config = {
      ...baseConfig,
      tables: {
        zebras: { fields: [{ name: "id", type: "bigserial", primary_key: true }], indexes: [], rls: [] },
        apples: { fields: [{ name: "id", type: "bigserial", primary_key: true }, { name: "name", type: "text" }], indexes: [], rls: [] },
      },
    };
    renderTables(config);

    const buttons = screen.getAllByRole("button").filter((b) => b.textContent?.includes("field"));
    expect(buttons[0]!.textContent).toContain("apples");
    expect(buttons[1]!.textContent).toContain("zebras");
  });

  it("shows field count badges", () => {
    const config = {
      ...baseConfig,
      tables: {
        todos: {
          fields: [
            { name: "id", type: "bigserial", primary_key: true },
            { name: "title", type: "text" },
            { name: "done", type: "boolean" },
          ],
          indexes: [],
          rls: [],
        },
      },
    };
    renderTables(config);
    expect(screen.getByText("3 fields")).toBeInTheDocument();
  });

  it("shows RLS badge when RLS policies exist", () => {
    const config = {
      ...baseConfig,
      tables: {
        todos: {
          fields: [{ name: "id", type: "bigserial", primary_key: true }],
          indexes: [],
          rls: [{ operations: ["select"], check: "true" }],
        },
      },
    };
    renderTables(config);
    expect(screen.getByText("1 RLS")).toBeInTheDocument();
  });


  it("shows table count in description", () => {
    const config = {
      ...baseConfig,
      tables: {
        a: { fields: [], indexes: [], rls: [] },
        b: { fields: [], indexes: [], rls: [] },
      },
    };
    renderTables(config);
    expect(screen.getByText("2 tables defined")).toBeInTheDocument();
  });

  it("hides Add Table button when canWriteConfig is false", () => {
    const config = {
      ...baseConfig,
      tables: {
        todos: { fields: [{ name: "id", type: "bigserial", primary_key: true }], indexes: [], rls: [] },
      },
    };
    const readOnlyBackend: ConsoleBackend = {
      ...adminBackend,
      capabilities: { ...adminBackend.capabilities, canWriteConfig: false },
    };
    renderTables(config, readOnlyBackend);
    expect(screen.queryByRole("button", { name: /add table/i })).not.toBeInTheDocument();
  });

  it("shows Add Table button when canWriteConfig is true", () => {
    const config = {
      ...baseConfig,
      tables: {
        todos: { fields: [{ name: "id", type: "bigserial", primary_key: true }], indexes: [], rls: [] },
      },
    };
    renderTables(config);
    expect(screen.getByRole("button", { name: /add table/i })).toBeInTheDocument();
  });

  it("does not save when adding a table; navigates to new mode", async () => {
    const save = vi.fn();
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
    mockNavigate.mockClear();
    renderWithChakra(
      <BackendProvider backend={adminBackend}>
        <ConfigContext.Provider value={ctx}>
          <MemoryRouter>
            <DialogProvider>
              <Tables />
            </DialogProvider>
          </MemoryRouter>
        </ConfigContext.Provider>
      </BackendProvider>
    );
    await act(async () => {
      // baseConfig has no tables so both the toolbar button and EmptyState action render
      fireEvent.click(screen.getAllByRole("button", { name: /add table/i })[0]!);
    });
    expect(save).not.toHaveBeenCalled();
    expect(mockNavigate).toHaveBeenCalledWith(
      "new",
      expect.objectContaining({
        state: expect.objectContaining({ tableName: "orders" }),
      }),
    );
  });
});
