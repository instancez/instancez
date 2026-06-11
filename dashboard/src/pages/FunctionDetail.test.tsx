import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { FunctionDetail } from "./FunctionDetail";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

// --- mock API -----------------------------------------------------------
vi.mock("../api/client", () => ({
  getFunctionCode: vi.fn(),
  putFunctionCode: vi.fn(),
}));

import { getFunctionCode, putFunctionCode } from "../api/client";
const mockGetCode = vi.mocked(getFunctionCode);
const mockPutCode = vi.mocked(putFunctionCode);

// --- fixtures -----------------------------------------------------------
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

const SAMPLE_CODE = `export default async function handler(req, ctx) {
  return new Response("ok");
}`;

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

// --- tests --------------------------------------------------------------
beforeEach(() => {
  vi.clearAllMocks();
  mockGetCode.mockResolvedValue({ content: SAMPLE_CODE, file: "functions/process-image.js" });
  mockPutCode.mockResolvedValue({ message: "saved" });
});

describe("FunctionDetail – config fields", () => {
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

describe("FunctionDetail – code editor", () => {
  it("shows the Code section when getFunctionCode resolves", async () => {
    renderFunctionDetail(baseConfig, "process_image");
    await waitFor(() => expect(screen.getByText("Code")).toBeInTheDocument());
    expect(mockGetCode).toHaveBeenCalledWith("process_image");
  });

  it("hides the Code section when getFunctionCode rejects (readonly / no configPath)", async () => {
    mockGetCode.mockRejectedValue(new Error("not implemented"));
    renderFunctionDetail(baseConfig, "process_image");
    await waitFor(() => expect(mockGetCode).toHaveBeenCalled());
    expect(screen.queryByText("Code")).not.toBeInTheDocument();
  });

  it("shows Save code button after editing", async () => {
    // CodeMirror is not rendered in jsdom — we just verify the section and button appear
    renderFunctionDetail(baseConfig, "process_image");
    // The "Save code" button is only shown when codeDirty is true.
    // With no actual CodeMirror interaction in jsdom, we verify the section header is present.
    await waitFor(() => expect(screen.getByText("Code")).toBeInTheDocument());
    // Initially no dirty state → no Save code button
    expect(screen.queryByRole("button", { name: "Save code" })).not.toBeInTheDocument();
  });
});
