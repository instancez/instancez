import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Tables } from "./Tables";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig: Config = {
  project: { name: "Test", description: "" },
  extensions: [],
  tables: {},
  auth: null,
  storage: {},
  functions: {},
  on: {},
  seeds: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
  },
};

function renderTables(config: Config) {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
  };
  return render(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter>
        <DialogProvider>
          <Tables />
        </DialogProvider>
      </MemoryRouter>
    </ConfigContext.Provider>
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
        zebras: { fields: { id: { type: "bigserial", primary_key: true } }, indexes: [], rls: [], searchable: [], search_config: "" },
        apples: { fields: { id: { type: "bigserial", primary_key: true }, name: { type: "text" } }, indexes: [], rls: [], searchable: [], search_config: "" },
      },
    };
    renderTables(config as any);

    const buttons = screen.getAllByRole("button").filter((b) => b.textContent?.includes("field"));
    expect(buttons[0]!.textContent).toContain("apples");
    expect(buttons[1]!.textContent).toContain("zebras");
  });

  it("shows field count badges", () => {
    const config = {
      ...baseConfig,
      tables: {
        todos: {
          fields: { id: { type: "bigserial", primary_key: true }, title: { type: "text" }, done: { type: "boolean" } },
          indexes: [],
          rls: [],
          
          searchable: [],
          search_config: "",
        },
      },
    };
    renderTables(config as any);
    expect(screen.getByText("3 fields")).toBeInTheDocument();
  });

  it("shows RLS badge when RLS policies exist", () => {
    const config = {
      ...baseConfig,
      tables: {
        todos: {
          fields: { id: { type: "bigserial", primary_key: true } },
          indexes: [],
          rls: [{ operations: ["select"], check: "true" }],
          
          searchable: [],
          search_config: "",
        },
      },
    };
    renderTables(config as any);
    expect(screen.getByText("1 RLS")).toBeInTheDocument();
  });


  it("shows table count in description", () => {
    const config = {
      ...baseConfig,
      tables: {
        a: { fields: {}, indexes: [], rls: [], searchable: [], search_config: "" },
        b: { fields: {}, indexes: [], rls: [], searchable: [], search_config: "" },
      },
    };
    renderTables(config as any);
    expect(screen.getByText("2 tables defined")).toBeInTheDocument();
  });
});
