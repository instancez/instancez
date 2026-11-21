import { describe, it, expect, vi, afterEach } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { CopyButton } from "./CopyButton";

afterEach(() => vi.restoreAllMocks());

function stubClipboard(writeText: ReturnType<typeof vi.fn>) {
  Object.assign(navigator, { clipboard: { writeText } });
}

describe("CopyButton", () => {
  it("copies the value and swaps to a check for 1.5s", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    stubClipboard(writeText);
    renderWithChakra(<CopyButton value="abc" label="URL" />);
    const btn = screen.getByLabelText("Copy URL");
    expect(btn.querySelector(".lucide-copy")).not.toBeNull();
    fireEvent.click(btn);
    expect(writeText).toHaveBeenCalledWith("abc");
    await waitFor(() => expect(btn.querySelector(".lucide-check")).not.toBeNull());
    await waitFor(() => expect(btn.querySelector(".lucide-copy")).not.toBeNull(), { timeout: 3000 });
  });

  it("stays idle when the clipboard rejects", async () => {
    const writeText = vi.fn().mockRejectedValue(new Error("denied"));
    stubClipboard(writeText);
    renderWithChakra(<CopyButton value="abc" label="URL" />);
    const btn = screen.getByLabelText("Copy URL");
    fireEvent.click(btn);
    await waitFor(() => expect(writeText).toHaveBeenCalled());
    expect(btn.querySelector(".lucide-check")).toBeNull();
  });

  it("copies an empty value without throwing", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    stubClipboard(writeText);
    renderWithChakra(<CopyButton value="" label="URL" />);
    fireEvent.click(screen.getByLabelText("Copy URL"));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(""));
  });
});
