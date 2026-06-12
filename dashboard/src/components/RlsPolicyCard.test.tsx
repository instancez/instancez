import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { RlsPolicyCard } from "./RlsPolicyCard";

describe("RlsPolicyCard", () => {
  it("frames the check editor as the USING (...) clause of the generated policy", () => {
    render(
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
