import { describe, expect, it, vi } from "vitest";
import { downloadYamlFromConfig } from "./downloadYaml";

describe("downloadYamlFromConfig", () => {
  it("creates an anchor element and clicks it", () => {
    const click = vi.fn();
    const anchor = { click, href: "", download: "" } as unknown as HTMLAnchorElement;
    vi.spyOn(document, "createElement").mockReturnValue(anchor);
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    vi.spyOn(URL, "createObjectURL").mockImplementation(() => "blob:fake");

    downloadYamlFromConfig({ version: 1, project: { name: "x" } } as any, "instancez.yaml");
    expect(click).toHaveBeenCalled();
    expect(anchor.download).toBe("instancez.yaml");
    expect(revoke).toHaveBeenCalled();
  });
});
