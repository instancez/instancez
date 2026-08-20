import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
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

  it("deletes an object via the context menu", async () => {
    const deleteObjects = vi.fn(async () => {});
    const backend = {
      capabilities: fullCapabilities(),
      listObjects: vi.fn(async () => ({
        folders: [],
        objects: [{ name: "a.png", id: "a.png", updated_at: "2026-08-16", metadata: { size: 10, mimetype: "image/png" } }],
        has_next: false,
      })),
      deleteObjects,
    } as unknown as ConsoleBackend;

    renderStorage(baseConfig, backend);

    // Open the bucket → file explorer.
    fireEvent.click(screen.getByText("uploads"));
    // Wait for the object row to load.
    expect(await screen.findByText("a.png")).toBeInTheDocument();

    // Open the row action menu, click Delete, confirm the dialog.
    fireEvent.click(screen.getByLabelText("Actions"));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    fireEvent.click(await screen.findByRole("button", { name: /^delete$/i }));

    await waitFor(() => expect(deleteObjects).toHaveBeenCalledWith("uploads", ["a.png"]));
  });
});
