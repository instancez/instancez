import { afterEach, describe, expect, it, vi } from "vitest";
import { copyText } from "./copyText";

afterEach(() => { vi.unstubAllGlobals(); });

describe("copyText", () => {
  it("writes to the clipboard and returns true", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    expect(await copyText("hi")).toBe(true);
    expect(writeText).toHaveBeenCalledWith("hi");
  });

  it("returns false when the write is rejected", async () => {
    vi.stubGlobal("navigator", { clipboard: { writeText: vi.fn().mockRejectedValue(new Error("denied")) } });
    expect(await copyText("hi")).toBe(false);
  });

  it("returns false when the clipboard API is missing", async () => {
    vi.stubGlobal("navigator", {});
    expect(await copyText("hi")).toBe(false);
  });
});
