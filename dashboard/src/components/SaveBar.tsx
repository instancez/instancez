import { Loader2, Save } from "lucide-react";
import type { ValidationError } from "../lib/types";

interface SaveBarProps {
  onSave: () => void;
  saving: boolean;
  errors: ValidationError[];
  dirty?: boolean;
}

export function SaveBar({ onSave, saving, errors, dirty = true }: SaveBarProps) {
  if (!dirty && errors.length === 0) return null;

  return (
    <div className="fixed bottom-0 left-60 right-0 z-30 border-t border-border bg-surface/95 backdrop-blur-sm px-8 py-3">
      <div className="flex items-center justify-between gap-4">
        <div className="flex-1 min-w-0">
          {errors.length > 0 && (
            <div className="space-y-1">
              {errors.slice(0, 3).map((err, i) => (
                <p key={i} className="text-xs text-destructive truncate">
                  {err.path && (
                    <span className="font-mono font-medium">{err.path}: </span>
                  )}
                  {err.message}
                  {err.suggestion && (
                    <span className="text-muted-foreground">
                      {" "}
                      — {err.suggestion}
                    </span>
                  )}
                </p>
              ))}
              {errors.length > 3 && (
                <p className="text-xs text-muted-foreground">
                  +{errors.length - 3} more errors
                </p>
              )}
            </div>
          )}
        </div>
        <button
          onClick={onSave}
          disabled={saving}
          className="inline-flex items-center gap-2 px-5 py-2 rounded-lg bg-accent text-white text-sm font-medium hover:bg-accent-hover transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
        >
          {saving ? (
            <Loader2 size={14} className="animate-spin" />
          ) : (
            <Save size={14} />
          )}
          {saving ? "Saving..." : "Save Changes"}
        </button>
      </div>
    </div>
  );
}
