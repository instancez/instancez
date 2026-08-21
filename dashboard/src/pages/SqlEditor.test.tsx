import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { BackendProvider } from "../console/BackendContext";
import { fullCapabilities, type ConsoleBackend } from "../console/backend";
import { SqlEditor } from "./SqlEditor";

function mk(runQuery: ConsoleBackend["runQuery"]) {
  return { capabilities: fullCapabilities(), runQuery } as unknown as ConsoleBackend;
}
function renderPage(b: ConsoleBackend) {
  return renderWithChakra(
    <BackendProvider backend={b}>
      <MemoryRouter><SqlEditor /></MemoryRouter>
    </BackendProvider>
  );
}
describe("SqlEditor", () => {
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
});
