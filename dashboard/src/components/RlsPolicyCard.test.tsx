import { describe, it, expect, vi } from "vitest";
import { renderWithChakra } from "../test/helpers";
import { RlsPolicyCard } from "./RlsPolicyCard";

describe("RlsPolicyCard", () => {
  it("frames the check editor as the USING (...) clause of the generated policy", () => {
    const { container } = renderWithChakra(
      <RlsPolicyCard
        policy={{ operations: ["select"], check: "", type: "permissive" }}
        onChange={vi.fn()}
        onDelete={vi.fn()}
      />
    );
    const lines = [...container.querySelectorAll(".cm-line")]
      .map((l) => l.textContent)
      .join("\n");
    expect(lines).toBe("CREATE POLICY … FOR select USING (\n\n)");
  });

  it("renders the clause as fixed, non-editable text framing the check expression", () => {
    const { container } = renderWithChakra(
      <RlsPolicyCard
        policy={{ operations: ["select"], check: "user_id = auth.uid()", type: "permissive" }}
        onChange={vi.fn()}
        onDelete={vi.fn()}
      />
    );
    // The USING (…) clause shows inside the editor as real, syntax-highlighted
    // lines around the check expression, but those lines are locked: tinted
    // read-only and fenced off from the caret (the lock itself is covered in
    // CodeEditor.scaffold.test.ts).
    const lines = [...container.querySelectorAll(".cm-line")];
    expect(lines.map((l) => l.textContent).join("\n")).toBe(
      "CREATE POLICY … FOR select USING (\nuser_id = auth.uid()\n)"
    );
    expect(lines.map((l) => l.classList.contains("cm-readonly-line"))).toEqual([
      true,
      false,
      true,
    ]);
  });
});
