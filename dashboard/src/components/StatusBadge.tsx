import { cn } from "../lib/utils";

type Variant = "success" | "error" | "warning" | "info" | "muted";

const VARIANT_STYLES: Record<Variant, string> = {
  success: "border-accent/30 bg-accent/10 text-accent",
  error: "border-destructive/40 bg-destructive/10 text-destructive",
  warning: "border-dashed border-foreground/40 text-warning hazard",
  info: "border-border bg-muted text-info",
  muted: "border-border bg-muted text-muted-foreground",
};

interface StatusBadgeProps {
  variant: Variant;
  children: React.ReactNode;
  className?: string;
  dot?: boolean;
}

export function StatusBadge({
  variant,
  children,
  className,
  dot = false,
}: StatusBadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2 py-1 border font-mono text-[10px] font-medium uppercase tracking-[0.14em]",
        VARIANT_STYLES[variant],
        className
      )}
    >
      {dot && (
        <span
          className={cn(
            "w-1.5 h-1.5 rounded-full",
            variant === "success" && "bg-accent",
            variant === "error" && "bg-destructive",
            variant === "warning" && "bg-warning",
            variant === "info" && "bg-info",
            variant === "muted" && "bg-muted-foreground"
          )}
        />
      )}
      {children}
    </span>
  );
}
