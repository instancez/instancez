import { describe, it, expect, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Link, MemoryRouter, Route, Routes } from "react-router-dom";
import { TableDetail } from "./TableDetail";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
import { renderWithChakra } from "../test/helpers";
import type { Config } from "../lib/types";

const config = {
  version: 1,
  project: { name: "P", description: "" },
  tables: {
    todos: {
      fields: [{ name: "id", type: "uuid", primary_key: true }],
      indexes: [],
      rls_enabled: true,
      rls: [],
    },
    notes: {
      fields: [{ name: "id", type: "uuid", primary_key: true }],
      indexes: [{ columns: ["id"], unique: false, where: "" }],
      rls_enabled: true,
      rls: [{ operations: ["select"], using: "true" }, { operations: ["insert"], with_check: "true" }],
    },
  },
  auth: null, storage: {}, rpc: {}, functions: {}, providers: { email: null, storage: null },
} as unknown as Config;

function mount(url: string, canWriteConfig = true) {
  const save = vi.fn().mockResolvedValue(true);
  const ctx = {
    config, loading: false, error: null, checksum: "c", saving: false, saveErrors: [],
    dotenvWritable: false, oauthCallbackBase: "", refresh: vi.fn(), save, updateConfig: vi.fn(),
  };
  const backend = { ...adminBackend, capabilities: { ...adminBackend.capabilities, canWriteConfig } };
  renderWithChakra(
    <BackendProvider backend={backend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter initialEntries={[url]}>
          <DialogProvider>
            <Routes>
              <Route path="/tables" element={<div>list page</div>} />
              <Route path="/tables/:name" element={<><Link to="/tables/notes?tab=rls">jump rls</Link><TableDetail /></>} />
            </Routes>
          </DialogProvider>
        </MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
  return save;
}

describe("TableDetail delete", () => {
  it("has no delete control; deleting lives in the tables list", async () => {
    mount("/tables/todos?tab=fields");
    await screen.findByText("Fields", { selector: "button" });
    expect(screen.queryByRole("button", { name: /delete table/i })).toBeNull();
    expect(screen.queryByText("Delete this table")).toBeNull();
  });
});

describe("TableDetail tabs", () => {
  it("opens the tab named by ?tab=", async () => {
    mount("/tables/notes?tab=rls");
    expect(await screen.findByText("Policy 1")).toBeInTheDocument();
  });

  it("ignores an unknown ?tab= value", async () => {
    mount("/tables/notes?tab=nope");
    expect(await screen.findByRole("tab", { name: "Data", selected: true })).toBeInTheDocument();
  });

  it("unknown table shows not-found, not a skeleton", async () => {
    mount("/tables/ghost");
    expect(await screen.findByText("Table not found.")).toBeInTheDocument();
  });
});

describe("TableDetail indexes and RLS empty states", () => {
  it("empty indexes shows an explanation and a single Add index button", async () => {
    mount("/tables/todos?tab=indexes");
    expect(await screen.findByText("No indexes")).toBeInTheDocument();
    expect(screen.getByText(/speed up filters and sorts/i)).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /add index/i })).toHaveLength(1);
    await userEvent.click(screen.getByRole("button", { name: /add index/i }));
    expect(await screen.findByLabelText("Delete index")).toBeInTheDocument();
    expect(screen.queryByText("No indexes")).toBeNull();
  });

  it("existing indexes list without the empty state", async () => {
    mount("/tables/notes?tab=indexes");
    expect(await screen.findByLabelText("Delete index")).toBeInTheDocument();
    expect(screen.queryByText("No indexes")).toBeNull();
  });

  it("RLS tab shows the policy count, numbered cards and a security link for that table", async () => {
    mount("/tables/notes?tab=rls");
    expect(await screen.findByText(/2 policies/)).toBeInTheDocument();
    expect(screen.getByText("Policy 2")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /view security findings/i })).toHaveAttribute("href", "/security?table=notes");
  });

  it("RLS tab with zero policies explains deny-all and offers one Add button", async () => {
    mount("/tables/todos?tab=rls");
    expect(await screen.findByText("No policies")).toBeInTheDocument();
    expect(screen.getByText(/all access is denied/i)).toBeInTheDocument();
    expect(screen.getByText(/0 policies/)).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /add rls policy/i })).toHaveLength(1);
  });
});

describe("TableDetail tab from URL", () => {
  it("a ?tab link to the already-mounted table switches tabs", async () => {
    mount("/tables/notes?tab=fields");
    expect(await screen.findByRole("tab", { name: "Fields" })).toHaveAttribute("aria-selected", "true");
    await userEvent.click(screen.getByText("jump rls"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "RLS" })).toHaveAttribute("aria-selected", "true"));
  });

  it("the same link works after the user picked another tab by hand", async () => {
    mount("/tables/notes?tab=rls");
    await userEvent.click(await screen.findByRole("tab", { name: "Indexes" }));
    await waitFor(() => expect(screen.getByRole("tab", { name: "Indexes" })).toHaveAttribute("aria-selected", "true"));
    await userEvent.click(screen.getByText("jump rls"));
    await waitFor(() => expect(screen.getByRole("tab", { name: "RLS" })).toHaveAttribute("aria-selected", "true"));
  });

  it("an unknown ?tab falls back to data", async () => {
    mount("/tables/notes?tab=bogus");
    expect(await screen.findByRole("tab", { name: "Data" })).toHaveAttribute("aria-selected", "true");
  });
});
