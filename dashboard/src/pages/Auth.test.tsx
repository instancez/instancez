import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
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

  it("requests status for credential vars, even ones not yet saved in config", () => {
    renderAuth(makeConfig(true));
    expect(api.getEnvVars).toHaveBeenCalledWith(
      expect.arrayContaining([
        "INSTANCEZ_GOOGLE_CLIENT_SECRET",
        "INSTANCEZ_GITHUB_CLIENT_SECRET",
      ])
    );
  });

  it("requests status for ${VAR} refs found in saved OAuth settings", () => {
    const config = makeConfig(true);
    config.auth!.google = {
      client_id: "${MY_CUSTOM_CLIENT_ID}",
      client_secret: "${INSTANCEZ_GOOGLE_CLIENT_SECRET}",
      redirect_url: "https://app.example.com/callback",
    };
    renderAuth(config);
    expect(api.getEnvVars).toHaveBeenCalledWith(
      expect.arrayContaining(["MY_CUSTOM_CLIENT_ID"])
    );
  });

  it("renders literal OAuth settings as plain editable inputs, creds first", () => {
    const config = makeConfig(true);
    config.auth!.google = {
      client_id: "abc123",
      client_secret: "${INSTANCEZ_GOOGLE_CLIENT_SECRET}",
      redirect_url: "https://app.example.com/callback",
    };
    renderAuth(config);
    expect(screen.getByLabelText("Client ID")).toHaveValue("abc123");
    expect(screen.getByLabelText("Redirect URL")).toHaveValue(
      "https://app.example.com/callback"
    );
    // one untitled section: no sub-headings, credential row first
    expect(screen.queryByText("Credentials")).not.toBeInTheDocument();
    expect(screen.queryByText("Settings")).not.toBeInTheDocument();
    const secret = screen.getByText("Client secret");
    const clientId = screen.getByLabelText("Client ID");
    expect(
      secret.compareDocumentPosition(clientId) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it("shows all three email template editors with default-subject placeholders", () => {
    renderAuth(makeConfig(true));
    expect(screen.getByText("Verification")).toBeInTheDocument();
    expect(screen.getByText("Magic link")).toBeInTheDocument();
    expect(screen.getByText("Password reset")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Confirm your email")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Your sign-in link")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Reset your password")).toBeInTheDocument();
  });

  it("reads template overrides from the backend's template keys", () => {
    const config = makeConfig(true);
    config.auth!.email = {
      verify_email: true,
      templates: {
        verification: { subject: "Custom verify subject", body: "b", body_file: "" },
      },
    };
    renderAuth(config);
    expect(screen.getByDisplayValue("Custom verify subject")).toBeInTheDocument();
  });

  it("hides the save bar again when an edit is undone", () => {
    const config = makeConfig(true);
    config.auth!.google = {
      client_id: "abc123",
      client_secret: "${INSTANCEZ_GOOGLE_CLIENT_SECRET}",
      redirect_url: "https://app.example.com/callback",
    };
    renderAuth(config);
    expect(screen.queryByRole("button", { name: /save changes/i })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Client ID"), { target: { value: "changed" } });
    expect(screen.getByRole("button", { name: /save changes/i })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Client ID"), { target: { value: "abc123" } });
    expect(screen.queryByRole("button", { name: /save changes/i })).not.toBeInTheDocument();
  });

  it("enabling a provider stages the secret as ${VAR} and settings as literals", () => {
    renderAuth(makeConfig(true));
    fireEvent.click(screen.getByLabelText("Enable google"));
    expect(screen.getByText("INSTANCEZ_GOOGLE_CLIENT_SECRET")).toBeInTheDocument();
    expect(screen.getByLabelText("Client ID")).toHaveValue("");
    expect(screen.getByLabelText("Redirect URL")).toHaveValue("");
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
    for (const varName of [
      "INSTANCEZ_GOOGLE_CLIENT_ID",
      "INSTANCEZ_GOOGLE_CLIENT_SECRET",
      "INSTANCEZ_GOOGLE_REDIRECT_URL",
    ]) {
      expect(screen.getByLabelText(varName)).toBeInTheDocument();
    }
  });

  it("shows friendly field labels for OAuth config rows", () => {
    const config = makeConfig(true);
    config.auth!.google = {
      client_id: "${INSTANCEZ_GOOGLE_CLIENT_ID}",
      client_secret: "${INSTANCEZ_GOOGLE_CLIENT_SECRET}",
      redirect_url: "${INSTANCEZ_GOOGLE_REDIRECT_URL}",
    };
    renderAuth(config);
    expect(screen.getByText("Client ID")).toBeInTheDocument();
    expect(screen.getByText("Client secret")).toBeInTheDocument();
    expect(screen.getByText("Redirect URL")).toBeInTheDocument();
  });

  it("hides dotenv inputs when dotenvWritable=false", () => {
    const config = makeConfig(true);
    config.auth!.google = {
      client_id: "${INSTANCEZ_GOOGLE_CLIENT_ID}",
      client_secret: "${INSTANCEZ_GOOGLE_CLIENT_SECRET}",
      redirect_url: "${INSTANCEZ_GOOGLE_REDIRECT_URL}",
    };
    renderAuth(config, false);
    expect(screen.queryByLabelText("INSTANCEZ_GOOGLE_CLIENT_ID")).not.toBeInTheDocument();
  });
});
