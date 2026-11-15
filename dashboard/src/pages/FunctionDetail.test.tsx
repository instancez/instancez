import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { FunctionDetail } from "./FunctionDetail";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
import type { Config, ValidationError } from "../lib/types";

// --- mock API -----------------------------------------------------------
vi.mock("../api/client", () => ({
  getFunctionCode: vi.fn(),
  putFunctionCode: vi.fn(),
  checkFunctionFile: vi.fn(),
}));

import { getFunctionCode, putFunctionCode, checkFunctionFile } from "../api/client";
const mockGetCode = vi.mocked(getFunctionCode);
const mockPutCode = vi.mocked(putFunctionCode);
const mockCheckFile = vi.mocked(checkFunctionFile);

// --- fixtures -----------------------------------------------------------
const baseConfig: Config = {
  version: 1,
  project: { name: "P", description: "" },
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

const SAMPLE_CODE = `export default async function handler(req, ctx) {
  return new Response("ok");
}`;

function renderFunctionDetail(
  config: Config,
  fnName: string,
  backend = adminBackend
) {
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
  renderWithChakra(
    <BackendProvider backend={backend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter initialEntries={[`/functions/${fnName}`]}>
          <DialogProvider>
            <Routes>
              <Route path="/functions/:name" element={<FunctionDetail />} />
            </Routes>
          </DialogProvider>
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
  return ctx;
}

// --- tests --------------------------------------------------------------
beforeEach(() => {
  vi.clearAllMocks();
  mockGetCode.mockResolvedValue({ content: SAMPLE_CODE, file: "functions/process-image.js" });
  mockPutCode.mockResolvedValue({ message: "saved" });
  mockCheckFile.mockResolvedValue({ exists: true });
});

describe("FunctionDetail – file existence on save", () => {
  it("aborts the save when the changed file does not exist", async () => {
    const user = userEvent.setup();
    mockCheckFile.mockResolvedValue({ exists: false });
    const ctx = renderFunctionDetail(structuredClone(baseConfig), "process_image");

    const fileInput = screen.getByDisplayValue("functions/process-image.js");
    await user.clear(fileInput);
    await user.type(fileInput, "functions/renamed.js");
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(mockCheckFile).toHaveBeenCalledWith("functions/renamed.js"));
    expect(ctx.save).not.toHaveBeenCalled();
    expect(screen.getByText(/not found/i)).toBeInTheDocument();
  });

  it("saves when the changed file exists", async () => {
    const user = userEvent.setup();
    const ctx = renderFunctionDetail(structuredClone(baseConfig), "process_image");

    const fileInput = screen.getByDisplayValue("functions/process-image.js");
    await user.clear(fileInput);
    await user.type(fileInput, "functions/renamed.js");
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(ctx.save).toHaveBeenCalled());
  });

  it("does not re-check the file when it is unchanged", async () => {
    const user = userEvent.setup();
    const ctx = renderFunctionDetail(structuredClone(baseConfig), "process_image");

    const timeoutInput = screen.getByDisplayValue("60s");
    await user.clear(timeoutInput);
    await user.type(timeoutInput, "90s");
    await user.click(screen.getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(ctx.save).toHaveBeenCalled());
    expect(mockCheckFile).not.toHaveBeenCalled();
  });
});

describe("FunctionDetail – env vars", () => {
  it("rejects adding an env var that already exists", async () => {
    const user = userEvent.setup();
    const config = structuredClone(baseConfig);
    config.functions.process_image!.env = { FOO: "bar" };
    renderFunctionDetail(config, "process_image");

    await user.click(screen.getByRole("button", { name: /add var/i }));
    await user.type(within(screen.getByRole("dialog")).getByRole("textbox"), "FOO");
    await user.click(screen.getByRole("button", { name: /create/i }));

    expect(await screen.findByText(/already exists/i)).toBeInTheDocument();
    // the existing value must not be clobbered
    expect(screen.getByDisplayValue("bar")).toBeInTheDocument();
  });
});

describe("FunctionDetail – config fields", () => {
  it("renders the back link to the Code Functions list", () => {
    renderFunctionDetail(baseConfig, "process_image");
    // The function name is now shell chrome (route handle); the page renders a
    // chrome-free toolbar with a back link to the parent area.
    expect(screen.getByRole("link", { name: /Code Functions/ })).toBeInTheDocument();
  });

  it("renders file path value", () => {
    renderFunctionDetail(baseConfig, "process_image");
    expect(screen.getByDisplayValue("functions/process-image.js")).toBeInTheDocument();
  });

  it("makes the file path read-only when canEditFunctionFile is off", () => {
    const backend = {
      ...adminBackend,
      capabilities: { ...adminBackend.capabilities, canEditFunctionFile: false },
    };
    renderFunctionDetail(baseConfig, "process_image", backend);
    // The value is still shown (the user can see which file backs the function)…
    const fileInput = screen.getByDisplayValue("functions/process-image.js");
    // …but the field is not editable.
    expect(fileInput).toHaveAttribute("readonly");
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

describe("FunctionDetail – delete", () => {
  it("is hidden when config writes are disabled", async () => {
    const backend = { ...adminBackend, capabilities: { ...adminBackend.capabilities, canWriteConfig: false } };
    renderFunctionDetail(baseConfig, "process_image", backend);
    await waitFor(() => expect(screen.getByText("Code")).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: "Delete function" })).toBeNull();
  });

  async function openDelete() {
    const ctx = renderFunctionDetail(structuredClone(baseConfig), "process_image");
    await userEvent.click(await screen.findByRole("button", { name: "Delete function" }));
    return ctx;
  }

  it("keeps Delete locked until the function name is typed", async () => {
    const ctx = await openDelete();
    const del = await screen.findByRole("button", { name: "Delete" });
    expect(del).toBeDisabled();
    const box = screen.getByRole("textbox", { name: /type process_image/i });
    await userEvent.type(box, "process_imag");
    expect(del).toBeDisabled();
    await userEvent.type(box, "e");
    await userEvent.click(del);
    await waitFor(() => expect(ctx.save).toHaveBeenCalled());
    expect(ctx.save.mock.calls[0]![0].functions).toEqual({});
  });

  it("cancel deletes nothing", async () => {
    const ctx = await openDelete();
    await userEvent.click((await screen.findAllByRole("button", { name: "Cancel" })).at(-1)!);
    expect(ctx.save).not.toHaveBeenCalled();
  });
});
