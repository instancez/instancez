import {
  createContext,
  useContext,
  useState,
  type ButtonHTMLAttributes,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
} from "react";
import { ChevronDown, ChevronRight, CheckCircle2, Loader2 } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { cn } from "../lib/utils";

/* ------------------------------------------------------------------ */
/* Surface nesting                                                      */
/*                                                                      */
/* Every box in the dashboard is a Panel. Panels track their nesting    */
/* depth through context and alternate background between `surface`    */
/* and `primary`, so a box always contrasts with its parent: canvas →  */
/* surface (white/near-black) → primary (gray inset) → surface → …     */
/* ------------------------------------------------------------------ */

const SurfaceDepthContext = createContext(0);

/** Marks a region as already sitting on a surface (e.g. the content card). */
export function SurfaceProvider({
  depth,
  children,
}: {
  depth: number;
  children: ReactNode;
}) {
  return (
    <SurfaceDepthContext.Provider value={depth}>
      {children}
    </SurfaceDepthContext.Provider>
  );
}

export function useSurfaceBg(): string {
  const depth = useContext(SurfaceDepthContext);
  return depth % 2 === 0 ? "bg-surface" : "bg-primary";
}

interface PanelProps {
  children: ReactNode;
  className?: string;
  onClick?: () => void;
  hoverable?: boolean;
}

/** The one box shape: rounded-xl, 1px border, depth-aware background. */
export function Panel({ children, className, onClick, hoverable }: PanelProps) {
  const depth = useContext(SurfaceDepthContext);
  const bg = depth % 2 === 0 ? "bg-surface" : "bg-primary";
  return (
    <div
      onClick={onClick}
      className={cn(
        bg,
        "border border-border rounded-xl",
        hoverable &&
          "transition-all duration-200 ease-out hover:bg-surface-hover hover:border-border-hover hover:shadow-lifted hover:-translate-y-1 cursor-pointer",
        onClick && "cursor-pointer",
        className
      )}
    >
      <SurfaceDepthContext.Provider value={depth + 1}>
        {children}
      </SurfaceDepthContext.Provider>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Buttons                                                              */
/* ------------------------------------------------------------------ */

type ButtonVariant =
  | "primary"
  | "outline"
  | "ghost"
  | "dashed"
  | "danger"
  | "danger-outline"
  | "danger-ghost";

type ButtonSize = "xs" | "sm" | "md" | "icon";

const BUTTON_VARIANTS: Record<ButtonVariant, string> = {
  primary: "bg-accent text-background hover:bg-accent-hover",
  outline:
    "border border-border text-muted-foreground hover:text-foreground hover:bg-surface-hover",
  ghost: "text-muted-foreground hover:text-foreground hover:bg-surface-hover",
  dashed:
    "border border-dashed border-border text-muted-foreground hover:text-foreground hover:border-border-hover",
  danger: "bg-destructive text-background hover:bg-destructive-hover",
  "danger-outline":
    "border border-destructive/30 text-destructive hover:bg-destructive/10",
  "danger-ghost":
    "text-muted-foreground hover:text-destructive hover:bg-destructive/10",
};

const BUTTON_SIZES: Record<ButtonSize, string> = {
  xs: "gap-1 px-2 py-1 rounded-md text-xs",
  sm: "gap-1.5 px-3 py-1.5 rounded-lg text-sm",
  md: "gap-2 px-4 py-2 rounded-lg text-sm",
  icon: "p-1.5 rounded-md",
};

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
}

export function Button({
  variant = "primary",
  size = "md",
  loading = false,
  className,
  disabled,
  children,
  type = "button",
  ...rest
}: ButtonProps) {
  return (
    <button
      type={type}
      disabled={disabled || loading}
      className={cn(
        "inline-flex items-center justify-center font-medium whitespace-nowrap transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed",
        BUTTON_SIZES[size],
        BUTTON_VARIANTS[variant],
        className
      )}
      {...rest}
    >
      {loading && <Loader2 size={14} className="animate-spin" />}
      {children}
    </button>
  );
}

/* ------------------------------------------------------------------ */
/* Form controls                                                        */
/* ------------------------------------------------------------------ */

const CONTROL_BASE =
  "w-full rounded-lg border border-input-border bg-input text-foreground placeholder:text-muted-foreground/50 focus:outline-none focus:border-ring transition-colors";

const CONTROL_SIZES = {
  sm: "px-2 py-1 text-xs",
  md: "px-3 py-2 text-sm",
} as const;

interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  mono?: boolean;
  inputSize?: keyof typeof CONTROL_SIZES;
}

export function Input({
  mono = false,
  inputSize = "md",
  className,
  ...rest
}: InputProps) {
  return (
    <input
      className={cn(
        CONTROL_BASE,
        CONTROL_SIZES[inputSize],
        mono && "font-mono",
        className
      )}
      {...rest}
    />
  );
}

interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  mono?: boolean;
  inputSize?: keyof typeof CONTROL_SIZES;
}

export function Select({
  mono = false,
  inputSize = "md",
  className,
  children,
  ...rest
}: SelectProps) {
  return (
    <select
      className={cn(
        CONTROL_BASE,
        CONTROL_SIZES[inputSize],
        mono && "font-mono",
        "cursor-pointer",
        className
      )}
      {...rest}
    >
      {children}
    </select>
  );
}

interface FieldProps {
  label: ReactNode;
  htmlFor?: string;
  hint?: ReactNode;
  className?: string;
  children: ReactNode;
}

/** Label + control + optional hint: the one way a config value is edited. */
export function Field({ label, htmlFor, hint, className, children }: FieldProps) {
  return (
    <div className={className}>
      <label htmlFor={htmlFor} className="t-label block mb-1.5">
        {label}
      </label>
      {children}
      {hint && <p className="mt-1.5 text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Config presentation                                                  */
/* ------------------------------------------------------------------ */

interface SectionProps {
  title: ReactNode;
  description?: ReactNode;
  icon?: LucideIcon;
  actions?: ReactNode;
  className?: string;
  children?: ReactNode;
}

/** A titled config group: every settings page is a stack of Sections. */
export function Section({
  title,
  description,
  icon: Icon,
  actions,
  className,
  children,
}: SectionProps) {
  return (
    <Panel className={className}>
      <div
        className={cn(
          "flex items-center justify-between gap-4 px-5 pt-4 pb-3",
          children != null && "border-b border-border"
        )}
      >
        <div className="min-w-0">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
            {Icon && <Icon size={15} className="text-muted-foreground shrink-0" />}
            {title}
          </h2>
          {description && (
            <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
          )}
        </div>
        {actions && <div className="flex items-center gap-2 shrink-0">{actions}</div>}
      </div>
      {children != null && <div className="px-5 py-4 space-y-4">{children}</div>}
    </Panel>
  );
}

interface ListRowProps {
  icon: LucideIcon;
  title: string;
  meta?: ReactNode;
  badges?: ReactNode;
  onClick?: () => void;
}

/** Clickable entity row used by every list page (tables, buckets, fns…). */
export function ListRow({ icon: Icon, title, meta, badges, onClick }: ListRowProps) {
  const bg = useSurfaceBg();
  return (
    <button
      onClick={onClick}
      className={cn(
        bg,
        "w-full flex items-center justify-between gap-3 px-5 py-3.5 rounded-xl border border-border hover:bg-surface-hover hover:border-border-hover transition-colors cursor-pointer text-left group"
      )}
    >
      <span className="flex items-center gap-3 min-w-0">
        <Icon
          size={16}
          className="shrink-0 text-muted-foreground group-hover:text-foreground transition-colors"
        />
        <span className="text-sm font-mono font-medium text-foreground truncate">
          {title}
        </span>
        {meta && (
          <span className="text-xs text-muted-foreground truncate">{meta}</span>
        )}
      </span>
      {badges && (
        <span className="flex items-center gap-2 shrink-0">{badges}</span>
      )}
    </button>
  );
}

interface CheckCardProps {
  selected: boolean;
  onClick: () => void;
  title: ReactNode;
  description?: ReactNode;
}

/** Selectable option card (provider pickers etc.). */
export function CheckCard({ selected, onClick, title, description }: CheckCardProps) {
  const bg = useSurfaceBg();
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "relative text-left px-4 py-3 rounded-xl border transition-all cursor-pointer",
        selected
          ? "border-accent bg-accent/5"
          : cn(bg, "border-border hover:border-border-hover")
      )}
    >
      {selected && (
        <CheckCircle2 size={14} className="absolute top-3 right-3 text-accent" />
      )}
      <p className="text-sm font-medium text-foreground">{title}</p>
      {description && (
        <p className="text-xs text-muted-foreground mt-0.5">{description}</p>
      )}
    </button>
  );
}

interface DisclosureProps {
  label: ReactNode;
  defaultOpen?: boolean;
  children: ReactNode;
}

/** Collapsible pane (migration preview, test runner…). */
export function Disclosure({ label, defaultOpen = false, children }: DisclosureProps) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="border-t border-border pt-4">
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors cursor-pointer"
      >
        {open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        {label}
      </button>
      {open && <div className="mt-3">{children}</div>}
    </div>
  );
}
