import { cn } from "../lib/utils";

type Variant = "success" | "error" | "warning" | "info" | "muted";

const VARIANT_STYLES: Record<Variant, string> = {
  success: "border-success/25 bg-success/10 text-success",
  error: "border-destructive/25 bg-destructive/10 text-destructive",
  warning: "border-warning/25 bg-warning/10 text-warning",
  info: "border-info/25 bg-info/10 text-info",
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
        "inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md border text-xs font-medium",
        VARIANT_STYLES[variant],
        className
      )}
    >
      {dot && (
        <span
          className={cn(
            "w-1.5 h-1.5 rounded-full",
            variant === "success" && "bg-success",
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
