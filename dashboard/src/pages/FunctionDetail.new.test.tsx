import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { FunctionDetail } from "./FunctionDetail";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
import type { Config, ValidationError, CodeFunction } from "../lib/types";
import type { ConsoleBackend } from "../console/backend";

// New mode navigates on success; spy on it without a landing route.
const mockNavigate = vi.hoisted(() => vi.fn());
vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return { ...actual, useNavigate: () => mockNavigate };
});

vi.mock("../api/client", () => ({
  getFunctionCode: vi.fn(),
  putFunctionCode: vi.fn(),
  checkFunctionFile: vi.fn(),
  getFunctionDeps: vi.fn(),
}));
import { getFunctionCode } from "../api/client";
const mockGetCode = vi.mocked(getFunctionCode);

const baseConfig: Config = {
  version: 1,
  project: { name: "P", description: "" },
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
    cors: { origins: [] },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
  },
  database: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
};

const SEED: CodeFunction = { runtime: "node", file: "functions/orders.js", auth_required: false };
const SEED_CODE = "export default async function handler(req, ctx) { return { status: 200 }; }";

function renderNew(
  backendOverrides: Partial<ConsoleBackend> = {},
  saveErrors: ValidationError[] = [],
  ctxOverrides: Partial<{ refresh: () => Promise<void> }> = {}
) {
  const backend: ConsoleBackend = { ...adminBackend, ...backendOverrides };
  const ctx = {
    config: baseConfig,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors,
    dotenvWritable: false, oauthCallbackBase: "",
    refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
    ...ctxOverrides,
  };
  renderWithChakra(
    <BackendProvider backend={backend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter
          initialEntries={[
            { pathname: "/functions/new", state: { functionName: "orders", seed: SEED, code: SEED_CODE } },
          ]}
        >
          <DialogProvider>
            <Routes>
              <Route path="/functions/new" element={<FunctionDetail />} />
              <Route path="/functions/:name" element={<FunctionDetail />} />
            </Routes>
          </DialogProvider>
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
  return ctx;
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("FunctionDetail – new mode", () => {
  it("seeds the form from router state without fetching code", async () => {
    renderNew();
    expect(screen.getByDisplayValue("functions/orders.js")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText("Code")).toBeInTheDocument());
    expect(mockGetCode).not.toHaveBeenCalled();
  });

  it("on save calls createFunction once then refreshes config before navigating", async () => {
    const user = userEvent.setup();
    const createFunction = vi.fn().mockResolvedValue(undefined);
    const refresh = vi.fn().mockResolvedValue(undefined);
    renderNew({ createFunction }, [], { refresh });
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(createFunction).toHaveBeenCalledTimes(1));
    expect(createFunction).toHaveBeenCalledWith(
      "orders",
      expect.objectContaining({ functions: expect.objectContaining({ orders: SEED }) }),
      SEED_CODE,
      expect.any(String),
    );
    await waitFor(() => expect(mockNavigate).toHaveBeenCalled());
    expect(refresh).toHaveBeenCalledTimes(1);
    // refresh must land before navigate, or the destination sees stale config.
    expect(refresh.mock.invocationCallOrder[0]!).toBeLessThan(
      mockNavigate.mock.invocationCallOrder[0]!
    );
    expect(mockNavigate).toHaveBeenCalledWith("../orders", { relative: "path", replace: true });
  });

  it("shows a clear message on a 409 conflict from createFunction", async () => {
    const user = userEvent.setup();
    const err: any = new Error("conflict");
    err.status = 409;
    err.body = { error: "conflict", current_version: 7 };
    const createFunction = vi.fn().mockRejectedValue(err);
    renderNew({ createFunction });
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    expect(
      await screen.findByText(/the draft changed since you started/i)
    ).toBeInTheDocument();
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it("surfaces server validation errors and does not navigate", async () => {
    const user = userEvent.setup();
    const err: any = new Error("bad"); err.body = { errors: [{ path: "functions.orders", message: "bad name" }] };
    const createFunction = vi.fn().mockRejectedValue(err);
    renderNew({ createFunction });
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    expect(await screen.findByText(/bad name/i)).toBeInTheDocument();
    expect(mockNavigate).not.toHaveBeenCalled();
  });
});
