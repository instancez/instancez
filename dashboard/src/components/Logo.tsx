import logoUrl from "../assets/instancez-logo-only.svg";
import { cn } from "../lib/utils";

interface LogoProps {
  size?: number;
  className?: string;
}

export function Logo({ size = 36, className }: LogoProps) {
  return (
    <img
      src={logoUrl}
      width={size}
      height={size}
      alt="instancez"
      className={cn("dark:invert", className)}
    />
  );
}

/** Brand lockup used in the navbar and on the login card. */
export function Wordmark({ badge = "Dashboard" }: { badge?: string }) {
  return (
    <span className="inline-flex items-center gap-2">
      <Logo size={26} />
      <span className="text-xl font-bold text-foreground">instancez</span>
      <span className="relative top-px ml-1 px-2 py-0.5 rounded-md text-[10px] font-bold uppercase tracking-[0.05em] bg-accent text-background border border-border shadow-card">
        {badge}
      </span>
    </span>
  );
}
