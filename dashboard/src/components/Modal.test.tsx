import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { Modal } from "./Modal";

describe("Modal", () => {
  it("renders title and children when open", () => {
    renderWithChakra(
      <Modal open onClose={vi.fn()} title="Test title">
        <Modal.Body>body content</Modal.Body>
      </Modal>
    );
    expect(screen.getByText("Test title")).toBeInTheDocument();
    expect(screen.getByText("body content")).toBeInTheDocument();
  });

  it("renders nothing when closed", () => {
    renderWithChakra(
      <Modal open={false} onClose={vi.fn()} title="Hidden">
        <Modal.Body>hidden content</Modal.Body>
      </Modal>
    );
    expect(screen.queryByText("Hidden")).not.toBeInTheDocument();
    expect(screen.queryByText("hidden content")).not.toBeInTheDocument();
  });

  it("calls onClose when × button clicked", () => {
    const onClose = vi.fn();
    renderWithChakra(
      <Modal open onClose={onClose} title="T">
        <Modal.Body>body</Modal.Body>
      </Modal>
    );
    fireEvent.click(screen.getByRole("button", { name: /close/i }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("calls onClose when backdrop clicked", () => {
    const onClose = vi.fn();
    renderWithChakra(
      <Modal open onClose={onClose} title="T">
        <Modal.Body>body</Modal.Body>
      </Modal>
    );
    fireEvent.click(screen.getByTestId("modal-backdrop"));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("calls onClose on Escape key", () => {
    const onClose = vi.fn();
    renderWithChakra(
      <Modal open onClose={onClose} title="T">
        <Modal.Body>body</Modal.Body>
      </Modal>
    );
    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("renders Modal.Footer children", () => {
    renderWithChakra(
      <Modal open onClose={vi.fn()} title="T">
        <Modal.Body>body</Modal.Body>
        <Modal.Footer>
          <button>Save</button>
        </Modal.Footer>
      </Modal>
    );
    expect(screen.getByText("Save")).toBeInTheDocument();
  });

  it("returns focus to the opener when it closes", () => {
    function Host() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button onClick={() => setOpen(true)}>opener</button>
          <Modal open={open} onClose={() => setOpen(false)} title="T"><Modal.Body><input aria-label="f" /></Modal.Body></Modal>
        </>
      );
    }
    renderWithChakra(<Host />);
    const opener = screen.getByText("opener");
    opener.focus();
    fireEvent.click(opener);
    expect(screen.getByLabelText("f")).toHaveFocus();
    fireEvent.click(screen.getByRole("button", { name: /close/i }));
    expect(opener).toHaveFocus();
  });

  it("moves focus to a button when there is no field", () => {
    renderWithChakra(<Modal open onClose={vi.fn()} title="T"><Modal.Body>plain</Modal.Body></Modal>);
    expect(screen.getByRole("dialog").contains(document.activeElement)).toBe(true);
  });
});
