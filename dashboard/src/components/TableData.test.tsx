import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { TableData } from "./TableData";
import { BackendProvider } from "../console/BackendContext";
import { fullCapabilities, type ConsoleBackend } from "../console/backend";

const mk = (runQuery: unknown) =>
  ({ capabilities: fullCapabilities(), runQuery }) as unknown as ConsoleBackend;
const view = (b: ConsoleBackend) =>
  renderWithChakra(<BackendProvider backend={b}><TableData name="t" primaryKey="id" /></BackendProvider>);

describe("TableData", () => {
  it("shows an empty state for zero rows (null rows)", async () => {
    view(mk(vi.fn(async () => ({ columns: null, rows: null, row_count: null }))));
    expect(await screen.findByText("No rows.")).toBeInTheDocument();
  });

  it("shows the query error", async () => {
    view(mk(vi.fn(async () => { throw new Error('relation "t" does not exist'); })));
    expect(await screen.findByText('relation "t" does not exist')).toBeInTheDocument();
  });

  it("renders null and object cells and pages forward using the extra row", async () => {
    const full = Array.from({ length: 21 }, (_, i) => [i, i === 0 ? null : { a: 1 }]);
    const runQuery = vi.fn(async (sql: string) =>
      sql.includes("OFFSET 20")
        ? { columns: ["id", "v"], rows: [[99, "last"]], row_count: 1 }
        : { columns: ["id", "v"], rows: full, row_count: 21 });
    view(mk(runQuery));
    expect((await screen.findAllByText('{"a":1}')).length).toBe(19);
    expect(screen.queryByText("20")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /next/i }));
    expect(await screen.findByText("last")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /next/i })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /prev/i }));
    await waitFor(() => expect(runQuery).toHaveBeenLastCalledWith(expect.stringContaining("OFFSET 0")));
  });
});
