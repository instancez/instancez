import { describe, it, expect, vi } from "vitest";
import { renderWithChakra } from "../test/helpers";
import { RlsPolicyCard } from "./RlsPolicyCard";

describe("RlsPolicyCard", () => {
  it("shows only the using editor for a select-only policy", () => {
    const { container } = renderWithChakra(
      <RlsPolicyCard
        policy={{ operations: ["select"], using: "", type: "permissive" }}
        onChange={vi.fn()}
        onDelete={vi.fn()}
      />
    );
    const lines = [...container.querySelectorAll(".cm-line")]
      .map((l) => l.textContent)
      .join("\n");
    expect(lines).toBe("CREATE POLICY … FOR select USING (\n\n)");
  });

  it("shows only the with_check editor for an insert-only policy", () => {
    const { container } = renderWithChakra(
      <RlsPolicyCard
        policy={{ operations: ["insert"], with_check: "auth.is_authenticated()", type: "permissive" }}
        onChange={vi.fn()}
        onDelete={vi.fn()}
      />
    );
    const lines = [...container.querySelectorAll(".cm-line")]
      .map((l) => l.textContent)
      .join("\n");
    expect(lines).toBe("CREATE POLICY … FOR insert WITH CHECK (\nauth.is_authenticated()\n)");
  });

  it("shows both editors for an update policy with divergent expressions", () => {
    const { container } = renderWithChakra(
      <RlsPolicyCard
        policy={{
          operations: ["update"],
          using: "owner_id = auth.uid()",
          with_check: "owner_id = auth.uid() AND status != 'locked'",
          type: "permissive",
        }}
        onChange={vi.fn()}
        onDelete={vi.fn()}
      />
    );
    const lines = [...container.querySelectorAll(".cm-line")]
      .map((l) => l.textContent)
      .join("\n");
    expect(lines).toBe(
      "CREATE POLICY … FOR update USING (\nowner_id = auth.uid()\n)\n" +
        "CREATE POLICY … FOR update WITH CHECK (\nowner_id = auth.uid() AND status != 'locked'\n)"
    );
  });
});
