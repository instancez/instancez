import { cn } from "../lib/utils";

interface DiffViewerProps {
  statements: string[];
  isDestructive: boolean;
}

export function DiffViewer({ statements, isDestructive }: DiffViewerProps) {
  if (statements.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-primary p-4">
        <p className="text-sm text-muted-foreground text-center">
          No pending migrations
        </p>
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-border bg-primary overflow-hidden">
      {isDestructive && (
        <div className="px-4 py-2 bg-destructive/10 border-b border-destructive/20">
          <p className="text-xs font-medium text-destructive">
            Warning: This migration contains destructive operations
          </p>
        </div>
      )}
      <div className="p-4 space-y-2 overflow-x-auto">
        {statements.map((stmt, i) => {
          const isDrop =
            stmt.toUpperCase().includes("DROP") ||
            stmt.toUpperCase().includes("DELETE");
          const isAlterDrop = stmt.toUpperCase().includes("DROP COLUMN");
          const isAdd =
            stmt.toUpperCase().includes("ADD") ||
            stmt.toUpperCase().includes("CREATE");

          return (
            <pre
              key={i}
              className={cn(
                "text-xs font-mono p-2 rounded-sm leading-relaxed whitespace-pre-wrap",
                isDrop || isAlterDrop
                  ? "bg-destructive/10 text-destructive"
                  : isAdd
                    ? "bg-accent/10 text-accent"
                    : "bg-muted text-foreground"
              )}
            >
              {stmt}
            </pre>
          );
        })}
      </div>
    </div>
  );
}
