import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
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

function renderRpcDetail(config: Config, fnName: string, save = vi.fn().mockResolvedValue(true)) {
  const ctx = {
    config,
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

  it("frames the body editor with the generated function signature", () => {
    renderRpcDetail(baseConfig, "get_todos");
    // Whitespace (incl. the per-clause newlines) is normalized by Testing
    // Library, so one regex spans the multi-line signature the migrator emits.
    expect(
      screen.getByText(
        /CREATE OR REPLACE FUNCTION public\."get_todos"\("user_id" uuid\)\s+RETURNS setof todos\s+LANGUAGE sql\s+STABLE\s+SECURITY INVOKER\s+AS \$ub\$/
      )
    ).toBeInTheDocument();
    expect(screen.getByText("$ub$;")).toBeInTheDocument();
  });

  it("keeps the save bar when the save is cancelled in the confirm dialog", async () => {
    // save resolving false is what a cancelled ConfirmSaveDialog produces
    const save = vi.fn().mockResolvedValue(false);
    renderRpcDetail(baseConfig, "get_todos", save);
    fireEvent.change(screen.getByDisplayValue("setof todos"), { target: { value: "text" } });
    fireEvent.click(screen.getByRole("button", { name: /save changes/i }));
    await vi.waitFor(() => expect(save).toHaveBeenCalled());
    expect(screen.getByRole("button", { name: /save changes/i })).toBeInTheDocument();
  });

  it("hides the save bar again when an edit is undone", () => {
    renderRpcDetail(baseConfig, "get_todos");
    const returns = screen.getByDisplayValue("setof todos");
    expect(screen.queryByRole("button", { name: /save changes/i })).not.toBeInTheDocument();
    fireEvent.change(returns, { target: { value: "text" } });
    expect(screen.getByRole("button", { name: /save changes/i })).toBeInTheDocument();
    fireEvent.change(returns, { target: { value: "setof todos" } });
    expect(screen.queryByRole("button", { name: /save changes/i })).not.toBeInTheDocument();
  });
});
