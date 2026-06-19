import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { ListSkeleton } from "./Skeletons";

describe("ListSkeleton", () => {
  it("renders the requested number of skeleton rows", () => {
    renderWithChakra(<ListSkeleton rows={3} />);
    const root = screen.getByTestId("list-skeleton");
    expect(root.children.length).toBe(3);
  });

  it("defaults to 5 rows", () => {
    renderWithChakra(<ListSkeleton />);
    expect(screen.getByTestId("list-skeleton").children.length).toBe(5);
  });
});
