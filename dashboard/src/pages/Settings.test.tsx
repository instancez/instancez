import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { SettingsPage } from "./Settings";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig: Config = {
  project: { name: "My App", description: "A cool app" },
  extensions: ["pgcrypto"],
  tables: {},
  auth: null,
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
    cors: {
      origins: ["http://localhost:3000"],
      methods: ["GET", "POST"],
      headers: ["Authorization"],
      credentials: true,
      max_age: 3600,
    },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
};

function renderSettings(config: Config = baseConfig) {
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
        <SettingsPage />
      </MemoryRouter>
    </ConfigContext.Provider>
  );
  return { ...result, save };
}

describe("SettingsPage", () => {
  it("renders page title", () => {
    renderSettings();
    expect(screen.getByText("Server Settings")).toBeInTheDocument();
  });

  it("shows project name and description", () => {
    renderSettings();
    expect(screen.getByDisplayValue("My App")).toBeInTheDocument();
    expect(screen.getByDisplayValue("A cool app")).toBeInTheDocument();
  });

  it("shows extensions", () => {
    renderSettings();
    expect(screen.getByText("pgcrypto")).toBeInTheDocument();
  });

  it("shows server settings", () => {
    renderSettings();
    expect(screen.getByDisplayValue("8080")).toBeInTheDocument();
    expect(screen.getByDisplayValue("10MB")).toBeInTheDocument();
    expect(screen.getByDisplayValue("1000")).toBeInTheDocument();
  });

  it("shows CORS origins", () => {
    renderSettings();
    expect(screen.getByText("http://localhost:3000")).toBeInTheDocument();
  });

  it("shows CORS methods as checkboxes", () => {
    renderSettings();
    const getCheckbox = screen.getByRole("checkbox", { name: "GET" });
    const postCheckbox = screen.getByRole("checkbox", { name: "POST" });
    expect(getCheckbox).toBeChecked();
    expect(postCheckbox).toBeChecked();
  });

  it("shows timeout values", () => {
    renderSettings();
    expect(screen.getByDisplayValue("30s")).toBeInTheDocument();
    expect(screen.getAllByDisplayValue("10s").length).toBe(2); // db_query + shutdown
    expect(screen.getByDisplayValue("60s")).toBeInTheDocument();
  });

  it("shows database pool settings", () => {
    renderSettings();
    expect(screen.getByDisplayValue("25")).toBeInTheDocument();
    expect(screen.getByDisplayValue("5")).toBeInTheDocument();
    expect(screen.getByDisplayValue("5m")).toBeInTheDocument();
  });

  it("shows provider dropdowns", () => {
    renderSettings();
    expect(screen.getByText("Email Provider")).toBeInTheDocument();
    expect(screen.getByText("Storage Provider")).toBeInTheDocument();
  });

  it("can update project name", async () => {
    renderSettings();
    const nameInput = screen.getByDisplayValue("My App");
    await userEvent.clear(nameInput);
    await userEvent.type(nameInput, "New Name");
    expect(screen.getByDisplayValue("New Name")).toBeInTheDocument();
    // Save bar should appear since we changed something
    expect(screen.getByText("Save Changes")).toBeInTheDocument();
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
          <SettingsPage />
        </MemoryRouter>
      </ConfigContext.Provider>
    );
    expect(container.innerHTML).toBe("");
  });
});
