import { Trash2 } from "lucide-react";
import { Panel, Button, Field } from "./ui";
import { Checkbox } from "./Checkbox";
import { CodeEditor } from "./CodeEditor";
import { RLS_OPERATIONS } from "../lib/utils";
import { cn } from "../lib/utils";
import type { RLSPolicy } from "../lib/types";

interface RlsPolicyCardProps {
  policy: RLSPolicy;
  onChange: (policy: RLSPolicy) => void;
  onDelete: () => void;
  /** Optional one-click expressions rendered under the check editor. */
  quickFills?: { label: string; expr: string }[];
}

/**
 * The single RLS policy editor, shared by table and storage-bucket config:
 * permissive/restrictive type, operation checkboxes, and the check
 * expression.
 */
export function RlsPolicyCard({
  policy,
  onChange,
  onDelete,
  quickFills,
}: RlsPolicyCardProps) {
  return (
    <Panel className="p-4 space-y-3">
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-3">
          <Field label="Type">
            <div className="flex gap-1">
              {(["permissive", "restrictive"] as const).map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => onChange({ ...policy, type: t })}
                  className={cn(
                    "px-2.5 py-1 rounded-md text-xs font-medium transition-colors cursor-pointer",
                    (policy.type || "permissive") === t
                      ? t === "restrictive"
                        ? "bg-warning/10 text-warning border border-warning/30"
                        : "bg-info/10 text-info border border-info/30"
                      : "border border-border text-muted-foreground hover:text-foreground hover:bg-surface-hover"
                  )}
                >
                  {t}
                </button>
              ))}
            </div>
          </Field>
          <Field label="Operations">
            <div className="flex gap-2">
              {RLS_OPERATIONS.map((op) => (
                <Checkbox
                  key={op}
                  className="text-xs"
                  label={op}
                  checked={(policy.operations || []).includes(op)}
                  onChange={(c) =>
                    onChange({
                      ...policy,
                      operations: c
                        ? [...(policy.operations || []), op]
                        : (policy.operations || []).filter((o) => o !== op),
                    })
                  }
                />
              ))}
            </div>
          </Field>
        </div>
        <Button
          variant="danger-ghost"
          size="icon"
          aria-label="Delete policy"
          onClick={onDelete}
        >
          <Trash2 size={14} />
        </Button>
      </div>
      <Field label="Check Expression">
        <CodeEditor
          value={policy.check || ""}
          onChange={(val) => onChange({ ...policy, check: val })}
          minHeight="60px"
        />
        {quickFills && quickFills.length > 0 && (
          <div className="flex gap-2 mt-2">
            {quickFills.map(({ label, expr }) => (
              <Button
                key={label}
                variant="outline"
                size="xs"
                onClick={() => onChange({ ...policy, check: expr })}
              >
                {label}
              </Button>
            ))}
          </div>
        )}
      </Field>
    </Panel>
  );
}
