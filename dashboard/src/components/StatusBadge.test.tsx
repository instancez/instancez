import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { StatusBadge } from "./StatusBadge";

describe("StatusBadge", () => {
  it("renders children text", () => {
    render(<StatusBadge variant="success">Active</StatusBadge>);
    expect(screen.getByText("Active")).toBeInTheDocument();
  });

  it("applies success variant styles", () => {
    render(<StatusBadge variant="success">OK</StatusBadge>);
    const badge = screen.getByText("OK");
    expect(badge.className).toContain("text-accent");
  });

  it("applies error variant styles", () => {
    render(<StatusBadge variant="error">Failed</StatusBadge>);
    const badge = screen.getByText("Failed");
    expect(badge.className).toContain("text-destructive");
  });

  it("renders dot indicator when dot prop is true", () => {
    const { container } = render(
      <StatusBadge variant="success" dot>
        Online
      </StatusBadge>
    );
    const dots = container.querySelectorAll(".rounded-full");
    // The dot is a small span with rounded-full
    expect(dots.length).toBeGreaterThan(0);
  });

  it("does not render dot by default", () => {
    const { container } = render(
      <StatusBadge variant="success">Online</StatusBadge>
    );
    const badge = screen.getByText("Online");
    // Should only have the badge itself, no dot child
    const innerSpans = badge.querySelectorAll("span");
    expect(innerSpans.length).toBe(0);
  });

  it("applies custom className", () => {
    render(
      <StatusBadge variant="info" className="custom-class">
        Info
      </StatusBadge>
    );
    expect(screen.getByText("Info").className).toContain("custom-class");
  });

  it("renders all variant types without error", () => {
    const variants = ["success", "error", "warning", "info", "muted"] as const;
    for (const variant of variants) {
      const { unmount } = render(
        <StatusBadge variant={variant}>{variant}</StatusBadge>
      );
      expect(screen.getByText(variant)).toBeInTheDocument();
      unmount();
    }
  });
});
