import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { PageHeader } from "./PageHeader";

describe("PageHeader", () => {
  it("renders title", () => {
    render(<PageHeader title="Tables" />);
    expect(screen.getByText("Tables")).toBeInTheDocument();
  });

  it("renders description when provided", () => {
    render(<PageHeader title="Tables" description="6 tables defined" />);
    expect(screen.getByText("6 tables defined")).toBeInTheDocument();
  });

  it("does not render description when not provided", () => {
    const { container } = render(<PageHeader title="Tables" />);
    const paragraphs = container.querySelectorAll("p");
    expect(paragraphs.length).toBe(0);
  });

  it("renders actions when provided", () => {
    render(
      <PageHeader
        title="Tables"
        actions={<button>Add Table</button>}
      />
    );
    expect(screen.getByText("Add Table")).toBeInTheDocument();
  });
});
