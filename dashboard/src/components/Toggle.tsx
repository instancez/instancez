import type { ReactNode } from "react";

type ToggleProps = {
  checked: boolean;
  onChange: (checked: boolean) => void;
  /** Optional text rendered to the right of the switch; clicking it also toggles. */
  label?: ReactNode;
  "aria-label"?: string;
  disabled?: boolean;
};

/**
 * Toggle is the single switch control used across the dashboard for boolean
 * settings. It renders an accessible `role="switch"` button; pass `label` to
 * get the standard "switch + text" row, or omit it for a bare switch (e.g. in
 * a table cell, where you should pass `aria-label`).
 */
export function Toggle({ checked, onChange, label, disabled, ...rest }: ToggleProps) {
  const sw = (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`relative w-11 h-6 rounded-full transition-all shrink-0 ${
        disabled ? "opacity-50 cursor-not-allowed" : "cursor-pointer"
      } ${
        checked
          ? "bg-accent shadow-[inset_0_1px_2px_rgb(0_0_0/0.2),0_0_10px_rgb(255_255_255/0.15)]"
          : "bg-secondary shadow-[inset_0_1px_2px_rgb(0_0_0/0.5)]"
      }`}
      {...rest}
    >
      <span
        className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full transition-all shadow-[inset_0_1px_0_rgb(255_255_255/0.25),0_1px_2px_rgb(0_0_0/0.4)] ${
          checked ? "translate-x-5 bg-background" : "bg-muted-foreground"
        }`}
      />
    </button>
  );

  if (label == null) return sw;

  return (
    <span className="inline-flex items-center gap-3 text-sm text-foreground">
      {sw}
      <span
        className={disabled ? "" : "cursor-pointer"}
        onClick={() => !disabled && onChange(!checked)}
      >
        {label}
      </span>
    </span>
  );
}
