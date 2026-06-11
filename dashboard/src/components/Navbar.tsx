import { Moon, Sun, Github } from "lucide-react";
import { Wordmark } from "./Logo";
import { useColorMode } from "../hooks/useColorMode";

/**
 * Floating pill navbar, matching the coder app's shell: a bordered
 * rounded-xl surface inset from the viewport edges with the brand lockup
 * on the left and quick actions on the right.
 */
export function Navbar() {
  const { colorMode, toggleColorMode } = useColorMode();

  return (
    <header className="shrink-0 m-2 px-4 py-2 flex items-center justify-between bg-surface border border-border rounded-xl shadow-card">
      <Wordmark />
      <div className="flex items-center gap-1">
        <a
          href="https://github.com/instancez/instancez"
          target="_blank"
          rel="noopener noreferrer"
          aria-label="GitHub repository"
          className="p-2 rounded-full text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors"
        >
          <Github size={16} />
        </a>
        <button
          type="button"
          onClick={toggleColorMode}
          aria-label="Toggle dark mode"
          className="p-2 rounded-full text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
        >
          {colorMode === "dark" ? <Sun size={16} /> : <Moon size={16} />}
        </button>
      </div>
    </header>
  );
}
