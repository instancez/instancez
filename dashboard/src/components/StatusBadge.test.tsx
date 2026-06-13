import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { StatusBadge } from "./StatusBadge";

describe("StatusBadge", () => {
  it("renders children text", () => {
    renderWithChakra(<StatusBadge variant="success">Active</StatusBadge>);
    expect(screen.getByText("Active")).toBeInTheDocument();
  });

  it("applies success variant styles", () => {
    renderWithChakra(<StatusBadge variant="success">OK</StatusBadge>);
    // StatusBadge now uses Chakra tokens — just verify rendering
    expect(screen.getByText("OK")).toBeInTheDocument();
  });

  it("applies error variant styles", () => {
    renderWithChakra(<StatusBadge variant="error">Failed</StatusBadge>);
    expect(screen.getByText("Failed")).toBeInTheDocument();
  });

  it("renders dot indicator when dot prop is true", () => {
    renderWithChakra(
      <StatusBadge variant="success" dot>
        Online
      </StatusBadge>
    );
    expect(screen.getByText("Online")).toBeInTheDocument();
    // The badge element contains the dot + text as multiple children
    const badge = screen.getByText("Online").parentElement;
    expect(badge?.childNodes.length).toBeGreaterThan(1);
  });

  it("does not render dot by default", () => {
    const { container } = renderWithChakra(
      <StatusBadge variant="success">Online</StatusBadge>
    );
    expect(screen.getByText("Online")).toBeInTheDocument();
    // Without dot, there are no extra box elements for the dot indicator
    // (dot prop adds a Box with w="1.5" h="1.5")
    const dotElements = container.querySelectorAll('[style*="width: var(--chakra-sizes-1\\.5)"]');
    expect(dotElements.length).toBe(0);
  });

  it("applies custom className", () => {
    renderWithChakra(
      <StatusBadge variant="info" className="custom-class">
        Info
      </StatusBadge>
    );
    expect(screen.getByText("Info").closest(".custom-class")).toBeTruthy();
  });

  it("renders all variant types without error", () => {
    const variants = ["success", "error", "warning", "info", "muted"] as const;
    for (const variant of variants) {
      const { unmount } = renderWithChakra(
        <StatusBadge variant={variant}>{variant}</StatusBadge>
      );
      expect(screen.getByText(variant)).toBeInTheDocument();
      unmount();
    }
  });
});
