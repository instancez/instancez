import { describe, it, expect, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { Tables } from "./Tables";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
import { renderWithChakra } from "../test/helpers";
import type { Config } from "../lib/types";

const table = { fields: [{ name: "id", type: "uuid", primary_key: true }], indexes: [], rls_enabled: true, rls: [] };
const config = {
  version: 1,
  project: { name: "P", description: "" },
  tables: { todos: table, notes: table },
  auth: null, storage: {}, rpc: {}, functions: {}, providers: { email: null, storage: null },
} as unknown as Config;

function mount(canWriteConfig = true) {
  const save = vi.fn().mockResolvedValue(true);
  const ctx = {
    config, loading: false, error: null, checksum: "c", saving: false, saveErrors: [],
    dotenvWritable: false, oauthCallbackBase: "", refresh: vi.fn(), save, updateConfig: vi.fn(),
  };
  const backend = { ...adminBackend, capabilities: { ...adminBackend.capabilities, canWriteConfig } };
  renderWithChakra(
    <BackendProvider backend={backend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter>
          <DialogProvider><Tables /></DialogProvider>
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
  return save;
}

describe("Tables list delete", () => {
  it("requires the exact table name, then saves without that table", async () => {
    const save = mount();
    await userEvent.click(await screen.findByRole("button", { name: "Delete table todos" }));
    expect(await screen.findByText("Delete table?")).toBeInTheDocument();
    const del = screen.getByRole("button", { name: "Delete" });
    expect(del).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox"), "todo");
    expect(del).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox"), "s");
    await userEvent.click(del);
    await waitFor(() => expect(save).toHaveBeenCalledTimes(1));
    expect(save.mock.calls[0]![0].tables).not.toHaveProperty("todos");
    expect(save.mock.calls[0]![0].tables).toHaveProperty("notes");
  });

  it("Escape deletes nothing", async () => {
    const save = mount();
    await userEvent.click(await screen.findByRole("button", { name: "Delete table notes" }));
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByText("Delete table?")).toBeNull());
    expect(save).not.toHaveBeenCalled();
  });

  it("is hidden for read-only backends", async () => {
    mount(false);
    await screen.findByText("todos");
    expect(screen.queryByRole("button", { name: /delete table/i })).toBeNull();
  });
});
