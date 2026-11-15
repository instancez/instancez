import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { act, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithChakra } from "../test/helpers";
import { DialogProvider, useDialog } from "./Dialog";
import { Modal } from "./Modal";

type Opener = (d: ReturnType<typeof useDialog>) => Promise<unknown>;

function Page({ open, onResult }: { open: Opener; onResult: (v: unknown) => void }) {
  const dialog = useDialog();
  return <button onClick={() => void open(dialog).then(onResult)}>open</button>;
}

function Host({ open, onResult }: { open: Opener; onResult: (v: unknown) => void }) {
  const [mounted, setMounted] = useState(true);
  return (
    <DialogProvider>
      <button onClick={() => setMounted(false)}>leave</button>
      {mounted && <Page open={open} onResult={onResult} />}
    </DialogProvider>
  );
}

const validate = (v: string) => (/^[a-z][a-z0-9_]*$/.test(v) ? null : "Use lowercase letters, digits and underscores");

async function openDialog(open: Opener) {
  const onResult = vi.fn();
  renderWithChakra(<Host open={open} onResult={onResult} />);
  await userEvent.click(screen.getByText("open"));
  return onResult;
}

describe("DialogProvider", () => {
  it("Escape closes a prompt and resolves null", async () => {
    const onResult = await openDialog((d) => d.prompt("Name"));
    fireEvent.keyDown(document.body, { key: "Escape" });
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(null));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("Escape still works when an inner handler stops propagation", async () => {
    const onResult = await openDialog((d) => d.confirm("Delete x?"));
    const stop = (e: KeyboardEvent) => e.stopPropagation();
    document.body.addEventListener("keydown", stop);
    fireEvent.keyDown(document.body, { key: "Escape" });
    document.body.removeEventListener("keydown", stop);
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(false));
  });

  it("closes when the page that opened it unmounts (tab change)", async () => {
    const onResult = await openDialog((d) => d.prompt("Name"));
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    await userEvent.click(screen.getByText("leave"));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(onResult).toHaveBeenCalledWith(null);
  });

  it("backdrop click closes a pristine prompt but not one with typed input", async () => {
    const onResult = await openDialog((d) => d.prompt("Name"));
    await userEvent.type(screen.getByRole("textbox"), "abc");
    fireEvent.click(screen.getByTestId("modal-backdrop"));
    expect(onResult).not.toHaveBeenCalled();
    await userEvent.clear(screen.getByRole("textbox"));
    fireEvent.click(screen.getByTestId("modal-backdrop"));
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(null));
  });

  it("prompt shows the hint, flags invalid input inline, and disables Create", async () => {
    const onResult = await openDialog((d) => d.prompt("Table name", { message: "Lowercase, digits, underscore", validate }));
    expect(screen.getByText("Lowercase, digits, underscore")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox"), "Bad Name");
    expect(screen.getByRole("alert")).toHaveTextContent("lowercase");
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
    await userEvent.keyboard("{Enter}");
    expect(onResult).not.toHaveBeenCalled();
  });

  it("Enter submits a valid prompt value, trimmed", async () => {
    const onResult = await openDialog((d) => d.prompt("Table name", { validate }));
    await userEvent.type(screen.getByRole("textbox"), "  posts  {Enter}");
    await waitFor(() => expect(onResult).toHaveBeenCalledWith("posts"));
  });

  it("whitespace-only input never submits", async () => {
    const onResult = await openDialog((d) => d.prompt("Name"));
    await userEvent.type(screen.getByRole("textbox"), "   {Enter}");
    expect(screen.getByRole("button", { name: "Create" })).toBeDisabled();
    expect(onResult).not.toHaveBeenCalled();
  });

  it("typed-name confirm keeps Delete locked until the name matches", async () => {
    const onResult = await openDialog((d) => d.confirm("Delete table?", { confirmText: "posts" }));
    const del = screen.getByRole("button", { name: "Delete" });
    expect(del).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox"), "post");
    expect(del).toBeDisabled();
    await userEvent.type(screen.getByRole("textbox"), "s");
    await userEvent.click(del);
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(true));
  });

  it("a double click on Delete resolves once", async () => {
    const resolve = vi.fn();
    await openDialog((d) => d.confirm("Delete x?").then((v) => { resolve(v); return v; }));
    const del = screen.getByRole("button", { name: "Delete" });
    await act(async () => { fireEvent.click(del); fireEvent.click(del); });
    await waitFor(() => expect(resolve).toHaveBeenCalledTimes(1));
  });

  it("opening a dialog over an open one resolves the old promise as dismissed", async () => {
    let dlg!: ReturnType<typeof useDialog>;
    function Grab() { dlg = useDialog(); return null; }
    renderWithChakra(<DialogProvider><Grab /></DialogProvider>);
    const first = vi.fn();
    const second = vi.fn();
    act(() => { void dlg.confirm("First?").then(first); });
    act(() => { void dlg.prompt("Second").then(second); });
    await waitFor(() => expect(first).toHaveBeenCalledWith(false));
    expect(second).not.toHaveBeenCalled();
    expect(screen.getByRole("textbox")).toBeInTheDocument();
  });

  it("a dialog opened right after another closes is not wiped by the close timer", async () => {
    let dlg!: ReturnType<typeof useDialog>;
    function Grab() { dlg = useDialog(); return null; }
    renderWithChakra(<DialogProvider><Grab /></DialogProvider>);
    let first!: Promise<unknown>;
    act(() => { first = dlg.confirm("First?"); });
    fireEvent.keyDown(document.body, { key: "Escape" });
    await first;
    act(() => { void dlg.confirm("Second?"); });
    await new Promise((r) => setTimeout(r, 250));
    expect(screen.getByText("Second?")).toBeInTheDocument();
  });
});

describe("DialogProvider regressions", () => {
  it("a confirm then prompt in one handler resolves the first as dismissed", async () => {
    let dlg!: ReturnType<typeof useDialog>;
    function Grab() { dlg = useDialog(); return null; }
    renderWithChakra(<DialogProvider><Grab /></DialogProvider>);
    const first = vi.fn();
    act(() => { void dlg.confirm("First?").then(first); void dlg.prompt("Second"); });
    await waitFor(() => expect(first).toHaveBeenCalledWith(false));
    expect(screen.getByRole("textbox")).toBeInTheDocument();
  });

  it("another page unmounting leaves an open dialog visible", async () => {
    let dlg!: ReturnType<typeof useDialog>;
    function Owner() { dlg = useDialog(); return null; }
    function Leaver() { useDialog(); return null; }
    function Shell() {
      const [show, setShow] = useState(true);
      return <><button onClick={() => setShow(false)}>drop</button>{show && <Leaver />}<Owner /></>;
    }
    renderWithChakra(<DialogProvider><Shell /></DialogProvider>);
    const result = vi.fn();
    act(() => { void dlg.confirm("Keep me?").then(result); });
    await userEvent.click(screen.getByText("drop"));
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.getByText("Keep me?")).toBeInTheDocument();
    expect(result).not.toHaveBeenCalled();
  });

  it("prompt dialogs are named by their title", async () => {
    await openDialog((d) => d.prompt("Table name"));
    expect(screen.getByRole("dialog", { name: "Table name" })).toBeInTheDocument();
  });

  it("a non-destructive confirm with confirmText is not locked", async () => {
    const onResult = await openDialog((d) => d.confirm("Continue?", { destructive: false, confirmText: "go", confirmLabel: "Continue" }));
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(true));
  });
});

describe("Modal stacking", () => {
  it("Escape closes only the top-most overlay", async () => {
    const onClose = vi.fn();
    function Inner() {
      const dialog = useDialog();
      return <button onClick={() => void dialog.confirm("Delete user?")}>del</button>;
    }
    renderWithChakra(
      <DialogProvider>
        <Modal open onClose={onClose} title="Edit user"><Inner /></Modal>
      </DialogProvider>
    );
    await userEvent.click(screen.getByText("del"));
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(onClose).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByText("Delete user?")).toBeNull());
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("Escape right after an overlay unmounts reaches the one beneath", async () => {
    const onClose = vi.fn();
    function Inner() {
      const dialog = useDialog();
      return <button onClick={() => void dialog.confirm("Delete user?")}>del</button>;
    }
    renderWithChakra(
      <DialogProvider>
        <Modal open onClose={onClose} title="Edit user"><Inner /></Modal>
      </DialogProvider>
    );
    await userEvent.click(screen.getByText("del"));
    fireEvent.keyDown(document.body, { key: "Escape" });
    await new Promise<void>((resolve) => {
      const mo = new MutationObserver(() => {
        if (screen.queryByText("Delete user?")) return;
        mo.disconnect();
        fireEvent.keyDown(document.body, { key: "Escape" });
        resolve();
      });
      mo.observe(document.body, { childList: true, subtree: true });
    });
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("dirty modal ignores backdrop clicks, pristine one closes", () => {
    const onClose = vi.fn();
    const first = renderWithChakra(<Modal open dirty onClose={onClose} title="T">x</Modal>);
    fireEvent.click(screen.getByTestId("modal-backdrop"));
    expect(onClose).not.toHaveBeenCalled();
    first.unmount();
    renderWithChakra(<Modal open onClose={onClose} title="T">x</Modal>);
    fireEvent.click(screen.getByTestId("modal-backdrop"));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it("focuses the first field", () => {
    renderWithChakra(<Modal open onClose={vi.fn()} title="T"><input aria-label="f" /></Modal>);
    expect(screen.getByLabelText("f")).toHaveFocus();
  });
});
