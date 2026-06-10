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
    <aside className="w-[272px] shrink-0 h-full bg-background border-r border-border flex flex-col">
      {/* Logo */}
      <div className="px-5 pt-6 pb-5 border-b border-border">
        <div className="flex items-center gap-3">
          <Logo size={34} />
          <p className="text-sm font-semibold text-foreground tracking-tight">
            instancez
          </p>
        </div>
      </div>

      {/* Navigation */}
      <nav className="flex-1 px-3 py-5 overflow-y-auto">
        <ul className="space-y-px">
          {NAV_ITEMS.map(({ to, icon: Icon, label }) => (
            <li key={to}>
              <NavLink
                to={to}
                end={to === "/"}
                className={({ isActive }) =>
                  `group flex items-center gap-2.5 px-2.5 py-2 text-[13px] transition-colors cursor-pointer ${
                    isActive
                      ? "bg-foreground text-background font-medium"
                      : "text-muted-foreground hover:text-foreground hover:bg-surface-hover"
                  }`
                }
              >
                {({ isActive }) => (
                  <>
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
          className="flex items-center gap-2 px-2.5 py-1.5 text-xs text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors cursor-pointer"
        >
          <ExternalLink size={12} />
          GitHub
        </a>
        <p className="px-2.5 mt-2 text-[10px] text-muted-foreground/40 font-mono tracking-widest">
          v0.1.0
        </p>
      </div>
    </aside>
  );
}
