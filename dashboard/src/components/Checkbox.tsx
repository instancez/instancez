import { Check } from "lucide-react";
import type { ReactNode } from "react";

type CheckboxProps = {
  checked: boolean;
  onChange: (checked: boolean) => void;
  /** Text rendered to the right of the box; clicking it also toggles. */
  label?: ReactNode;
  /** Extra classes for the wrapper — use to set the label's text size. */
  className?: string;
  "aria-label"?: string;
  disabled?: boolean;
};

/**
 * Checkbox is the styled multi-select control used where a Toggle would be the
 * wrong mental model — i.e. "pick which of N" rather than a single on/off
 * setting (RLS operations, searchable fields). For single booleans use Toggle.
 */
export function Checkbox({ checked, onChange, label, className, disabled, ...rest }: CheckboxProps) {
  const box = (
    <button
      type="button"
      role="checkbox"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`flex items-center justify-center w-[18px] h-[18px] rounded-md border transition-colors shrink-0 ${
        disabled ? "opacity-50 cursor-not-allowed" : "cursor-pointer"
      } ${
        checked
          ? "bg-accent border-accent text-background"
          : "bg-input border-border hover:border-border-hover"
      }`}
      {...rest}
    >
      {checked && <Check size={12} strokeWidth={3} />}
    </button>
  );

  if (label == null) return box;

  return (
    <span className={`inline-flex items-center gap-2 text-foreground ${className ?? ""}`}>
      {box}
      <span
        className={disabled ? "" : "cursor-pointer"}
        onClick={() => !disabled && onChange(!checked)}
      >
        {label}
      </span>
    </span>
  );
}
