import { useNavigate } from "react-router-dom";
import { ArrowLeft, Trash2 } from "lucide-react";
import { Button } from "./ui";
import { cn } from "../lib/utils";

interface PageHeaderProps {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  /** Route to navigate back to; renders the standard Back button. */
  backTo?: string;
  /** Renders the standard Delete button for the entity being viewed. */
  onDelete?: () => void;
  className?: string;
}

export function PageHeader({
  title,
  description,
  actions,
  backTo,
  onDelete,
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
      <div className="flex items-center gap-2 shrink-0">
        {backTo && <BackButton to={backTo} />}
        {actions}
        {onDelete && (
          <Button variant="danger-outline" size="sm" onClick={onDelete}>
            <Trash2 size={14} />
            Delete
          </Button>
        )}
      </div>
    </div>
  );
}

function BackButton({ to }: { to: string }) {
  const navigate = useNavigate();
  return (
    <Button variant="outline" size="sm" onClick={() => navigate(to)}>
      <ArrowLeft size={14} />
      Back
    </Button>
  );
}
