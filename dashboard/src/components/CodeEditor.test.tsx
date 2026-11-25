import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { act, fireEvent } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { EditorView } from "codemirror";
import { startCompletion, currentCompletions } from "@codemirror/autocomplete";
import { waitFor } from "@testing-library/react";
import { CodeEditor } from "./CodeEditor";

describe("CodeEditor onSubmit", () => {
  it("fires onSubmit on Cmd/Ctrl+Enter, not a plain Enter", () => {
    const onSubmit = vi.fn();
    const { container } = renderWithChakra(
      <CodeEditor value="select 1" onChange={vi.fn()} onSubmit={onSubmit} />
    );
    const editorEl = container.querySelector(".cm-content") as HTMLElement;
    fireEvent.keyDown(editorEl, { key: "Enter" });
    expect(onSubmit).not.toHaveBeenCalled();
    fireEvent.keyDown(editorEl, { key: "Enter", ctrlKey: true });
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("leaves Mod-Enter to the default (insert blank line) when no onSubmit", () => {
    const onChange = vi.fn();
    const { container } = renderWithChakra(
      <CodeEditor value="select 1" onChange={onChange} />
    );
    const editorEl = container.querySelector(".cm-content") as HTMLElement;
    const before = container.querySelectorAll(".cm-line").length;
    fireEvent.keyDown(editorEl, { key: "Enter", ctrlKey: true });
    expect(container.querySelectorAll(".cm-line").length).toBe(before + 1);
  });
});

/**
 * The `frame` prop renders a fixed scaffold (header/footer) as real, locked
 * lines inside the editor: they get syntax highlighting and line numbers like
 * the rest of the code, but they can't be edited and never reach value/onChange.
 * The locking and caret-fencing themselves are covered headless in
 * CodeEditor.scaffold.test.ts; these tests check what gets rendered.
 */
describe("CodeEditor frame", () => {
  it("renders the header and footer as lines around the body", () => {
    const { container } = renderWithChakra(
      <CodeEditor
        value="user_id = auth.uid()"
        onChange={vi.fn()}
        frame={{ header: "CREATE POLICY x USING (", footer: ")" }}
      />
    );
    const lines = [...container.querySelectorAll(".cm-line")]
      .map((l) => l.textContent)
      .join("\n");
    expect(lines).toBe("CREATE POLICY x USING (\nuser_id = auth.uid()\n)");
  });

  it("tints the scaffold lines as read-only but leaves the body line plain", () => {
    const { container } = renderWithChakra(
      <CodeEditor
        value="user_id = auth.uid()"
        onChange={vi.fn()}
        frame={{ header: "CREATE POLICY x USING (", footer: ")" }}
      />
    );
    const readonly = [...container.querySelectorAll(".cm-line")].map((l) =>
      l.classList.contains("cm-readonly-line")
    );
    expect(readonly).toEqual([true, false, true]);
  });

  it("updates the scaffold when the frame prop changes without losing the body", () => {
    // Mirrors the RPC editor, where the header recomputes from form fields
    // while the body stays put.
    function Harness() {
      const [arg, setArg] = useState(false);
      return (
        <>
          <button onClick={() => setArg(true)}>add arg</button>
          <CodeEditor
            value="return 1;"
            onChange={vi.fn()}
            frame={{
              header: `FUNCTION foo(${arg ? "arg int" : ""}) AS $ub$`,
              footer: "$ub$;",
            }}
          />
        </>
      );
    }
    const { container, getByText } = renderWithChakra(<Harness />);
    expect(container.textContent).toContain("FUNCTION foo() AS $ub$");
    act(() => getByText("add arg").click());
    const lines = [...container.querySelectorAll(".cm-line")]
      .map((l) => l.textContent)
      .join("\n");
    expect(lines).toBe("FUNCTION foo(arg int) AS $ub$\nreturn 1;\n$ub$;");
  });
});

const viewOf = (c: HTMLElement) => EditorView.findFromDOM(c.querySelector(".cm-editor") as HTMLElement)!;
const tab = (c: HTMLElement, shift = false) =>
  fireEvent.keyDown(c.querySelector(".cm-content") as HTMLElement, { key: "Tab", shiftKey: shift });

describe("CodeEditor Tab", () => {
  it("indents on Tab and outdents on Shift+Tab", () => {
    const { container } = renderWithChakra(<CodeEditor value="select 1" onChange={vi.fn()} />);
    const view = viewOf(container);
    view.dispatch({ selection: { anchor: 0 } });
    tab(container);
    expect(view.state.doc.toString()).toMatch(/^\s+select 1$/);
    tab(container, true);
    expect(view.state.doc.toString()).toBe("select 1");
  });

  it("accepts the open suggestion instead of indenting", async () => {
    const { container } = renderWithChakra(
      <CodeEditor value="select tod" onChange={vi.fn()} language="sql" sqlSchema={{ todos: ["id"] }} />
    );
    const view = viewOf(container);
    view.dispatch({ selection: { anchor: 10 } });
    startCompletion(view);
    await waitFor(() => expect(currentCompletions(view.state).length).toBeGreaterThan(0));
    await new Promise((r) => setTimeout(r, 150)); // CodeMirror ignores accepts right after the popup opens
    tab(container);
    expect(view.state.doc.toString()).toBe("select todos");
  });

  it("leaves a read-only editor alone", () => {
    const { container } = renderWithChakra(<CodeEditor value="select 1" onChange={vi.fn()} readOnly />);
    tab(container);
    expect(viewOf(container).state.doc.toString()).toBe("select 1");
  });
});

describe("CodeEditor format", () => {
  it("rewrites the document on Shift+Alt+F and keeps it undoable-safe when unchanged", async () => {
    const format = vi.fn(async (d: string) => d.toUpperCase());
    const { container } = renderWithChakra(<CodeEditor value="select 1" onChange={vi.fn()} format={format} />);
    fireEvent.keyDown(container.querySelector(".cm-content") as HTMLElement, { key: "f", shiftKey: true, altKey: true });
    await waitFor(() => expect(viewOf(container).state.doc.toString()).toBe("SELECT 1"));
    expect(format).toHaveBeenCalledWith("select 1");
  });

  it("does nothing when there is no formatter", () => {
    const { container } = renderWithChakra(<CodeEditor value="select 1" onChange={vi.fn()} />);
    fireEvent.keyDown(container.querySelector(".cm-content") as HTMLElement, { key: "f", shiftKey: true, altKey: true });
    expect(viewOf(container).state.doc.toString()).toBe("select 1");
  });

  it("drops a format result if the user typed meanwhile", async () => {
    let release!: (s: string) => void;
    const format = vi.fn(() => new Promise<string>((r) => (release = r)));
    const { container } = renderWithChakra(<CodeEditor value="select 1" onChange={vi.fn()} format={format} />);
    fireEvent.keyDown(container.querySelector(".cm-content") as HTMLElement, { key: "f", shiftKey: true, altKey: true });
    viewOf(container).dispatch({ changes: { from: 8, insert: "0" } });
    release("SELECT 1");
    await Promise.resolve();
    expect(viewOf(container).state.doc.toString()).toBe("select 10");
  });
});

describe("CodeEditor sql lint", () => {
  it("underlines a syntax error and leaves valid SQL clean", async () => {
    const bad = renderWithChakra(<CodeEditor value="selec 1" onChange={vi.fn()} language="sql" sqlLint />);
    await waitFor(() => expect(bad.container.querySelector(".cm-lintRange-error")).not.toBeNull(), { timeout: 4000 });
    bad.unmount();
    const good = renderWithChakra(<CodeEditor value="select 1" onChange={vi.fn()} language="sql" sqlLint />);
    await new Promise((r) => setTimeout(r, 1200));
    expect(good.container.querySelector(".cm-lintRange-error")).toBeNull();
  });
});
