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
});
