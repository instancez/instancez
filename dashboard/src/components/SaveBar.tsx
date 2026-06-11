import { Save } from "lucide-react";
import { Button } from "./ui";
import type { ValidationError } from "../lib/types";

interface SaveBarProps {
  onSave: () => void;
  saving: boolean;
  errors: ValidationError[];
  dirty?: boolean;
}

export function SaveBar({ onSave, saving, errors, dirty = true }: SaveBarProps) {
  if (!dirty && errors.length === 0) return null;

  // Floats inside the content card: 8px page margin + 240px sidebar +
  // 8px gap + 16px inset = 272px from the viewport's left edge.
  return (
    <div className="fixed bottom-6 left-[272px] right-6 z-30 rounded-xl border border-border bg-surface shadow-lifted px-5 py-3 animate-rise">
      <div className="flex items-center justify-between gap-4">
        <div className="flex-1 min-w-0">
          {errors.length > 0 && (
            <div className="space-y-1">
              {errors.slice(0, 3).map((err, i) => (
                <p key={i} className="text-xs font-mono text-destructive truncate">
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
        <Button onClick={onSave} loading={saving}>
          {!saving && <Save size={14} />}
          {saving ? "Saving..." : "Save Changes"}
        </Button>
      </div>
    </div>
  );
}
