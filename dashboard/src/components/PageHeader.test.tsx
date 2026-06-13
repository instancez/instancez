import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { PageHeader } from "./PageHeader";

describe("PageHeader", () => {
  it("renders title", () => {
    renderWithChakra(<PageHeader title="Tables" />);
    expect(screen.getByText("Tables")).toBeInTheDocument();
  });

  it("renders description when provided", () => {
    renderWithChakra(<PageHeader title="Tables" description="6 tables defined" />);
    expect(screen.getByText("6 tables defined")).toBeInTheDocument();
  });

  it("does not render description when not provided", () => {
    renderWithChakra(<PageHeader title="Tables" />);
    // Only the title renders; no secondary description text
    expect(screen.queryByText(/tables defined|description/i)).toBeNull();
  });

  it("renders actions when provided", () => {
    renderWithChakra(
      <PageHeader
        title="Tables"
        actions={<button>Add Table</button>}
      />
    );
    expect(screen.getByText("Add Table")).toBeInTheDocument();
  });
});
