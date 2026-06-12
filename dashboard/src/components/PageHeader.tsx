import { cn } from "../lib/utils";

interface PageHeaderProps {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  className?: string;
}

/**
 * Shell-owned page chrome: title + optional description and actions. Rendered
 * once by Layout from the matched route's handle — pages themselves are
 * chrome-free, so this no longer carries page-level back/delete affordances
 * (those moved into page content as DetailToolbar).
 */
export function PageHeader({
  title,
  description,
  actions,
  className,
}: PageHeaderProps) {
  return (
    <div
      className={cn(
        "flex items-start justify-between gap-4 px-8 pt-8 pb-6 mb-2",
        className
      )}
    >
      <div className="min-w-0">
        <h1 className="text-2xl font-bold tracking-tight text-foreground truncate">
          {title}
        </h1>
        {description && (
          <p className="mt-1.5 text-sm text-muted-foreground">
            {description}
          </p>
        )}
      </div>
      {actions && (
        <div className="flex items-center gap-2 shrink-0">{actions}</div>
      )}
    </div>
  );
}
