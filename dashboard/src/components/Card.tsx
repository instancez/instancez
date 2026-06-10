import { cn } from "../lib/utils";

interface CardProps {
  children: React.ReactNode;
  className?: string;
  onClick?: () => void;
  hoverable?: boolean;
}

export function Card({
  children,
  className,
  onClick,
  hoverable = false,
}: CardProps) {
  return (
    <div
      onClick={onClick}
      className={cn(
        "rounded-xl border border-border bg-surface sheen shadow-gloss p-5",
        hoverable &&
          "hover:bg-surface-hover hover:border-border-hover transition-colors cursor-pointer",
        onClick && "cursor-pointer",
        className
      )}
    >
      {children}
    </div>
  );
}

export function CardTitle({ children }: { children: React.ReactNode }) {
  return (
    <h3 className="text-sm font-medium text-muted-foreground">{children}</h3>
  );
}

export function CardValue({ children }: { children: React.ReactNode }) {
  return (
    <p className="mt-1 text-2xl font-semibold text-foreground tabular-nums">
      {children}
    </p>
  );
}
