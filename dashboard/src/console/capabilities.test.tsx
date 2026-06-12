import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { BackendProvider } from "./BackendContext";
import { adminBackend } from "./adminBackend";
import { ProvidersPage } from "../pages/Providers";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<any>();
  return { ...real, getEnvVars: vi.fn().mockResolvedValue({ vars: {} }) };
});

const config = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [], tables: {}, auth: null, storage: {}, rpc: {}, functions: {}, data: {},
  providers: {
    email: { type: "resend", api_key: "${INSTANCEZ_RESEND_API_KEY}", default_from_email: "" },
    storage: null,
  },
  server: {
    port: 8080, max_body_size: "10MB", max_limit: 1000, docs_ui: true,
    cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
    db: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
  },
} as unknown as Config;

function renderWithCaps(canWriteSecrets: boolean) {
  const backend = {
    ...adminBackend,
    capabilities: { ...adminBackend.capabilities, canWriteSecrets },
  };
  const ctx = {
    config, loading: false, error: null, checksum: "abc", saving: false,
    saveErrors: [] as ValidationError[], dotenvWritable: true,
    refresh: vi.fn(), save: vi.fn().mockResolvedValue(true), updateConfig: vi.fn(),
  };
  render(
    <BackendProvider backend={backend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter>
          <ProvidersPage />
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
}

describe("capability gating", () => {
  it("hides secret inputs when the backend cannot write secrets, even if dotenvWritable", () => {
    renderWithCaps(false);
    expect(screen.queryByLabelText("INSTANCEZ_RESEND_API_KEY")).not.toBeInTheDocument();
  });

  it("shows secret inputs when the backend can write secrets", () => {
    renderWithCaps(true);
    expect(screen.getByLabelText("INSTANCEZ_RESEND_API_KEY")).toBeInTheDocument();
  });
});
