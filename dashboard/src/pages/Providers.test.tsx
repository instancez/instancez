import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
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

function renderProvidersWithCtx(config: Config, dotenvWritable = false) {
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
  renderWithChakra(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter>
        <ProvidersPage />
      </MemoryRouter>
    </ConfigContext.Provider>
  );
  return ctx;
}

function renderProviders(config: Config, dotenvWritable = false) {
  renderProvidersWithCtx(config, dotenvWritable);
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

  it("renders only the supported email providers", () => {
    renderProviders(baseConfig);
    expect(screen.getByText("Resend")).toBeInTheDocument();
    expect(screen.queryByText("SendGrid")).not.toBeInTheDocument();
  });

  it("renders only the supported storage providers", () => {
    renderProviders(baseConfig);
    expect(screen.getByText("AWS S3")).toBeInTheDocument();
    expect(screen.getByText("Local Filesystem")).toBeInTheDocument();
    expect(screen.queryByText("MinIO")).not.toBeInTheDocument();
    expect(screen.queryByText("Google Cloud Storage")).not.toBeInTheDocument();
  });

  it("requests status for all credential vars, even ones not yet saved in config", () => {
    renderProviders(baseConfig);
    expect(api.getEnvVars).toHaveBeenCalledWith(
      expect.arrayContaining([
        "INSTANCEZ_RESEND_API_KEY",
        "AWS_ACCESS_KEY_ID",
        "AWS_SECRET_ACCESS_KEY",
      ])
    );
    expect(api.getEnvVars).not.toHaveBeenCalledWith(
      expect.arrayContaining(["INSTANCEZ_SENDGRID_API_KEY"])
    );
  });

  it("shows a labeled config row when email provider is selected", () => {
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
    expect(screen.getByText("API key")).toBeInTheDocument();
    expect(screen.getByText("INSTANCEZ_RESEND_API_KEY")).toBeInTheDocument();
  });

  it("renders credentials at the top, before settings", () => {
    const config: Config = {
      ...baseConfig,
      providers: {
        email: {
          type: "resend",
          api_key: "${INSTANCEZ_RESEND_API_KEY}",
          default_from_email: "noreply@x.com",
        },
        storage: null,
      },
    };
    renderProviders(config);
    // one untitled section: no "Credentials"/"Settings" sub-headings,
    // credential rows simply come first
    expect(screen.queryByText("Environment variables")).not.toBeInTheDocument();
    expect(screen.queryByText("Credentials")).not.toBeInTheDocument();
    expect(screen.queryByText("Settings")).not.toBeInTheDocument();
    const apiKey = screen.getByText("API key");
    const fromEmail = screen.getByText("Default from email");
    expect(
      apiKey.compareDocumentPosition(fromEmail) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  it("hides the save bar again when an edit is undone", () => {
    const config: Config = {
      ...baseConfig,
      providers: {
        email: null,
        storage: {
          type: "s3",
          bucket: "my-bucket",
          region: "eu-central-1",
          access_key_id: "",
          secret_access_key: "",
          endpoint: "",
          path: "",
        },
      },
    };
    renderProviders(config);
    expect(screen.queryByRole("button", { name: /save changes/i })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Bucket"), { target: { value: "other" } });
    expect(screen.getByRole("button", { name: /save changes/i })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Bucket"), { target: { value: "my-bucket" } });
    expect(screen.queryByRole("button", { name: /save changes/i })).not.toBeInTheDocument();
  });

  it("renders literal settings as plain editable inputs", () => {
    const config: Config = {
      ...baseConfig,
      providers: {
        email: null,
        storage: {
          type: "s3",
          bucket: "my-bucket",
          region: "eu-central-1",
          access_key_id: "",
          secret_access_key: "",
          endpoint: "",
          path: "",
        },
      },
    };
    renderProviders(config);
    expect(screen.getByLabelText("Bucket")).toHaveValue("my-bucket");
    expect(screen.getByLabelText("Region")).toHaveValue("eu-central-1");
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
    expect(screen.getByLabelText("INSTANCEZ_RESEND_API_KEY")).toBeInTheDocument();
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
    expect(screen.queryByLabelText("INSTANCEZ_RESEND_API_KEY")).not.toBeInTheDocument();
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
          path: "${INSTANCEZ_LOCAL_STORAGE_PATH}",
        },
      },
    };
    renderProviders(config);
    expect(screen.queryByText("Provide explicit AWS credentials")).not.toBeInTheDocument();
  });

  it("renders settings holding ${VAR} refs as env-managed rows", () => {
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
          path: "",
        },
      },
    };
    renderProviders(config);
    expect(screen.getByText("Bucket")).toBeInTheDocument();
    expect(screen.getByText("INSTANCEZ_S3_BUCKET")).toBeInTheDocument();
    expect(screen.getByText("Region")).toBeInTheDocument();
    expect(screen.getByText("AWS_REGION")).toBeInTheDocument();
    expect(api.getEnvVars).toHaveBeenCalledWith(
      expect.arrayContaining(["INSTANCEZ_S3_BUCKET", "AWS_REGION"])
    );
  });

  it("passes staged dotenv changes to save for the confirmation summary", async () => {
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
    const { save } = renderProvidersWithCtx(config, true);
    fireEvent.change(screen.getByLabelText("INSTANCEZ_RESEND_API_KEY"), {
      target: { value: "re_secret_value_abcd" },
    });
    fireEvent.click(screen.getByRole("button", { name: /save/i }));
    await waitFor(() =>
      expect(save).toHaveBeenCalledWith(expect.anything(), {
        dotenvChanges: [
          { name: "INSTANCEZ_RESEND_API_KEY", tail: "abcd", isUpdate: false },
        ],
      })
    );
  });
});
