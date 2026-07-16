import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { Storage } from "./Storage";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
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

function renderStorage(config: Config) {
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
    <BackendProvider backend={adminBackend}>
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
});
