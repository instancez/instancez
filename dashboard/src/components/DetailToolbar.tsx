import { Link } from "react-router-dom";
import { ChevronLeft, Trash2 } from "lucide-react";
import { Button } from "./ui";

interface DetailToolbarProps {
  /** Label for the back link (the parent area's name, e.g. "Tables"). */
  backLabel: string;
  /** Delete handler for the entity being viewed. */
  onDelete: () => void;
}

/**
 * Slim in-content toolbar for detail pages: a back link on the left and a
 * delete button on the right. This is content (not page chrome), so it renders
 * natively wherever the page is mounted. Styles mirror PageHeader's old
 * back/delete affordances.
 */
export function DetailToolbar({ backLabel, onDelete }: DetailToolbarProps) {
  return (
    <div className="flex items-center justify-between gap-4 mb-6">
      <Link
        to=".."
        relative="path"
        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium border border-border text-muted-foreground hover:text-foreground hover:bg-surface-hover transition-colors"
      >
        <ChevronLeft size={14} />
        {backLabel}
      </Link>
      <Button variant="danger-outline" size="sm" onClick={onDelete}>
        <Trash2 size={14} />
        Delete
      </Button>
    </div>
  );
}
