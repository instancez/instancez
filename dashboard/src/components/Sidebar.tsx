import { NavLink } from "react-router-dom";
import {
  LayoutDashboard,
  Table2,
  Shield,
  HardDrive,
  Code2,
  Database,
  Plug,
} from "lucide-react";

const NAV_ITEMS = [
  { to: "/", icon: LayoutDashboard, label: "Overview" },
  { to: "/tables", icon: Table2, label: "Tables" },
  { to: "/auth", icon: Shield, label: "Auth" },
  { to: "/storage", icon: HardDrive, label: "Storage" },
  { to: "/rpc", icon: Database, label: "Database Functions" },
  { to: "/functions", icon: Code2, label: "Code Functions" },
  { to: "/providers", icon: Plug, label: "Providers" },
] as const;

export function Sidebar() {
  return (
    <aside className="w-60 shrink-0 flex flex-col px-3 py-4 bg-surface border border-border rounded-xl shadow-card">
      <nav className="flex-1 overflow-y-auto">
        <ul className="space-y-1">
          {NAV_ITEMS.map(({ to, icon: Icon, label }) => (
            <li key={to}>
              <NavLink
                to={to}
                end={to === "/"}
                className={({ isActive }) =>
                  `group flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm font-medium transition-colors cursor-pointer ${
                    isActive
                      ? "bg-accent text-background shadow-card"
                      : "text-muted-foreground hover:text-foreground hover:bg-surface-hover"
                  }`
                }
              >
                {({ isActive }) => (
                  <>
                    <Icon
                      size={16}
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

      <p className="px-3 pt-4 text-[11px] text-muted-foreground/60 font-mono">
        v0.1.0
      </p>
    </aside>
  );
}
