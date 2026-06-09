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
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
} as unknown as Config;

function renderFunctions(config: Config) {
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
        <Functions />
      </MemoryRouter>
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
