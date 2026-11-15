import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { DialogFrame } from "./DialogFrame";

const primary = (over = {}) => ({ label: "Go", onClick: vi.fn(), ...over });

describe("DialogFrame", () => {
  it("renders title, description, body, then Cancel before the primary action", () => {
    renderWithChakra(
      <DialogFrame title="T" description="Why" onClose={vi.fn()} primary={primary()}>body</DialogFrame>
    );
    expect(screen.getByText("Why")).toBeInTheDocument();
    expect(screen.getByText("body")).toBeInTheDocument();
    const names = screen.getAllByRole("button").map((b) => b.getAttribute("aria-label") ?? b.textContent);
    expect(names).toEqual(expect.arrayContaining(["Cancel", "Go", "Close"]));
    expect(names.indexOf("Cancel")).toBeLessThan(names.indexOf("Go"));
  });

  it("Cancel closes and the primary action fires", () => {
    const onClose = vi.fn();
    const p = primary();
    renderWithChakra(<DialogFrame title="T" onClose={onClose} primary={p} />);
    fireEvent.click(screen.getByRole("button", { name: "Go" }));
    expect(p.onClick).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("secondary=false drops Cancel", () => {
    renderWithChakra(<DialogFrame title="T" onClose={vi.fn()} primary={primary()} secondary={false} />);
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
  });

  it("danger tone recolors only the primary button and keeps the layout neutral", () => {
    const render = (tone: "default" | "danger") =>
      renderWithChakra(<DialogFrame title="T" tone={tone} onClose={vi.fn()} primary={primary()} />);
    const normal = render("default");
    const normalClass = screen.getByRole("button", { name: "Cancel" }).className;
    const normalPrimary = screen.getByRole("button", { name: "Go" }).className;
    normal.unmount();
    render("danger");
    expect(screen.getByRole("alertdialog", { name: "T" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cancel" }).className).toBe(normalClass);
    expect(screen.getByRole("button", { name: "Go" }).className).not.toBe(normalPrimary);
    expect(screen.getByRole("alertdialog").innerHTML).not.toMatch(/red\.|svg.*lucide-triangle/);
  });

  it("busy blocks Close, Cancel, Escape and backdrop", () => {
    const onClose = vi.fn();
    renderWithChakra(<DialogFrame title="T" busy onClose={onClose} primary={primary()} />);
    expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Close" })).toBeDisabled();
    fireEvent.keyDown(window, { key: "Escape" });
    fireEvent.click(screen.getByTestId("modal-backdrop"));
    expect(onClose).not.toHaveBeenCalled();
  });

  it("shows an error as an alert and omits the body when empty", () => {
    const first = renderWithChakra(<DialogFrame title="T" error="Nope" onClose={vi.fn()} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Nope");
    first.unmount();
    renderWithChakra(<DialogFrame title="T" onClose={vi.fn()} />);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("dirty ignores backdrop clicks but Escape still closes", () => {
    const onClose = vi.fn();
    renderWithChakra(<DialogFrame title="T" dirty onClose={onClose} />);
    fireEvent.click(screen.getByTestId("modal-backdrop"));
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("a disabled primary cannot fire", () => {
    const p = primary({ disabled: true });
    renderWithChakra(<DialogFrame title="T" onClose={vi.fn()} primary={p} />);
    fireEvent.click(screen.getByRole("button", { name: "Go" }));
    expect(p.onClick).not.toHaveBeenCalled();
  });
});
