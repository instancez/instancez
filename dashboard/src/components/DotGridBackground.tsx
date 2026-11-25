import { Flex, type FlexProps } from "@chakra-ui/react";
import { useEffect, useRef } from "react";
import type { CSSProperties } from "react";

export interface DotGridBackgroundProps {
  dotColor?: string;
  glowColor?: string;
  glowRadius?: number;
  gap?: number;
  dotSize?: number;
}

const OFFSCREEN = "-1000px";
const fill: CSSProperties = { position: "absolute", inset: 0 };

/** Dot grid that lights up near the cursor. Parent needs `position: relative; isolation: isolate`. */
export function DotGridBackground({ dotColor, glowColor, glowRadius = 60, gap = 12, dotSize = 1 }: DotGridBackgroundProps) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const glowRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const host = wrapRef.current?.parentElement;
    const layer = glowRef.current;
    if (!host || !layer || window.matchMedia?.("(hover: none)").matches) return;

    let frame = 0;
    const move = (e: PointerEvent) => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const rect = host.getBoundingClientRect();
        layer.style.setProperty("--x", `${e.clientX - rect.left}px`);
        layer.style.setProperty("--y", `${e.clientY - rect.top}px`);
        layer.style.opacity = "1";
      });
    };
    const leave = () => {
      cancelAnimationFrame(frame);
      layer.style.opacity = "0";
      layer.style.setProperty("--x", OFFSCREEN);
      layer.style.setProperty("--y", OFFSCREEN);
    };

    host.addEventListener("pointermove", move);
    host.addEventListener("pointerenter", move);
    host.addEventListener("pointerleave", leave);
    return () => {
      cancelAnimationFrame(frame);
      host.removeEventListener("pointermove", move);
      host.removeEventListener("pointerenter", move);
      host.removeEventListener("pointerleave", leave);
    };
  }, []);

  const mask = `radial-gradient(circle, black ${dotSize}px, transparent ${dotSize}px)`;
  const size = `${gap}px ${gap}px`;
  const colors = {
    ...(dotColor && { "--dot-grid-dot": dotColor }),
    ...(glowColor && { "--dot-grid-glow": glowColor }),
  } as CSSProperties;

  return (
    <div ref={wrapRef} className="dot-grid" aria-hidden style={{ ...fill, ...colors, pointerEvents: "none", zIndex: -1 }}>
      <div
        style={{
          ...fill,
          backgroundImage: `radial-gradient(circle, var(--dot-grid-dot) ${dotSize}px, transparent ${dotSize}px)`,
          backgroundSize: size,
        }}
      />
      <div
        ref={glowRef}
        className="dot-grid-glow"
        style={{
          ...fill,
          ["--x" as string]: OFFSCREEN,
          ["--y" as string]: OFFSCREEN,
          background: `radial-gradient(${glowRadius}px at var(--x) var(--y), var(--dot-grid-glow), var(--dot-grid-dot))`,
          maskImage: mask,
          WebkitMaskImage: mask,
          maskSize: size,
          WebkitMaskSize: size,
          opacity: 0,
        }}
      />
    </div>
  );
}

/** A flex container that carries the dot grid; children paint above it. */
export function DotGridSurface({ children, ...props }: FlexProps) {
  return (
    <Flex position="relative" isolation="isolate" bg="bg.muted" {...props}>
      <DotGridBackground />
      {children}
    </Flex>
  );
}
