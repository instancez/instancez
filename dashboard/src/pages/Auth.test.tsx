import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { AuthPage } from "./Auth";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const makeConfig = (authEnabled: boolean): Config => ({
  project: { name: "Test", description: "" },
  extensions: [],
  tables: {},
  auth: authEnabled
    ? {
        jwt_expiry: "15m",
        refresh_tokens: true,
        refresh_token_expiry: "7d",
        fields: {},
        email: { verify_email: false, templates: {} },
        google: null,
        github: null,
      }
    : null,
  storage: {},
  functions: {},
  on: {},
  seeds: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
  },
});

function renderAuth(config: Config) {
  const save = vi.fn().mockResolvedValue(true);
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    refresh: vi.fn(),
    save,
    updateConfig: vi.fn(),
  };
  const result = render(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter>
        <AuthPage />
      </MemoryRouter>
    </ConfigContext.Provider>
  );
  return { ...result, save };
}

describe("AuthPage", () => {
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

  it("shows system fields as read-only", () => {
    renderAuth(makeConfig(true));
    expect(screen.getByText("id")).toBeInTheDocument();
    expect(screen.getByText("email")).toBeInTheDocument();
    expect(screen.getByText("created_at")).toBeInTheDocument();
  });

  it("shows OAuth provider toggles", () => {
    renderAuth(makeConfig(true));
    expect(screen.getByText("google")).toBeInTheDocument();
    expect(screen.getByText("github")).toBeInTheDocument();
  });

  it("toggles auth on when clicking the toggle", async () => {
    renderAuth(makeConfig(false));
    expect(screen.getByText("Auth is disabled")).toBeInTheDocument();

    // Click the toggle button (the first one on the page)
    const toggles = screen.getAllByRole("button");
    const authToggle = toggles[0]!;
    await userEvent.click(authToggle);

    // Should now show JWT settings
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
      refresh: vi.fn(),
      save: vi.fn(),
      updateConfig: vi.fn(),
    };
    const { container } = render(
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter>
          <AuthPage />
        </MemoryRouter>
      </ConfigContext.Provider>
    );
    expect(container.innerHTML).toBe("");
  });
});
