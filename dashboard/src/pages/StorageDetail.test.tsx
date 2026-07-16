import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { StorageDetail } from "./StorageDetail";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig: Config = {
  version: 1,
  project: { name: "P", description: "" },
  tables: {},
  auth: null,
  storage: {
    avatars: {
      public: true,
      max_size: "5MB",
      types: ["image/jpeg", "image/png"],
      rls: [],
    },
  },
  rpc: {},
  functions: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    cors: { origins: [] },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
  },
  database: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
};

function renderStorageDetail(config: Config, bucketName: string) {
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    dotenvWritable: false, oauthCallbackBase: "",
    refresh: vi.fn(),
    save: vi.fn().mockResolvedValue(true),
    updateConfig: vi.fn(),
  };
  return renderWithChakra(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter initialEntries={[`/storage/${bucketName}`]}>
        <DialogProvider>
          <Routes>
            <Route path="/storage/:name" element={<StorageDetail />} />
          </Routes>
        </DialogProvider>
      </MemoryRouter>
    </ConfigContext.Provider>
  );
}

describe("StorageDetail", () => {
  it("renders the back link to the Storage list", () => {
    renderStorageDetail(baseConfig, "avatars");
    // The bucket name is now shell chrome (route handle); the page renders a
    // chrome-free toolbar with a back link to the parent area.
    expect(screen.getByRole("link", { name: /Storage/ })).toBeInTheDocument();
  });

  it("renders max file size value", () => {
    renderStorageDetail(baseConfig, "avatars");
    expect(screen.getByDisplayValue("5MB")).toBeInTheDocument();
  });

  it("shows not-found message for missing bucket", () => {
    renderStorageDetail(baseConfig, "nonexistent");
    expect(screen.getByText("Bucket not found.")).toBeInTheDocument();
  });
});
