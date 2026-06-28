import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { act } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { CodeEditor } from "./CodeEditor";

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
