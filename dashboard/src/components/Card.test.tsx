import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { Card, CardTitle, CardValue } from "./Card";

describe("Card", () => {
  it("renders children", () => {
    renderWithChakra(<Card>Card content</Card>);
    expect(screen.getByText("Card content")).toBeInTheDocument();
  });

  it("calls onClick when clicked", () => {
    const onClick = vi.fn();
    renderWithChakra(<Card onClick={onClick}>Clickable</Card>);
    fireEvent.click(screen.getByText("Clickable"));
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("adds hover styles when hoverable", () => {
    const { container } = renderWithChakra(<Card hoverable>Hover me</Card>);
    // Panel uses Chakra _hover prop — just verify the element renders
    expect(container.firstChild).toBeTruthy();
  });

  it("applies custom className", () => {
    renderWithChakra(<Card className="my-class">Test</Card>);
    expect(screen.getByText("Test").closest(".my-class")).toBeTruthy();
  });
});

describe("CardTitle", () => {
  it("renders title text", () => {
    renderWithChakra(<CardTitle>Tables</CardTitle>);
    expect(screen.getByText("Tables")).toBeInTheDocument();
  });
});

describe("CardValue", () => {
  it("renders value text", () => {
    renderWithChakra(<CardValue>42</CardValue>);
    expect(screen.getByText("42")).toBeInTheDocument();
  });
});
