import { cn } from "../lib/utils";

interface PageHeaderProps {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  className?: string;
}

export function PageHeader({
  title,
  description,
  actions,
  className,
}: PageHeaderProps) {
  return (
    <div
      className={cn(
        "flex items-start justify-between gap-4 px-8 pt-8 pb-6 mb-6 border-b border-border",
        className
      )}
    >
      <div className="min-w-0">
        <h1 className="flex items-center gap-3 text-2xl font-medium tracking-tight text-foreground">
          <span aria-hidden="true" className="w-2 h-2 bg-foreground shrink-0" />
          <span className="truncate">{title}</span>
        </h1>
        {description && (
          <p className="mt-1.5 pl-5 text-sm text-muted-foreground">
            {description}
          </p>
        )}
      </div>
      {actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
    </div>
  );
}
