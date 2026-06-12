import { useMemo } from "react";
import { structuredPatch } from "diff";
import { FileDiff, X } from "lucide-react";
import { Button } from "./ui";

export interface DotenvChange {
  /** Env var name being written to .env */
  name: string;
  /** Last 4 characters of the new value, for recognition without disclosure */
  tail: string;
  /** True when the var is already set and this overwrites it */
  isUpdate: boolean;
}

interface ConfirmSaveDialogProps {
  current: string;
  proposed: string;
  dotenvChanges: DotenvChange[];
  saving: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

function lineClass(line: string): string {
  if (line.startsWith("+")) return "text-green-600 dark:text-green-400 bg-green-500/10";
  if (line.startsWith("-")) return "text-destructive bg-destructive/10";
  return "text-muted-foreground";
}

/**
 * Pre-save review: a unified diff of instancez.yaml plus the staged .env
 * writes (values masked to a last-4 tail). Nothing is applied until Confirm.
 */
export function ConfirmSaveDialog({
  current,
  proposed,
  dotenvChanges,
  saving,
  onConfirm,
  onCancel,
}: ConfirmSaveDialogProps) {
  const hunks = useMemo(
    () => structuredPatch("instancez.yaml", "instancez.yaml", current, proposed, "", "", { context: 3 }).hunks,
    [current, proposed]
  );

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-6">
      <div className="absolute inset-0 bg-black/40" onClick={onCancel} />
      <div className="relative w-full max-w-2xl max-h-[80vh] flex flex-col rounded-xl border border-border bg-surface shadow-lifted">
        <div className="flex items-center justify-between px-5 py-3 border-b border-border">
          <span className="flex items-center gap-2 text-sm font-medium text-foreground">
            <FileDiff size={15} />
            Review changes before saving
          </span>
          <Button variant="ghost" size="icon" aria-label="Close" onClick={onCancel}>
            <X size={14} />
          </Button>
        </div>

        <div className="flex-1 min-h-0 overflow-y-auto px-5 py-4 space-y-4">
          <section>
            <p className="text-xs font-medium text-foreground mb-2">instancez.yaml</p>
            {hunks.length === 0 ? (
              <p className="text-xs text-muted-foreground italic">No changes</p>
            ) : (
              <pre className="rounded-lg border border-border bg-background text-[11px] font-mono leading-5 overflow-x-auto">
                {hunks.map((hunk, hi) => (
                  <div key={hi} className={hi > 0 ? "border-t border-border" : ""}>
                    <div className="px-3 text-muted-foreground/60 select-none">
                      @@ -{hunk.oldStart},{hunk.oldLines} +{hunk.newStart},{hunk.newLines} @@
                    </div>
                    {hunk.lines.map((line, li) => (
                      <div key={li} className={`px-3 whitespace-pre ${lineClass(line)}`}>
                        {line}
                      </div>
                    ))}
                  </div>
                ))}
              </pre>
            )}
          </section>

          {dotenvChanges.length > 0 && (
            <section>
              <p className="text-xs font-medium text-foreground mb-2">.env</p>
              <div className="rounded-lg border border-border bg-background divide-y divide-border">
                {dotenvChanges.map((change) => (
                  <div key={change.name} className="flex items-center gap-3 px-3 py-2">
                    <code className="flex-1 min-w-0 text-[11px] font-mono text-foreground truncate">
                      {change.name}=<span className="text-muted-foreground">••••{change.tail}</span>
                    </code>
                    <span
                      className={`shrink-0 text-[11px] font-medium ${change.isUpdate ? "text-amber-600 dark:text-amber-400" : "text-green-600 dark:text-green-400"}`}
                    >
                      {change.isUpdate ? "updated" : "added"}
                    </span>
                  </div>
                ))}
              </div>
            </section>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 px-5 py-3 border-t border-border">
          <Button variant="ghost" onClick={onCancel} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={onConfirm} loading={saving}>
            {saving ? "Saving..." : "Confirm & Save"}
          </Button>
        </div>
      </div>
    </div>
  );
}
