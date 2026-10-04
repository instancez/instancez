import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { ObjectBrowser } from "./ObjectBrowser";
import { DialogProvider } from "./Dialog";
import { BackendProvider } from "../console/BackendContext";
import { fullCapabilities, type ConsoleBackend } from "../console/backend";

const obj = (name: string) => ({ name, id: name, updated_at: "", metadata: { size: 10, mimetype: "image/png" } });
const page = (names: string[], next?: string) => ({
  folders: [], objects: names.map(obj), has_next: !!next, next_cursor: next,
});
const view = (b: Partial<ConsoleBackend>) =>
  renderWithChakra(
    <BackendProvider backend={{ capabilities: fullCapabilities(), ...b } as ConsoleBackend}>
      <DialogProvider><ObjectBrowser bucket="uploads" /></DialogProvider>
    </BackendProvider>
  );

describe("ObjectBrowser", () => {
  it("opens a folder and lists its content immediately", async () => {
    const listObjects = vi.fn(async (_b: string, prefix: string) =>
      prefix === "" ? { folders: [{ name: "photos", key: "photos/" }], objects: [], has_next: false } : page(["a.png"]));
    view({ listObjects });
    fireEvent.click(await screen.findByText("photos"));
    expect(await screen.findByText("a.png")).toBeInTheDocument();
    expect(listObjects).toHaveBeenLastCalledWith("uploads", "photos/", undefined);
  });

  it("pages with the cursor and goes back", async () => {
    const listObjects = vi.fn(async (_b: string, _p: string, cursor?: string) =>
      cursor === "c1" ? page(["second.png"]) : page(["first.png"], "c1"));
    view({ listObjects });
    expect(await screen.findByText("first.png")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /next/i }));
    expect(await screen.findByText("second.png")).toBeInTheDocument();
    expect(listObjects).toHaveBeenLastCalledWith("uploads", "", "c1");
    expect(screen.getByRole("button", { name: /next/i })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /prev/i }));
    expect(await screen.findByText("first.png")).toBeInTheDocument();
  });

  it("has no pager for a single page", async () => {
    view({ listObjects: vi.fn(async () => page(["a.png"])) });
    await screen.findByText("a.png");
    expect(screen.queryByRole("button", { name: /next/i })).not.toBeInTheDocument();
  });

  it("deletes via the action menu, rendered outside the table", async () => {
    const deleteObjects = vi.fn(async () => {});
    view({ listObjects: vi.fn(async () => page(["a.png"])), deleteObjects });
    await screen.findByText("a.png");
    fireEvent.click(screen.getByLabelText("Actions"));
    const item = await screen.findByRole("menuitem", { name: "Delete" });
    expect(item.closest("table")).toBeNull();
    fireEvent.click(item);
    fireEvent.click(await screen.findByRole("button", { name: /^delete$/i }));
    await waitFor(() => expect(deleteObjects).toHaveBeenCalledWith("uploads", ["a.png"]));
  });

  it("shows the error, not the empty state, when the list fails", async () => {
    view({ listObjects: vi.fn(async () => { throw new Error("boom"); }) });
    expect(await screen.findByText("boom")).toBeInTheDocument();
    expect(screen.queryByText("Empty")).not.toBeInTheDocument();
  });

  it("steps back a page when a later page comes back empty", async () => {
    const listObjects = vi.fn(async (_b: string, _p: string, cursor?: string) =>
      cursor === "c1" ? page([]) : page(["first.png"], "c1"));
    view({ listObjects });
    await screen.findByText("first.png");
    fireEvent.click(screen.getByRole("button", { name: /next/i }));
    await waitFor(() => expect(listObjects).toHaveBeenCalledWith("uploads", "", "c1"));
    await waitFor(() => expect(listObjects).toHaveBeenCalledTimes(3));
    expect(await screen.findByText("first.png")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /prev/i })).toBeDisabled();
  });
});
