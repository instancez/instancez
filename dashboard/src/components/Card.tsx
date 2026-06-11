import { Panel } from "./ui";

interface CardProps {
  children: React.ReactNode;
  className?: string;
  onClick?: () => void;
  hoverable?: boolean;
}

/** Padded Panel — kept as the stat/summary card shape. */
export function Card({
  children,
  className,
  onClick,
  hoverable = false,
}: CardProps) {
  return (
    <Panel
      onClick={onClick}
      hoverable={hoverable}
      className={`p-5 ${className ?? ""}`}
    >
      {children}
    </Panel>
  );
}

export function CardTitle({ children }: { children: React.ReactNode }) {
  return <h3 className="t-label">{children}</h3>;
}

export function CardValue({ children }: { children: React.ReactNode }) {
  return (
    <p className="mt-2 text-3xl font-semibold tracking-tight text-foreground tabular-nums">
      {children}
    </p>
  );
}
