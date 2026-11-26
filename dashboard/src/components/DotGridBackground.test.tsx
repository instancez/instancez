import { render } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { DotGridBackground } from "./DotGridBackground";

const mount = () => {
  const host = document.createElement("div");
  document.body.appendChild(host);
  const { container } = render(<DotGridBackground />, { container: host });
  return { host, glow: container.querySelector<HTMLElement>(".dot-grid-glow")! };
};

describe("DotGridBackground", () => {
  beforeEach(() => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q }));
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => { cb(0); return 1; });
  });

  it("starts hidden and offscreen", () => {
    const { glow } = mount();
    expect(glow.style.opacity).toBe("0");
    expect(glow.style.getPropertyValue("--x")).toBe("-1000px");
  });

  it("follows the pointer and resets on leave", () => {
    const { host, glow } = mount();
    host.dispatchEvent(new PointerEvent("pointermove", { clientX: 30, clientY: 40 }));
    expect(glow.style.opacity).toBe("1");
    expect(glow.style.getPropertyValue("--x")).toBe("30px");
    host.dispatchEvent(new PointerEvent("pointerleave"));
    expect(glow.style.opacity).toBe("0");
    expect(glow.style.getPropertyValue("--y")).toBe("-1000px");
  });

  it("skips the glow on touch-only devices", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q === "(hover: none)", media: q }));
    const { host, glow } = mount();
    host.dispatchEvent(new PointerEvent("pointermove", { clientX: 5, clientY: 5 }));
    expect(glow.style.opacity).toBe("0");
  });
});

describe("DotGridBackground colors", () => {
  it("uses the theme variables unless colors are passed", () => {
    const plain = render(<DotGridBackground />).container.querySelector<HTMLElement>(".dot-grid")!;
    expect(plain.style.getPropertyValue("--dot-grid-dot")).toBe("");
    const custom = render(<DotGridBackground dotColor="red" glowColor="blue" />).container.querySelector<HTMLElement>(".dot-grid")!;
    expect(custom.style.getPropertyValue("--dot-grid-dot")).toBe("red");
    expect(custom.style.getPropertyValue("--dot-grid-glow")).toBe("blue");
  });
});
