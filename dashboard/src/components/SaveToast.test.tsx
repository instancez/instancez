import { describe, expect, it, vi } from "vitest";
import { screen, act } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { SaveToast, showSaveToast } from "./SaveToast";

describe("SaveToast", () => {
  it("renders when triggered and disappears after the timeout", () => {
    vi.useFakeTimers();
    renderWithChakra(<SaveToast />);
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
    renderWithChakra(<SaveToast />);
    expect(screen.queryByRole("status")).toBeNull();
  });
});
