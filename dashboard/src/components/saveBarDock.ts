import { createContext, useContext } from "react";

// The Layout mounts an empty slot at the bottom of the content card, below the
// scroll region, and publishes its DOM node here. SaveBar portals into that
// node so the bar sits in its own row at the card's bottom edge and the content
// shrinks above it, rather than floating over the page. The value is null
// outside the console shell (for example in unit tests), where SaveBar falls
// back to rendering inline.
export const SaveBarDockContext = createContext<HTMLElement | null>(null);

export function useSaveBarDock(): HTMLElement | null {
  return useContext(SaveBarDockContext);
}
