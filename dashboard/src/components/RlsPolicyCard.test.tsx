import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { RlsPolicyCard } from "./RlsPolicyCard";

describe("RlsPolicyCard", () => {
  it("frames the check editor as the USING (...) clause of the generated policy", () => {
    renderWithChakra(
      <RlsPolicyCard
        policy={{ operations: ["select"], check: "", type: "permissive" }}
        onChange={vi.fn()}
        onDelete={vi.fn()}
      />
    );
    expect(screen.getByText(/CREATE POLICY .* USING \(/)).toBeInTheDocument();
    expect(screen.getByText(")")).toBeInTheDocument();
  });

  it("renders the clause as fixed, non-editable text framing the check expression", () => {
    const { container } = renderWithChakra(
      <RlsPolicyCard
        policy={{ operations: ["select"], check: "user_id = auth.uid()", type: "permissive" }}
        onChange={vi.fn()}
        onDelete={vi.fn()}
      />
    );
    // The USING (…) clause shows inside the editor but is locked: it renders as
    // non-editable widgets, not as editable document lines.
    const scaffolds = [...container.querySelectorAll(".cm-scaffold")];
    expect(scaffolds).toHaveLength(2);
    scaffolds.forEach((el) =>
      expect(el.getAttribute("contenteditable")).toBe("false")
    );
    const lines = [...container.querySelectorAll(".cm-line")]
      .map((l) => l.textContent)
      .join("\n");
    expect(lines).toBe("user_id = auth.uid()");
  });
});
