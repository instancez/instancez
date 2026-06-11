import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ProvidersPage } from "./Providers";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig: Config = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
  tables: {},
  auth: null,
  storage: {},
  rpc: {},
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

function renderProviders(config: Config) {
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
        <ProvidersPage />
      </MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("ProvidersPage", () => {
  it("renders Email Provider section", () => {
    renderProviders(baseConfig);
    expect(screen.getByText("Email Provider")).toBeInTheDocument();
  });

  it("renders Storage Provider section", () => {
    renderProviders(baseConfig);
    expect(screen.getByText("Storage Provider")).toBeInTheDocument();
  });

  it("renders email provider options", () => {
    renderProviders(baseConfig);
    expect(screen.getByText("Resend")).toBeInTheDocument();
    expect(screen.getByText("SendGrid")).toBeInTheDocument();
  });

  it("renders storage provider options", () => {
    renderProviders(baseConfig);
    expect(screen.getByText("AWS S3")).toBeInTheDocument();
  });
});
