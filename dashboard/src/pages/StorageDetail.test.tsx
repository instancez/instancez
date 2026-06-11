import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { StorageDetail } from "./StorageDetail";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const baseConfig: Config = {
  version: 1,
  project: { name: "P", description: "" },
  extensions: [],
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

function renderStorageDetail(config: Config, bucketName: string) {
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
  it("renders bucket name", () => {
    renderStorageDetail(baseConfig, "avatars");
    expect(screen.getByText("avatars")).toBeInTheDocument();
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
