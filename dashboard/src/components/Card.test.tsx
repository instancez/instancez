import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Card, CardTitle, CardValue } from "./Card";

describe("Card", () => {
  it("renders children", () => {
    render(<Card>Card content</Card>);
    expect(screen.getByText("Card content")).toBeInTheDocument();
  });

  it("calls onClick when clicked", () => {
    const onClick = vi.fn();
    render(<Card onClick={onClick}>Clickable</Card>);
    fireEvent.click(screen.getByText("Clickable"));
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("adds hover styles when hoverable", () => {
    const { container } = render(<Card hoverable>Hover me</Card>);
    expect(container.firstChild).toHaveClass("hover:bg-surface-hover");
  });

  it("applies custom className", () => {
    const { container } = render(<Card className="my-class">Test</Card>);
    expect(container.firstChild).toHaveClass("my-class");
  });
});

describe("CardTitle", () => {
  it("renders title text", () => {
    render(<CardTitle>Tables</CardTitle>);
    expect(screen.getByText("Tables")).toBeInTheDocument();
  });
});

describe("CardValue", () => {
  it("renders value text", () => {
    render(<CardValue>42</CardValue>);
    expect(screen.getByText("42")).toBeInTheDocument();
  });
});
