import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { AuthPage } from "./Auth";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";
import * as api from "../api/client";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<typeof api>();
  return {
    ...real,
    getEnvVars: vi.fn().mockResolvedValue({ vars: {} }),
    putDotenv: vi.fn().mockResolvedValue({ message: "ok" }),
  };
});

const makeConfig = (authEnabled: boolean): Config => ({
  version: 1,
  project: { name: "Test", description: "" },
  extensions: [],
  tables: {},
  auth: authEnabled
    ? {
        jwt_expiry: "15m",
        refresh_tokens: true,
        refresh_token_expiry: "7d",
        email: { verify_email: false, templates: {} },
        google: null,
        github: null,
      }
    : null,
  storage: {},
  rpc: {},
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
});

function renderAuth(config: Config, dotenvWritable = false) {
  const save = vi.fn().mockResolvedValue(true);
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    dotenvWritable,
    refresh: vi.fn(),
    save,
    updateConfig: vi.fn(),
  };
  const result = render(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter>
        <DialogProvider>
          <AuthPage />
        </DialogProvider>
      </MemoryRouter>
    </ConfigContext.Provider>
  );
  return { ...result, save };
}

describe("AuthPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getEnvVars).mockResolvedValue({ vars: {} });
  });

  it("renders page title", () => {
    renderAuth(makeConfig(true));
    expect(screen.getByRole("heading", { name: "Authentication" })).toBeInTheDocument();
  });

  it("shows JWT settings when auth is enabled", () => {
    renderAuth(makeConfig(true));
    expect(screen.getByText("JWT Settings")).toBeInTheDocument();
    expect(screen.getByDisplayValue("15m")).toBeInTheDocument();
    expect(screen.getByDisplayValue("7d")).toBeInTheDocument();
  });

  it("shows disabled state when auth is off", () => {
    renderAuth(makeConfig(false));
    expect(screen.getByText("Auth is disabled")).toBeInTheDocument();
    expect(screen.queryByText("JWT Settings")).not.toBeInTheDocument();
  });

  it("shows OAuth provider toggles", () => {
    renderAuth(makeConfig(true));
    expect(screen.getByText("google")).toBeInTheDocument();
    expect(screen.getByText("github")).toBeInTheDocument();
  });

  it("toggles auth on when clicking the toggle", async () => {
    renderAuth(makeConfig(false));
    expect(screen.getByText("Auth is disabled")).toBeInTheDocument();

    const toggles = screen.getAllByRole("switch");
    const authToggle = toggles[0]!;
    await userEvent.click(authToggle);

    expect(screen.getByText("JWT Settings")).toBeInTheDocument();
    expect(screen.queryByText("Auth is disabled")).not.toBeInTheDocument();
  });

  it("shows email verification section", () => {
    renderAuth(makeConfig(true));
    expect(screen.getByText("Email Verification")).toBeInTheDocument();
    expect(screen.getByText("Require email verification")).toBeInTheDocument();
  });

  it("renders nothing when config is null", () => {
    const ctx = {
      config: null,
      loading: true,
      error: null,
      checksum: "",
      saving: false,
      saveErrors: [] as ValidationError[],
      dotenvWritable: false,
      refresh: vi.fn(),
      save: vi.fn(),
      updateConfig: vi.fn(),
    };
    const { container } = render(
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter>
          <DialogProvider>
            <AuthPage />
          </DialogProvider>
        </MemoryRouter>
      </ConfigContext.Provider>
    );
    expect(container.innerHTML).toBe("");
  });

  it("shows Google OAuth var names when Google is enabled", async () => {
    const config = makeConfig(true);
    config.auth!.google = {
      client_id: "${INSTANCEZ_GOOGLE_CLIENT_ID}",
      client_secret: "${INSTANCEZ_GOOGLE_CLIENT_SECRET}",
      redirect_url: "${INSTANCEZ_GOOGLE_REDIRECT_URL}",
    };
    renderAuth(config);
    expect(screen.getByText("INSTANCEZ_GOOGLE_CLIENT_ID")).toBeInTheDocument();
    expect(screen.getByText("INSTANCEZ_GOOGLE_CLIENT_SECRET")).toBeInTheDocument();
    expect(screen.getByText("INSTANCEZ_GOOGLE_REDIRECT_URL")).toBeInTheDocument();
  });

  it("shows var status as set when getEnvVars returns set=true", async () => {
    vi.mocked(api.getEnvVars).mockResolvedValue({
      vars: { INSTANCEZ_GOOGLE_CLIENT_ID: { set: true } },
    });
    const config = makeConfig(true);
    config.auth!.google = {
      client_id: "${INSTANCEZ_GOOGLE_CLIENT_ID}",
      client_secret: "${INSTANCEZ_GOOGLE_CLIENT_SECRET}",
      redirect_url: "${INSTANCEZ_GOOGLE_REDIRECT_URL}",
    };
    renderAuth(config);
    await waitFor(() => expect(screen.getByText("✓ set")).toBeInTheDocument());
  });

  it("shows dotenv input when dotenvWritable=true and provider is enabled", () => {
    const config = makeConfig(true);
    config.auth!.google = {
      client_id: "${INSTANCEZ_GOOGLE_CLIENT_ID}",
      client_secret: "${INSTANCEZ_GOOGLE_CLIENT_SECRET}",
      redirect_url: "${INSTANCEZ_GOOGLE_REDIRECT_URL}",
    };
    renderAuth(config, true);
    expect(screen.getAllByPlaceholderText("enter value…").length).toBeGreaterThan(0);
  });

  it("hides dotenv inputs when dotenvWritable=false", () => {
    const config = makeConfig(true);
    config.auth!.google = {
      client_id: "${INSTANCEZ_GOOGLE_CLIENT_ID}",
      client_secret: "${INSTANCEZ_GOOGLE_CLIENT_SECRET}",
      redirect_url: "${INSTANCEZ_GOOGLE_REDIRECT_URL}",
    };
    renderAuth(config, false);
    expect(screen.queryByPlaceholderText("enter value…")).not.toBeInTheDocument();
  });
});
