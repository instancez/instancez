import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ProvidersPage } from "./Providers";
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

const baseConfig: Config = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
  tables: {},
  auth: null,
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
};

function renderProviders(config: Config, dotenvWritable = false) {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    dotenvWritable,
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
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getEnvVars).mockResolvedValue({ vars: {} });
  });

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

  it("shows env var row when email provider is selected", () => {
    const config: Config = {
      ...baseConfig,
      providers: {
        email: {
          type: "resend",
          api_key: "${INSTANCEZ_RESEND_API_KEY}",
          default_from_email: "",
        },
        storage: null,
      },
    };
    renderProviders(config);
    expect(screen.getByText("INSTANCEZ_RESEND_API_KEY")).toBeInTheDocument();
  });

  it("shows var as unset when getEnvVars returns set=false", async () => {
    vi.mocked(api.getEnvVars).mockResolvedValue({
      vars: { INSTANCEZ_RESEND_API_KEY: { set: false } },
    });
    const config: Config = {
      ...baseConfig,
      providers: {
        email: {
          type: "resend",
          api_key: "${INSTANCEZ_RESEND_API_KEY}",
          default_from_email: "",
        },
        storage: null,
      },
    };
    renderProviders(config);
    await waitFor(() => expect(screen.getByText("✗ unset")).toBeInTheDocument());
  });

  it("shows var as set when getEnvVars returns set=true", async () => {
    vi.mocked(api.getEnvVars).mockResolvedValue({
      vars: { INSTANCEZ_RESEND_API_KEY: { set: true } },
    });
    const config: Config = {
      ...baseConfig,
      providers: {
        email: {
          type: "resend",
          api_key: "${INSTANCEZ_RESEND_API_KEY}",
          default_from_email: "",
        },
        storage: null,
      },
    };
    renderProviders(config);
    await waitFor(() => expect(screen.getByText("✓ set")).toBeInTheDocument());
  });

  it("shows dotenv input fields when dotenvWritable=true", () => {
    const config: Config = {
      ...baseConfig,
      providers: {
        email: {
          type: "resend",
          api_key: "${INSTANCEZ_RESEND_API_KEY}",
          default_from_email: "",
        },
        storage: null,
      },
    };
    renderProviders(config, true);
    expect(screen.getAllByPlaceholderText("enter value…").length).toBeGreaterThan(0);
  });

  it("hides dotenv input fields when dotenvWritable=false", () => {
    const config: Config = {
      ...baseConfig,
      providers: {
        email: {
          type: "resend",
          api_key: "${INSTANCEZ_RESEND_API_KEY}",
          default_from_email: "",
        },
        storage: null,
      },
    };
    renderProviders(config, false);
    expect(screen.queryByPlaceholderText("enter value…")).not.toBeInTheDocument();
  });

  it("shows S3 explicit credentials toggle for S3 provider", () => {
    const config: Config = {
      ...baseConfig,
      providers: {
        email: null,
        storage: {
          type: "s3",
          bucket: "${INSTANCEZ_S3_BUCKET}",
          region: "${AWS_REGION}",
          access_key_id: "",
          secret_access_key: "",
          endpoint: "",
          credentials: "",
          path: "",
        },
      },
    };
    renderProviders(config);
    expect(screen.getByText("Provide explicit AWS credentials")).toBeInTheDocument();
  });

  it("does not show S3 credentials toggle for non-S3 providers", () => {
    const config: Config = {
      ...baseConfig,
      providers: {
        email: null,
        storage: {
          type: "local",
          bucket: "",
          region: "",
          access_key_id: "",
          secret_access_key: "",
          endpoint: "",
          credentials: "",
          path: "${INSTANCEZ_LOCAL_STORAGE_PATH}",
        },
      },
    };
    renderProviders(config);
    expect(screen.queryByText("Provide explicit AWS credentials")).not.toBeInTheDocument();
  });

  it("shows S3 bucket and region vars", () => {
    const config: Config = {
      ...baseConfig,
      providers: {
        email: null,
        storage: {
          type: "s3",
          bucket: "${INSTANCEZ_S3_BUCKET}",
          region: "${AWS_REGION}",
          access_key_id: "",
          secret_access_key: "",
          endpoint: "",
          credentials: "",
          path: "",
        },
      },
    };
    renderProviders(config);
    expect(screen.getByText("INSTANCEZ_S3_BUCKET")).toBeInTheDocument();
    expect(screen.getByText("AWS_REGION")).toBeInTheDocument();
  });
});
