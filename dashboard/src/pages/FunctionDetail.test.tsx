import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { FunctionDetail } from "./FunctionDetail";
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
  rpc: {},
  functions: {
    process_image: {
      runtime: "node",
      file: "functions/process-image.js",
      auth_required: true,
      timeout: "60s",
    },
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
};

function renderFunctionDetail(config: Config, fnName: string) {
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
      <MemoryRouter initialEntries={[`/functions/${fnName}`]}>
        <DialogProvider>
          <Routes>
            <Route path="/functions/:name" element={<FunctionDetail />} />
          </Routes>
        </DialogProvider>
      </MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("FunctionDetail", () => {
  it("renders function name", () => {
    renderFunctionDetail(baseConfig, "process_image");
    expect(screen.getByText("process_image")).toBeInTheDocument();
  });

  it("renders file path value", () => {
    renderFunctionDetail(baseConfig, "process_image");
    expect(screen.getByDisplayValue("functions/process-image.js")).toBeInTheDocument();
  });

  it("renders timeout value", () => {
    renderFunctionDetail(baseConfig, "process_image");
    expect(screen.getByDisplayValue("60s")).toBeInTheDocument();
  });

  it("shows not-found message for missing function", () => {
    renderFunctionDetail(baseConfig, "nonexistent");
    expect(screen.getByText("Function not found.")).toBeInTheDocument();
  });
});
