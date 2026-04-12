import { useEffect, useRef, useState } from "react";
import { NavLink, useLocation } from "react-router-dom";
import { Search, ExternalLink, ChevronRight } from "lucide-react";
import { pages, groupBySection } from "../content";
import { Logo } from "./Logo";

const groups = groupBySection(pages);

export function Sidebar() {
  const [query, setQuery] = useState("");
  const location = useLocation();
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "/") return;
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.tagName === "SELECT" ||
          target.isContentEditable)
      ) {
        return;
      }
      e.preventDefault();
      inputRef.current?.focus();
      inputRef.current?.select();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const filtered = query.trim()
    ? pages.filter(
        (p) =>
          p.meta.title.toLowerCase().includes(query.toLowerCase()) ||
          p.meta.description?.toLowerCase().includes(query.toLowerCase())
      )
    : null;

  return (
    <aside className="w-[272px] shrink-0 border-r border-border bg-surface/50 backdrop-blur-sm sticky top-0 h-dvh overflow-y-auto flex flex-col">
      {/* Logo */}
      <div className="px-5 pt-6 pb-4">
        <NavLink to="/" className="flex items-center gap-3 group">
          <Logo size={36} />
          <div>
            <p className="text-sm font-semibold text-foreground tracking-tight">Ultrabase</p>
            <p className="text-[11px] text-muted-foreground font-medium">Documentation</p>
          </div>
        </NavLink>
      </div>

      {/* Search */}
      <div className="px-3 pb-4">
        <div className="relative">
          <Search
            size={14}
            className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground"
          />
          <input
            ref={inputRef}
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search docs..."
            className="w-full pl-9 pr-3 py-2 rounded-lg border border-border bg-background/50 text-sm text-foreground placeholder:text-muted-foreground/60 focus:outline-none focus:border-accent/50 focus:bg-background transition-all"
          />
          {!query && (
            <kbd className="absolute right-3 top-1/2 -translate-y-1/2 text-[10px] text-muted-foreground/50 font-mono border border-border rounded px-1.5 py-0.5">
              /
            </kbd>
          )}
        </div>
      </div>

      {/* Nav */}
      <nav className="flex-1 px-3 pb-6 overflow-y-auto">
        {filtered ? (
          <div>
            <p className="px-2 mb-2 text-[11px] font-medium text-muted-foreground">
              {filtered.length} result{filtered.length !== 1 ? "s" : ""}
            </p>
            <ul className="space-y-0.5">
              {filtered.map((page) => (
                <li key={page.slug}>
                  <NavLink
                    to={page.slug === "" ? "/" : `/${page.slug}`}
                    end={page.slug === ""}
                    onClick={() => setQuery("")}
                    className="flex items-center gap-2 px-2.5 py-2 rounded-lg text-sm text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
                  >
                    <ChevronRight size={12} className="text-accent shrink-0" />
                    <div>
                      <p className="font-medium">{page.meta.title}</p>
                      {page.meta.description && (
                        <p className="text-xs text-muted-foreground/70 mt-0.5 line-clamp-1">{page.meta.description}</p>
                      )}
                    </div>
                  </NavLink>
                </li>
              ))}
            </ul>
          </div>
        ) : (
          groups.map(({ section, pages }) => (
            <div key={section} className="mb-6">
              <p className="px-2.5 mb-2 text-[11px] font-semibold text-muted-foreground/70 uppercase tracking-widest">
                {section}
              </p>
              <ul className="space-y-0.5">
                {pages.map((page) => {
                  const to = page.slug === "" ? "/" : `/${page.slug}`;
                  return (
                    <li key={page.slug}>
                      <NavLink
                        to={to}
                        end={page.slug === ""}
                        className={({ isActive }) =>
                          `group flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-[13px] transition-all cursor-pointer ${
                            isActive
                              ? "bg-accent/10 text-accent font-medium"
                              : "text-muted-foreground hover:text-foreground hover:bg-surface-hover"
                          }`
                        }
                      >
                        {({ isActive }) => (
                          <>
                            <span
                              className={`w-1 h-1 rounded-full shrink-0 transition-all ${
                                isActive
                                  ? "bg-accent shadow-[0_0_6px_var(--color-accent)]"
                                  : "bg-border-bright group-hover:bg-muted-foreground"
                              }`}
                            />
                            {page.meta.title}
                          </>
                        )}
                      </NavLink>
                    </li>
                  );
                })}
              </ul>
            </div>
          ))
        )}
      </nav>

      {/* Footer */}
      <div className="px-3 py-4 border-t border-border mt-auto">
        <a
          href="https://github.com/ultrabase/ultrabase"
          target="_blank"
          rel="noopener noreferrer"
          className="flex items-center gap-2 px-2.5 py-1.5 rounded-lg text-xs text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
        >
          <ExternalLink size={12} />
          GitHub
        </a>
        <p className="px-2.5 mt-2 text-[10px] text-muted-foreground/40 font-mono">v0.1.0</p>
      </div>
    </aside>
  );
}
