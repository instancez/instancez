import { describe, it, expect, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { Tables } from "./Tables";
import { Rpc } from "./Rpc";
import { Storage } from "./Storage";
import { Functions } from "./Functions";
import { DialogProvider } from "../components/Dialog";
import { ConfigContext } from "../hooks/useConfig";
import { BackendProvider } from "../console/BackendContext";
import { adminBackend } from "../console/adminBackend";
import type { Config } from "../lib/types";

const nav = vi.hoisted(() => vi.fn());
vi.mock("react-router-dom", async () => ({
  ...(await vi.importActual<typeof import("react-router-dom")>("react-router-dom")),
  useNavigate: () => nav,
}));

const backend = {
  ...adminBackend,
  getFunctionDeps: vi.fn().mockRejectedValue(new Error("501")),
  getStats: vi.fn().mockResolvedValue({ storage: {} }),
};

const config = {
  version: 1,
  project: { name: "P", description: "" },
  tables: { posts: { fields: [], indexes: [], rls: [] } },
  auth: null,
  storage: { uploads: { max_size: "5MB", public: false, types: [], rls: [] } },
  rpc: { calc: { language: "plpgsql", args: [] } },
  functions: { hello: { runtime: "node", file: "functions/hello.js" } },
  providers: { email: null, storage: null },
} as unknown as Config;

function mount(ui: React.ReactElement, save = vi.fn().mockResolvedValue(true)) {
  const ctx = {
    config, loading: false, error: null, checksum: "x", saving: false, saveErrors: [],
    dotenvWritable: false, oauthCallbackBase: "", refresh: vi.fn(), save, updateConfig: vi.fn(),
  };
  renderWithChakra(
    <BackendProvider backend={backend}>
      <ConfigContext.Provider value={ctx}>
        <MemoryRouter><DialogProvider>{ui}</DialogProvider></MemoryRouter>
      </ConfigContext.Provider>
    </BackendProvider>
  );
  return save;
}

const cases = [
  { kind: "table", Page: Tables, button: /add table/i, label: "Table name", existing: "posts" },
  { kind: "function (rpc)", Page: Rpc, button: /add function/i, label: "Function name", existing: "calc" },
  { kind: "bucket", Page: Storage, button: /add bucket/i, label: "Bucket name", existing: "uploads" },
  { kind: "code function", Page: Functions, button: /new function/i, label: "Function name", existing: "hello" },
] as const;

describe.each(cases)("create $kind dialog", ({ Page, button, label, existing }) => {
  it("shows naming rules, rejects bad names and duplicates inline, blocks Create", async () => {
    mount(<Page />);
    await userEvent.click(await screen.findByRole("button", { name: button }));
    expect(screen.getByRole("textbox")).toHaveFocus();
    expect(screen.getByText(/lowercase letters, digits and underscores/i)).toBeInTheDocument();
    const create = screen.getByRole("button", { name: "Create" });
    expect(create).toBeDisabled();

    await userEvent.type(screen.getByRole("textbox"), "Bad Name");
    expect(screen.getByRole("alert")).toHaveTextContent(/lowercase/i);
    expect(create).toBeDisabled();

    await userEvent.clear(screen.getByRole("textbox"));
    await userEvent.type(screen.getByRole("textbox"), existing);
    expect(screen.getByRole("alert")).toHaveTextContent(/already exists/i);
    expect(create).toBeDisabled();
  });

  it("Escape closes without creating anything", async () => {
    const save = mount(<Page />);
    await userEvent.click(await screen.findByRole("button", { name: button }));
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(save).not.toHaveBeenCalled();
    expect(nav).not.toHaveBeenCalled();
  });
});

describe("create dialog success", () => {
  it("a valid table name navigates to the new-table editor", async () => {
    nav.mockClear();
    mount(<Tables />);
    await userEvent.click(await screen.findByRole("button", { name: /add table/i }));
    await userEvent.type(screen.getByRole("textbox"), "orders{Enter}");
    await waitFor(() => expect(nav).toHaveBeenCalledWith("new", expect.objectContaining({ state: expect.objectContaining({ tableName: "orders" }) })));
  });
});
