import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { RpcDetail } from "./RpcDetail";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig: Config = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
  tables: {},
  auth: null,
  storage: {},
  rpc: {
    get_todos: {
      description: "",
      auth_required: false,
      language: "sql",
      volatility: "stable",
      security: "invoker",
      args: [{ name: "user_id", type: "uuid", required: false }],
      body: "SELECT * FROM todos WHERE user_id = $1",
      returns: { type: "setof todos" },
    },
    no_args_fn: {
      description: "",
      auth_required: false,
      language: "sql",
      volatility: "immutable",
      security: "invoker",
      args: [],
      body: "SELECT 'hello'",
      returns: { type: "text" },
    },
  },
  functions: {},
  seeds: {},
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

function renderRpcDetail(config: Config, fnName: string) {
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
      <MemoryRouter initialEntries={[`/rpc/${fnName}`]}>
        <DialogProvider>
          <Routes>
            <Route path="/rpc/:name" element={<RpcDetail />} />
          </Routes>
        </DialogProvider>
      </MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("RpcDetail", () => {
  it("renders function name in the page", () => {
    renderRpcDetail(baseConfig, "get_todos");
    expect(screen.getByText("get_todos")).toBeInTheDocument();
  });

  it("renders argument name", () => {
    renderRpcDetail(baseConfig, "get_todos");
    expect(screen.getByText("user_id")).toBeInTheDocument();
  });

  it("renders return type", () => {
    renderRpcDetail(baseConfig, "get_todos");
    expect(screen.getByDisplayValue("setof todos")).toBeInTheDocument();
  });

  it("handles RPC with no arguments", () => {
    renderRpcDetail(baseConfig, "no_args_fn");
    expect(screen.getByText("No arguments defined.")).toBeInTheDocument();
  });

  it("shows not-found message for missing function", () => {
    renderRpcDetail(baseConfig, "nonexistent");
    expect(screen.getByText("Function not found.")).toBeInTheDocument();
  });
});
