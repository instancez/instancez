import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { Overview } from "./Overview";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

vi.mock("../api/client", () => ({
  getStats: vi.fn(),
  getStatus: vi.fn(),
  getKeys: vi.fn().mockResolvedValue({ anon_key: "test-anon-key" }),
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
      fields: [
        { name: "id", type: "bigserial", primary_key: true },
        { name: "title", type: "text" },
      ],
      indexes: [],
      rls: [],
    },
  },
  auth: { jwt_expiry: "15m", refresh_tokens: true, refresh_token_expiry: "7d", email: { verify_email: false, templates: {} }, google: null, github: null },
  storage: {
    avatars: { max_size: "5MB", types: ["image/*"], public: true, rls: [] },
  },
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
};

function renderOverview(config: Config = baseConfig) {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    dotenvWritable: false,
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

  it("shows summary card counts with units", () => {
    renderOverview();
    expect(screen.getAllByText("Tables").length).toBeGreaterThan(0);
    expect(screen.getByText("table")).toBeInTheDocument();
    expect(screen.getByText("bucket")).toBeInTheDocument();
    expect(screen.getByText("Enabled")).toBeInTheDocument();
  });

  it("does not show status badges", async () => {
    renderOverview();
    expect(screen.queryByText("Database Connected")).not.toBeInTheDocument();
    expect(screen.queryByText("Port 8080")).not.toBeInTheDocument();
    expect(screen.queryByText("Auth Enabled")).not.toBeInTheDocument();
  });

  it("does not list individual tables or buckets", async () => {
    renderOverview();
    expect(screen.queryByText("2 fields")).not.toBeInTheDocument();
    expect(screen.queryByText("avatars")).not.toBeInTheDocument();
    expect(screen.queryByText("Storage Buckets")).not.toBeInTheDocument();
  });

  it("shows client connection examples with the real URL and an ANON_KEY var", async () => {
    renderOverview();
    expect(screen.getByText("JS/TS")).toBeInTheDocument();
    expect(screen.queryByText("supabase-js")).not.toBeInTheDocument();
    const snippet = await screen.findByTestId("connect-snippet");
    expect(snippet.textContent).toContain(window.location.origin);
    expect(snippet.textContent).toContain("createClient");
    expect(snippet.textContent).toContain("ANON_KEY");
    // the raw token never appears in the snippet — shorter, and safe to screenshot
    expect(snippet.textContent).not.toContain("test-anon-key");
    expect(snippet.textContent).toContain("from('todos')");

    fireEvent.click(screen.getByRole("button", { name: "Python" }));
    expect(screen.getByTestId("connect-snippet").textContent).toContain("create_client");

    fireEvent.click(screen.getByRole("button", { name: "Go" }));
    expect(screen.getByTestId("connect-snippet").textContent).toContain(
      "supabase-community/supabase-go"
    );
  });

  it("does not show section descriptions for API Keys and Connect", async () => {
    renderOverview();
    expect(screen.queryByText(/browser-safe/)).not.toBeInTheDocument();
    expect(screen.queryByText(/same wire protocol/)).not.toBeInTheDocument();
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
          <Overview />
        </MemoryRouter>
      </ConfigContext.Provider>
    );
    expect(container.innerHTML).toBe("");
  });
});
