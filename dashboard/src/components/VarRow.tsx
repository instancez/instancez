import { useState } from "react";
import { Input, Button } from "./ui";

export interface VarRowProps {
  /** Human-readable config name, e.g. "API key" */
  label: string;
  /** Env var that can supply this value, e.g. INSTANCEZ_RESEND_API_KEY */
  name: string;
  isSet: boolean;
  canWrite: boolean;
  inputValue: string;
  onInputChange: (value: string) => void;
}

/**
 * One provider config value: label + set/unset status, the env var that can
 * supply the value as a caption, and a write input. When the var is already
 * set, the input hides behind an explicit "Override" affordance so it's clear
 * a staged value replaces the current one.
 */
export function VarRow({ label, name, isSet, canWrite, inputValue, onInputChange }: VarRowProps) {
  const [overriding, setOverriding] = useState(false);
  const showInput = canWrite && (!isSet || overriding || inputValue !== "");

  return (
    <div className="py-2.5 space-y-1.5 first:pt-0 last:pb-0">
      <div className="flex items-center justify-between gap-3">
        <span className="text-xs font-medium text-foreground">{label}</span>
        <span className="flex items-center gap-2">
          <span
            className={`shrink-0 text-[11px] font-medium ${isSet ? "text-green-600 dark:text-green-400" : "text-destructive"}`}
          >
            {isSet ? "✓ set" : "✗ unset"}
          </span>
          {canWrite && isSet && !showInput && (
            <Button variant="dashed" size="sm" onClick={() => setOverriding(true)}>
              Override
            </Button>
          )}
        </span>
      </div>
      {showInput && (
        <div className="flex items-center gap-2">
          <Input
            type="password"
            aria-label={name}
            placeholder={isSet ? "new value — overrides the current one…" : "enter value…"}
            value={inputValue}
            onChange={(e) => onInputChange(e.target.value)}
            className="flex-1 h-8 text-xs"
          />
          {isSet && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setOverriding(false);
                onInputChange("");
              }}
            >
              Keep current
            </Button>
          )}
        </div>
      )}
      <p className="text-[11px] text-muted-foreground/70">
        env <code className="font-mono">{name}</code>
      </p>
    </div>
  );
}
