import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { act } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { CodeEditor } from "./CodeEditor";

/**
 * The `frame` prop renders a fixed scaffold (header/footer) inside the editor.
 * The scaffold reads as part of the code block but is not part of the
 * editable document, so it can't be typed into or accidentally edited.
 */
describe("CodeEditor frame", () => {
  it("renders the header and footer scaffold inside the editor", () => {
    const { container } = renderWithChakra(
      <CodeEditor
        value="user_id = auth.uid()"
        onChange={vi.fn()}
        frame={{ header: "CREATE POLICY x USING (", footer: ")" }}
      />
    );
    expect(container.textContent).toContain("CREATE POLICY x USING (");
    expect(container.textContent).toContain(")");
  });

  it("keeps the scaffold out of the editable document", () => {
    const { container } = renderWithChakra(
      <CodeEditor
        value="user_id = auth.uid()"
        onChange={vi.fn()}
        frame={{ header: "CREATE POLICY x USING (", footer: ")" }}
      />
    );
    // The document is the editable lines, which the caret edits and which feed
    // value/onChange. The scaffold is a non-editable widget, so the lines hold
    // only the body and never the surrounding statement.
    const lines = [...container.querySelectorAll(".cm-line")]
      .map((l) => l.textContent)
      .join("\n");
    expect(lines).toBe("user_id = auth.uid()");
    container.querySelectorAll(".cm-scaffold").forEach((el) => {
      expect(el.getAttribute("contenteditable")).toBe("false");
    });
  });

  it("updates the scaffold when the frame prop changes without losing the body", async () => {
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
    expect(container.textContent).toContain("FUNCTION foo(arg int) AS $ub$");
    const lines = [...container.querySelectorAll(".cm-line")]
      .map((l) => l.textContent)
      .join("\n");
    expect(lines).toBe("return 1;");
  });
});
