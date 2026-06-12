import { describe, expect, it, vi } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { SaveToast, showSaveToast } from "./SaveToast";

describe("SaveToast", () => {
  it("renders when triggered and disappears after the timeout", () => {
    vi.useFakeTimers();
    render(<SaveToast />);
    act(() => {
      showSaveToast({ source: "s3://bucket/key" });
    });
    expect(screen.getByText(/Saved to/)).toBeTruthy();
    expect(screen.getByText(/s3:\/\/bucket\/key/)).toBeTruthy();
    expect(screen.queryByText(/statement/)).toBeNull();
    expect(screen.getByText(/update your git source/i)).toBeTruthy();

    act(() => { vi.advanceTimersByTime(8000); });
    expect(screen.queryByText(/Saved to/)).toBeNull();
    vi.useRealTimers();
  });

  it("does not render when never triggered", () => {
    const { container } = render(<SaveToast />);
    expect(container.firstChild).toBeNull();
  });
});
