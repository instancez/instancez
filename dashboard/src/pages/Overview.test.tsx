import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Overview } from "./Overview";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

vi.mock("../api/client", () => ({
  getStats: vi.fn(),
  getStatus: vi.fn(),
}));

import { getStats, getStatus } from "../api/client";

const mockGetStats = vi.mocked(getStats);
const mockGetStatus = vi.mocked(getStatus);

const baseConfig: Config = {
  version: 1,
  project: { name: "My Project", description: "A test project" },
  extensions: [],
  tables: {
    todos: {
      fields: { id: { type: "bigserial", primary_key: true }, title: { type: "text" } },
      indexes: [],
      rls: [],
      
      searchable: [],
      search_config: "",
    },
  },
  auth: { jwt_expiry: "15m", refresh_tokens: true, refresh_token_expiry: "7d", email: { verify_email: false, templates: {} }, google: null, github: null },
  storage: {
    avatars: { max_size: "5MB", types: ["image/*"], public: true, rls: [] },
  },
  functions: {},
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

function renderOverview(config: Config = baseConfig) {
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
        <Overview />
      </MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("Overview", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetStats.mockResolvedValue({
      tables: { todos: { row_count: 42 } },
      storage: { avatars: { object_count: 10, total_bytes: 1048576 } },
    });
    mockGetStatus.mockResolvedValue({ database: "connected" });
  });

  it("renders project name as title", () => {
    renderOverview();
    expect(screen.getByText("My Project")).toBeInTheDocument();
  });

  it("shows summary cards with correct counts", () => {
    renderOverview();
    // Tables card
    expect(screen.getAllByText("Tables").length).toBeGreaterThan(0);
    // Auth card
    expect(screen.getByText("Enabled")).toBeInTheDocument();
    // Storage card shows bucket count
    expect(screen.getAllByText("1").length).toBeGreaterThan(0);
  });

  it("shows database connected status after loading", async () => {
    renderOverview();
    await waitFor(() => {
      expect(screen.getByText("Database Connected")).toBeInTheDocument();
    });
  });

  it("shows auth enabled badge", () => {
    renderOverview();
    expect(screen.getByText("Auth Enabled")).toBeInTheDocument();
  });

  it("shows port badge", () => {
    renderOverview();
    expect(screen.getByText("Port 8080")).toBeInTheDocument();
  });

  it("shows table detail rows with row counts", async () => {
    renderOverview();
    await waitFor(() => {
      expect(screen.getByText("todos")).toBeInTheDocument();
      expect(screen.getByText("2 fields")).toBeInTheDocument();
    });
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
          <Overview />
        </MemoryRouter>
      </ConfigContext.Provider>
    );
    expect(container.innerHTML).toBe("");
  });
});
