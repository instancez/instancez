import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { Table2 } from "lucide-react";
import { renderWithChakra } from "../test/helpers";
import { EmptyState } from "./EmptyState";

describe("EmptyState", () => {
  it("renders title and description", () => {
    renderWithChakra(
      <EmptyState
        icon={Table2}
        title="No tables"
        description="Create a table to get started."
      />
    );
    expect(screen.getByText("No tables")).toBeInTheDocument();
    expect(screen.getByText("Create a table to get started.")).toBeInTheDocument();
  });

  it("renders action when provided", () => {
    renderWithChakra(
      <EmptyState
        icon={Table2}
        title="Empty"
        description="Nothing here."
        action={<button>Add Item</button>}
      />
    );
    expect(screen.getByText("Add Item")).toBeInTheDocument();
  });

  it("does not render action container when no action", () => {
    const { container } = renderWithChakra(
      <EmptyState icon={Table2} title="Empty" description="Nothing." />
    );
    expect(container.querySelector("button")).toBeNull();
  });
});
