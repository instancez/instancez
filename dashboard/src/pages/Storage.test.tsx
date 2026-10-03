import { describe, it, expect, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { Storage } from "./Storage";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
import { fullCapabilities, type ConsoleBackend } from "../console/backend";
import type { Config, ValidationError } from "../lib/types";

const baseConfig = {
  version: 1,
  project: { name: "P", description: "" },
  tables: {},
  auth: null,
  // A deployed config can omit array fields the TS type marks required — here a
  // bucket with no `types`. The list must not crash reading `.length` off it.
  storage: {
    uploads: { max_size: "5MB", public: false } as unknown,
  },
  rpc: {},
  functions: {},
  providers: { email: null, storage: null },
} as unknown as Config;

function renderStorage(config: Config, backend: ConsoleBackend = adminBackend) {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    dotenvWritable: false,
    oauthCallbackBase: "",
    refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
  };
  return renderWithChakra(
    <BackendProvider backend={backend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter>
          <DialogProvider>
            <Storage />
          </DialogProvider>
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
}

describe("Storage", () => {
  it("renders a bucket that has no `types` field without crashing", () => {
    renderStorage(baseConfig);
    expect(screen.getByText("uploads")).toBeInTheDocument();
    expect(screen.getByText("1 bucket configured")).toBeInTheDocument();
  });

  it("shows each bucket's total size from stats", async () => {
    const backend = {
      capabilities: fullCapabilities(),
      getStats: vi.fn(async () => ({ storage: { uploads: { object_count: 3, total_bytes: 2048 } } })),
    } as unknown as ConsoleBackend;
    renderStorage(baseConfig, backend);
    expect(await screen.findByText("2 KB")).toBeInTheDocument();
  });

  it("renders without a size when stats are null or missing the bucket", async () => {
    const getStats = vi.fn(async () => ({ storage: null }));
    const backend = { capabilities: fullCapabilities(), getStats } as unknown as ConsoleBackend;
    renderStorage(baseConfig, backend);
    await waitFor(() => expect(getStats).toHaveBeenCalled());
    expect(screen.getByText("uploads")).toBeInTheDocument();
  });

  it("skips stats when the backend has none", () => {
    const getStats = vi.fn();
    const backend = { capabilities: { ...fullCapabilities(), hasStats: false }, getStats } as unknown as ConsoleBackend;
    renderStorage(baseConfig, backend);
    expect(getStats).not.toHaveBeenCalled();
  });
});
