import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { Functions } from "./Functions";
import { ConfigContext } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
import type { ConsoleBackend } from "../console/backend";
import type { Config, ValidationError } from "../lib/types";

// Hoisted spies so the vi.mock factories can close over them.
const mockNavigate = vi.hoisted(() => vi.fn());
const promptFn = vi.hoisted(() => vi.fn());
const alertFn = vi.hoisted(() => vi.fn());

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return { ...actual, useNavigate: () => mockNavigate };
});

// Stub useDialog so prompt/alert resolve without DOM interaction.
vi.mock("../components/Dialog", async () => {
  const actual = await vi.importActual<typeof import("../components/Dialog")>("../components/Dialog");
  return {
    ...actual,
    useDialog: () => ({ prompt: promptFn, alert: alertFn, confirm: vi.fn().mockResolvedValue(true) }),
  };
});

// --- mock API -----------------------------------------------------------
vi.mock("../api/client", () => ({
  getFunctionDeps: vi.fn(),
  postFunctionDeps: vi.fn(),
  putFunctionCode: vi.fn(),
}));

import { getFunctionDeps, postFunctionDeps, putFunctionCode } from "../api/client";
const mockGetDeps = vi.mocked(getFunctionDeps);
const mockPostDeps = vi.mocked(postFunctionDeps);
const mockPutCode = vi.mocked(putFunctionCode);

const DEFAULT_DEPS = {
  dependencies: { axios: "^1.7.2", lodash: "^4.17.21" },
  has_lock: true,
  readonly: false,
};

// --- fixtures -----------------------------------------------------------
const baseConfig = {
  version: 1,
  project: { name: "P", description: "" },
  tables: {},
  auth: null,
  storage: {},
  rpc: {},
  functions: {
    todos: { runtime: "node", file: "functions/todos.js", auth_required: true, timeout: "30s" },
  },
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    cors: { origins: [] },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
  },
} as unknown as Config;

function renderFunctions(
  config: Config,
  opts: { backend?: ConsoleBackend; save?: (updated: Config) => Promise<boolean> } = {}
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
    save: opts.save ?? vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
  };
  return renderWithChakra(
    <BackendProvider backend={opts.backend ?? adminBackend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter>
          <Functions />
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
}

// --- tests --------------------------------------------------------------
beforeEach(() => {
  vi.clearAllMocks();
  mockGetDeps.mockResolvedValue(DEFAULT_DEPS);
});

describe("Functions: create", () => {
  const readWrite = adminBackend; // canWriteConfig + canEditFunctionCode both true

  it("shows New function button when config + code are writable", () => {
    renderFunctions(baseConfig, { backend: readWrite });
    expect(screen.getByRole("button", { name: /new function/i })).toBeInTheDocument();
  });

  it("hides New function button when function code is not editable", () => {
    const backend: ConsoleBackend = {
      ...adminBackend,
      capabilities: { ...adminBackend.capabilities, canEditFunctionCode: false },
    };
    renderFunctions(baseConfig, { backend });
    expect(screen.queryByRole("button", { name: /new function/i })).not.toBeInTheDocument();
  });

  it("hides New function button when config is read-only (e.g. viewing the production env)", () => {
    const backend: ConsoleBackend = {
      ...adminBackend,
      capabilities: { ...adminBackend.capabilities, canWriteConfig: false },
    };
    renderFunctions(baseConfig, { backend });
    expect(screen.queryByRole("button", { name: /new function/i })).not.toBeInTheDocument();
  });

  it("does not write anything; navigates to new mode with the seed", async () => {
    promptFn.mockResolvedValue("Send Email");
    const save = vi.fn();
    renderFunctions(baseConfig, { backend: readWrite, save });

    await act(async () => {
      screen.getByRole("button", { name: /new function/i }).click();
    });

    expect(save).not.toHaveBeenCalled();
    expect(mockPutCode).not.toHaveBeenCalled();
    // Name normalized like tables: lowercased, spaces → underscores.
    expect(mockNavigate).toHaveBeenCalledWith(
      "new",
      expect.objectContaining({
        relative: "path",
        state: expect.objectContaining({
          functionName: "send_email",
          seed: expect.objectContaining({ runtime: "node", file: "functions/send_email.js" }),
          code: expect.stringContaining("send_email"),
        }),
      })
    );
  });

  it("does not create when the prompt is cancelled", async () => {
    promptFn.mockResolvedValue(null);
    const save = vi.fn();
    renderFunctions(baseConfig, { backend: readWrite, save });

    await act(async () => {
      screen.getByRole("button", { name: /new function/i }).click();
    });

    expect(save).not.toHaveBeenCalled();
    expect(mockPutCode).not.toHaveBeenCalled();
  });

  it("rejects a name that already exists", async () => {
    promptFn.mockResolvedValue("todos"); // present in baseConfig
    const save = vi.fn();
    renderFunctions(baseConfig, { backend: readWrite, save });

    await act(async () => {
      screen.getByRole("button", { name: /new function/i }).click();
    });

    expect(save).not.toHaveBeenCalled();
    expect(alertFn).toHaveBeenCalled();
  });

});

describe("Functions – function list", () => {
  it("lists code functions with file and runtime", async () => {
    renderFunctions(baseConfig);
    expect(screen.queryByText(/edge/i)).not.toBeInTheDocument();
    expect(screen.getByText("todos")).toBeInTheDocument();
    expect(screen.getByText("functions/todos.js")).toBeInTheDocument();
    expect(screen.getByText("node")).toBeInTheDocument();
  });

  it("shows empty state when no functions", async () => {
    renderFunctions({ ...baseConfig, functions: {} });
    expect(screen.getByText("No code functions")).toBeInTheDocument();
  });
});

describe("Functions – dependencies section", () => {
  it("shows installed packages after loading", async () => {
    renderFunctions(baseConfig);
    await waitFor(() => expect(screen.getByText("axios")).toBeInTheDocument());
    expect(screen.getByText("^1.7.2")).toBeInTheDocument();
    expect(screen.getByText("lodash")).toBeInTheDocument();
    expect(screen.getByText("^4.17.21")).toBeInTheDocument();
  });

  it("shows lock file badge when has_lock is true", async () => {
    renderFunctions(baseConfig);
    await waitFor(() => expect(screen.getByText("lock file")).toBeInTheDocument());
  });

  it("shows no-lock-file warning when has_lock is false", async () => {
    mockGetDeps.mockResolvedValue({ ...DEFAULT_DEPS, has_lock: false });
    renderFunctions(baseConfig);
    await waitFor(() => expect(screen.getByText("no lock file")).toBeInTheDocument());
  });

  it("hides dependencies section when API is unavailable", async () => {
    mockGetDeps.mockRejectedValue(new Error("not implemented"));
    renderFunctions(baseConfig);
    // Give the effect time to settle
    await waitFor(() => expect(mockGetDeps).toHaveBeenCalled());
    expect(screen.queryByText("Dependencies")).not.toBeInTheDocument();
  });

  it("shows empty message when no packages installed", async () => {
    mockGetDeps.mockResolvedValue({ dependencies: {}, has_lock: false, readonly: false });
    renderFunctions(baseConfig);
    await waitFor(() => expect(screen.getByText("No dependencies installed.")).toBeInTheDocument());
    // No lock file badge or warning when there are no packages
    expect(screen.queryByText("lock file")).not.toBeInTheDocument();
    expect(screen.queryByText("no lock file")).not.toBeInTheDocument();
  });

  it("hides add/remove controls in readonly mode", async () => {
    mockGetDeps.mockResolvedValue({ ...DEFAULT_DEPS, readonly: true });
    renderFunctions(baseConfig);
    await waitFor(() => expect(screen.getByText("axios")).toBeInTheDocument());
    expect(screen.queryByPlaceholderText(/e\.g\./)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Install/ })).not.toBeInTheDocument();
  });

  it("installs a package when the user types a name and clicks Install", async () => {
    const user = userEvent.setup();
    const updatedDeps = {
      dependencies: { ...DEFAULT_DEPS.dependencies, "date-fns": "^3.0.0" },
      has_lock: true,
      readonly: false,
    };
    mockPostDeps.mockResolvedValue(updatedDeps);
    renderFunctions(baseConfig);

    await waitFor(() => expect(screen.getByPlaceholderText(/e\.g\./)).toBeInTheDocument());

    const input = screen.getByPlaceholderText(/e\.g\./);
    await user.type(input, "date-fns");
    await user.click(screen.getByRole("button", { name: "Install" }));

    expect(mockPostDeps).toHaveBeenCalledWith(["date-fns"], []);
    await waitFor(() => expect(screen.getByText("date-fns")).toBeInTheDocument());
    await waitFor(() => expect(screen.getByText(/Installed date-fns/)).toBeInTheDocument());
  });

  it("submits on Enter key", async () => {
    const user = userEvent.setup();
    mockPostDeps.mockResolvedValue(DEFAULT_DEPS);
    renderFunctions(baseConfig);

    await waitFor(() => expect(screen.getByPlaceholderText(/e\.g\./)).toBeInTheDocument());
    await user.type(screen.getByPlaceholderText(/e\.g\./), "express{Enter}");

    expect(mockPostDeps).toHaveBeenCalledWith(["express"], []);
  });

  it("removes a package when the remove button is clicked", async () => {
    const user = userEvent.setup();
    const afterRemove = {
      dependencies: { lodash: "^4.17.21" },
      has_lock: true,
      readonly: false,
    };
    mockPostDeps.mockResolvedValue(afterRemove);
    renderFunctions(baseConfig);

    await waitFor(() => expect(screen.getByText("axios")).toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: "Remove axios" }));

    expect(mockPostDeps).toHaveBeenCalledWith([], ["axios"]);
    await waitFor(() => expect(screen.queryByText("axios")).not.toBeInTheDocument());
  });

  it("shows npm error output when install fails", async () => {
    const user = userEvent.setup();
    mockPostDeps.mockRejectedValue(
      Object.assign(new Error("npm install failed"), {
        body: { detail: "npm ERR! 404 Not Found: nonexistent-pkg@latest" },
      })
    );
    renderFunctions(baseConfig);

    await waitFor(() => expect(screen.getByPlaceholderText(/e\.g\./)).toBeInTheDocument());
    await user.type(screen.getByPlaceholderText(/e\.g\./), "nonexistent-pkg{Enter}");

    await waitFor(() =>
      expect(screen.getByText("npm ERR! 404 Not Found: nonexistent-pkg@latest")).toBeInTheDocument()
    );
  });

  it("disables Install button while installing", async () => {
    const user = userEvent.setup();
    // Never resolves during the test
    mockPostDeps.mockImplementation(() => new Promise(() => {}));
    renderFunctions(baseConfig);

    await waitFor(() => expect(screen.getByPlaceholderText(/e\.g\./)).toBeInTheDocument());
    await user.type(screen.getByPlaceholderText(/e\.g\./), "axios");
    await user.click(screen.getByRole("button", { name: "Install" }));

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Installing…" })).toBeDisabled()
    );
  });
});
