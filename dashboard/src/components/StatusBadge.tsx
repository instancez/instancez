import { cn } from "../lib/utils";

type Variant = "success" | "error" | "warning" | "info" | "muted";

const VARIANT_STYLES: Record<Variant, string> = {
  success: "bg-accent/15 text-accent",
  error: "bg-destructive/15 text-destructive",
  warning: "bg-warning/15 text-warning",
  info: "bg-info/15 text-info",
  muted: "bg-muted text-muted-foreground",
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
        "inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium",
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
