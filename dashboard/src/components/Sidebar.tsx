import { NavLink } from "react-router-dom";
import {
  LayoutDashboard,
  Table2,
  Shield,
  HardDrive,
  Code2,
  Database,
  Plug,
  ExternalLink,
} from "lucide-react";
import { Logo } from "./Logo";

const NAV_ITEMS = [
  { to: "/", icon: LayoutDashboard, label: "Overview" },
  { to: "/tables", icon: Table2, label: "Tables" },
  { to: "/auth", icon: Shield, label: "Auth" },
  { to: "/storage", icon: HardDrive, label: "Storage" },
  { to: "/rpc", icon: Database, label: "Database Functions" },
  { to: "/functions", icon: Code2, label: "Edge Functions" },
  { to: "/providers", icon: Plug, label: "Providers" },
] as const;

export function Sidebar() {
  return (
    <aside className="w-[272px] shrink-0 h-full bg-surface/50 backdrop-blur-sm border-r border-border flex flex-col">
      {/* Logo */}
      <div className="px-5 pt-6 pb-4">
        <div className="flex items-center gap-3">
          <Logo size={36} />
          <div>
            <p className="text-sm font-semibold text-foreground tracking-tight">instancez</p>
            <p className="text-[11px] text-muted-foreground font-medium">Dashboard</p>
          </div>
        </div>
      </div>

      {/* Navigation */}
      <nav className="flex-1 px-3 pb-6 overflow-y-auto">
        <p className="px-2.5 mb-2 text-[11px] font-semibold text-muted-foreground/70 uppercase tracking-widest">
          Navigation
        </p>
        <ul className="space-y-0.5">
          {NAV_ITEMS.map(({ to, icon: Icon, label }) => (
            <li key={to}>
              <NavLink
                to={to}
                end={to === "/"}
                className={({ isActive }) =>
                  `group flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-[13px] transition-all cursor-pointer ${
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
                          : "bg-border-hover group-hover:bg-muted-foreground"
                      }`}
                    />
                    <Icon
                      size={15}
                      strokeWidth={isActive ? 2 : 1.6}
                      className="shrink-0"
                    />
                    {label}
                  </>
                )}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>

      {/* Footer */}
      <div className="px-3 py-4 border-t border-border mt-auto">
        <a
          href="https://github.com/instancez/instancez"
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
