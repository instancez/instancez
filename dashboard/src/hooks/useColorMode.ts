import { useCallback, useEffect, useState } from "react";

const STORAGE_KEY = "instancez_color_mode";

export type ColorMode = "light" | "dark";

function currentMode(): ColorMode {
  return document.documentElement.classList.contains("dark")
    ? "dark"
    : "light";
}

/**
 * useColorMode mirrors the coder app's light/dark switch. The mode lives on
 * <html class="dark"> (applied before first paint by an inline script in
 * index.html) and is persisted to localStorage.
 */
export function useColorMode() {
  const [mode, setMode] = useState<ColorMode>(currentMode);

  useEffect(() => {
    const observer = new MutationObserver(() => setMode(currentMode()));
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    return () => observer.disconnect();
  }, []);

  const toggleColorMode = useCallback(() => {
    const next: ColorMode = currentMode() === "dark" ? "light" : "dark";
    document.documentElement.classList.toggle("dark", next === "dark");
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // Private browsing — the mode just won't persist.
    }
    setMode(next);
  }, []);

  return { colorMode: mode, toggleColorMode };
}
