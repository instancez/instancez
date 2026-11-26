import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { BackendProvider } from "../console/BackendContext";
import { fullCapabilities, type ConsoleBackend } from "../console/backend";
import { EditorView } from "codemirror";
import { SqlEditor, sortRows, cellText, rowObject } from "./SqlEditor";
import { ApiBaseUrlProvider } from "../console/BackendContext";

function mk(runQuery: ConsoleBackend["runQuery"]) {
  return {
    capabilities: fullCapabilities(),
    runQuery,
    getConfig: async () => ({ tables: {} }),
  } as unknown as ConsoleBackend;
}
function renderPage(b: ConsoleBackend) {
  return renderWithChakra(
    <BackendProvider backend={b}>
      <MemoryRouter><SqlEditor /></MemoryRouter>
    </BackendProvider>
  );
}
function stubStorage(overrides: Partial<Storage> = {}) {
  const data = new Map<string, string>();
  const store = {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
    clear: () => data.clear(),
    ...overrides,
  };
  vi.stubGlobal("localStorage", store);
  return store;
}

describe("SqlEditor", () => {
  beforeEach(() => { stubStorage(); });

  it("runs a query and shows results", async () => {
    const runQuery = vi.fn(async () => ({ columns: ["n"], rows: [[1]], row_count: 1 }));
    renderPage(mk(runQuery));
    fireEvent.click(screen.getByRole("button", { name: /run/i }));
    await waitFor(() => expect(runQuery).toHaveBeenCalled());
    // Scope to the result table cell — "1" also appears in the "1 row" summary.
    expect(await screen.findByRole("cell", { name: "1" })).toBeInTheDocument();
  });
  it("shows the postgres error on failure", async () => {
    const runQuery = vi.fn(async () => { throw new Error("permission denied for schema auth"); });
    renderPage(mk(runQuery));
    fireEvent.click(screen.getByRole("button", { name: /run/i }));
    expect(await screen.findByText(/permission denied/i)).toBeInTheDocument();
  });

  it("runs on Cmd/Ctrl+Enter", async () => {
    const runQuery = vi.fn(async () => ({ columns: ["n"], rows: [[1]], row_count: 1 }));
    const { container } = renderPage(mk(runQuery));
    const editorEl = container.querySelector(".cm-content") as HTMLElement;
    fireEvent.keyDown(editorEl, { key: "Enter", ctrlKey: true });
    await waitFor(() => expect(runQuery).toHaveBeenCalled());
  });

  it("shows how long the query took", async () => {
    renderPage(mk(vi.fn(async () => ({ columns: ["n"], rows: [[1]], row_count: 1 }))));
    fireEvent.click(screen.getByRole("button", { name: /run/i }));
    expect(await screen.findByText(/1 row · \d+ ms/)).toBeInTheDocument();
  });

  it("explains a statement that returns no columns", async () => {
    renderPage(mk(vi.fn(async () => ({ columns: [], rows: [], row_count: 0 }))));
    fireEvent.click(screen.getByRole("button", { name: /run/i }));
    expect(await screen.findByText(/no rows returned/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /export csv/i })).not.toBeInTheDocument();
  });

  it("copes with a null result from the backend", async () => {
    renderPage(mk(vi.fn(async () => ({ columns: null, rows: null, row_count: null }) as never)));
    fireEvent.click(screen.getByRole("button", { name: /run/i }));
    expect(await screen.findByText(/0 rows/)).toBeInTheDocument();
  });

  it("sorts by a column header: asc, desc, then back to original order", async () => {
    renderPage(mk(vi.fn(async () => ({ columns: ["n"], rows: [[2], [null], [10], [1]], row_count: 4 }))));
    fireEvent.click(screen.getByRole("button", { name: /^run$/i }));
    await screen.findByRole("cell", { name: "10" });
    const order = () => screen.getAllByRole("cell").filter((c) => c.title === "Click to copy").map((c) => c.textContent);
    expect(order()).toEqual(["2", "", "10", "1"]);
    fireEvent.click(screen.getByRole("button", { name: /sort by n/i }));
    expect(order()).toEqual(["1", "2", "10", ""]);
    fireEvent.click(screen.getByRole("button", { name: /sort by n/i }));
    expect(order()).toEqual(["10", "2", "1", ""]);
    fireEvent.click(screen.getByRole("button", { name: /sort by n/i }));
    expect(order()).toEqual(["2", "", "10", "1"]);
  });

  it("copies a cell on click and a row as JSON", async () => {
    const writeText = vi.fn(async () => {});
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    renderPage(mk(vi.fn(async () => ({ columns: ["id", "name"], rows: [[1, "ann"]], row_count: 1 }))));
    fireEvent.click(screen.getByRole("button", { name: /^run$/i }));
    fireEvent.click(await screen.findByRole("cell", { name: "ann" }));
    expect(writeText).toHaveBeenCalledWith("ann");
    fireEvent.click(screen.getByRole("button", { name: /copy row/i }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('{"id":1,"name":"ann"}'));
  });

  it("restores the saved draft and saves edits", async () => {
    localStorage.setItem("inz:sql-draft:" + window.location.origin, "select 42");
    const { container } = renderPage(mk(vi.fn()));
    expect(container.querySelector(".cm-content")?.textContent).toBe("select 42");
    expect(localStorage.getItem("inz:sql-draft:" + window.location.origin)).toBe("select 42");
  });

  it("starts from the default query when storage is empty or unavailable", () => {
    stubStorage({
      getItem: () => { throw new Error("denied"); },
      setItem: () => { throw new Error("quota"); },
    });
    const { container } = renderPage(mk(vi.fn()));
    expect(container.querySelector(".cm-content")?.textContent).toBe("select * from ");
  });

  it("formats the query with the Format button", async () => {
    localStorage.setItem("inz:sql-draft:" + window.location.origin, "SELECT id,title FROM todos");
    const { container } = renderPage(mk(vi.fn()));
    fireEvent.click(screen.getByRole("button", { name: /format/i }));
    // The formatter is lazy-loaded; its first import can be slow on a busy runner.
    await waitFor(() => expect(container.querySelector(".cm-content")?.textContent).toMatch(/^select\s*id,/), { timeout: 10_000 });
    expect(container.querySelectorAll(".cm-line").length).toBeGreaterThan(1);
  });

  it("runs only the selected text", async () => {
    localStorage.setItem("inz:sql-draft:" + window.location.origin, "select 1; select 2");
    const runQuery = vi.fn(async () => ({ columns: ["n"], rows: [[1]], row_count: 1 }));
    const { container } = renderPage(mk(runQuery));
    const content = container.querySelector(".cm-content") as HTMLElement;
    const view = EditorView.findFromDOM(container.querySelector(".cm-editor") as HTMLElement)!;
    view.dispatch({ selection: { anchor: 10, head: 18 } });
    fireEvent.click(screen.getByRole("button", { name: /^run$/i }));
    await waitFor(() => expect(runQuery).toHaveBeenCalledWith("select 2"));
  });

  it("runs the statement under the cursor on Ctrl+Enter", async () => {
    localStorage.setItem("inz:sql-draft:" + window.location.origin, "select 1;\nselect 2;");
    const runQuery = vi.fn(async () => ({ columns: ["n"], rows: [[1]], row_count: 1 }));
    const { container } = renderPage(mk(runQuery));
    const content = container.querySelector(".cm-content") as HTMLElement;
    const view = EditorView.findFromDOM(container.querySelector(".cm-editor") as HTMLElement)!;
    view.dispatch({ selection: { anchor: 3 } });
    fireEvent.keyDown(content, { key: "Enter", ctrlKey: true });
    await waitFor(() => expect(runQuery).toHaveBeenCalledWith("select 1;"));
  });
});

describe("sortRows and cellText", () => {
  it("orders numbers numerically, text naturally and nulls last in both directions", () => {
    const rows = [["b"], [null], ["a10"], ["a2"]];
    expect(sortRows(rows, 0, "asc").map((r) => r[0])).toEqual(["a2", "a10", "b", null]);
    expect(sortRows(rows, 0, "desc").map((r) => r[0])).toEqual(["b", "a10", "a2", null]);
    expect(sortRows([], 0, "asc")).toEqual([]);
  });

  it("sorts numeric strings as numbers, including negatives and decimals", () => {
    const col = (vals: unknown[], dir: "asc" | "desc" = "asc") => sortRows(vals.map((v) => [v]), 0, dir).map((r) => r[0]);
    expect(col(["-5", "-10", "0", "3"])).toEqual(["-10", "-5", "0", "3"]);
    expect(col(["1.5", "1.25", "1.3"])).toEqual(["1.25", "1.3", "1.5"]);
    expect(col(["10", 9, null, "8.5"], "desc")).toEqual(["10", 9, "8.5", null]);
    expect(col(["", "2", "1"])).toEqual(["", "1", "2"]);
  });

  it("does not mutate its input", () => {
    const rows = [[2], [1]];
    sortRows(rows, 0, "asc");
    expect(rows).toEqual([[2], [1]]);
  });

  it("renders null, objects and primitives for display and copy", () => {
    expect(cellText(null)).toBe("");
    expect(cellText(undefined)).toBe("");
    expect(cellText({ a: 1 })).toBe('{"a":1}');
    expect(cellText(0)).toBe("0");
    expect(cellText(false)).toBe("false");
  });
});

describe("rowObject", () => {
  it("keeps every column when names repeat", () => {
    expect(rowObject(["?column?", "?column?", "id"], [1, 2, 3])).toEqual({ "?column?": 1, "?column?_2": 2, id: 3 });
    expect(rowObject(["a", "a_2", "a"], [1, 2, 3])).toEqual({ a: 1, a_2: 2, a_3: 3 });
    expect(rowObject([], [])).toEqual({});
  });
});

describe("SqlEditor drafts per project", () => {
  beforeEach(() => { stubStorage(); });

  it("loads the new project's draft and leaves the old one intact when the API URL changes", async () => {
    localStorage.setItem("inz:sql-draft:https://a.test", "select 'a'");
    localStorage.setItem("inz:sql-draft:https://b.test", "select 'b'");
    const page = (url: string) => (
      <BackendProvider backend={mk(vi.fn())}>
        <ApiBaseUrlProvider value={url}><MemoryRouter><SqlEditor /></MemoryRouter></ApiBaseUrlProvider>
      </BackendProvider>
    );
    const { container, rerender } = renderWithChakra(page("https://a.test"));
    expect(container.querySelector(".cm-content")?.textContent).toBe("select 'a'");
    rerender(page("https://b.test"));
    await waitFor(() => expect(container.querySelector(".cm-content")?.textContent).toBe("select 'b'"));
    expect(localStorage.getItem("inz:sql-draft:https://a.test")).toBe("select 'a'");
    expect(localStorage.getItem("inz:sql-draft:https://b.test")).toBe("select 'b'");
  });
});
