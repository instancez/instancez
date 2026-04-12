import type { ReactNode } from "react";
import { Info, AlertTriangle, AlertCircle, Lightbulb } from "lucide-react";

type CalloutVariant = "info" | "warning" | "danger" | "tip";

const variants: Record<
  CalloutVariant,
  { icon: typeof Info; border: string; bg: string; iconColor: string; titleColor: string }
> = {
  info: {
    icon: Info,
    border: "border-info/20",
    bg: "bg-info/5",
    iconColor: "text-info",
    titleColor: "text-info",
  },
  warning: {
    icon: AlertTriangle,
    border: "border-warning/20",
    bg: "bg-warning/5",
    iconColor: "text-warning",
    titleColor: "text-warning",
  },
  danger: {
    icon: AlertCircle,
    border: "border-destructive/20",
    bg: "bg-destructive/5",
    iconColor: "text-destructive",
    titleColor: "text-destructive",
  },
  tip: {
    icon: Lightbulb,
    border: "border-accent/20",
    bg: "bg-accent/5",
    iconColor: "text-accent",
    titleColor: "text-accent",
  },
};

interface CalloutProps {
  variant?: CalloutVariant;
  title?: string;
  children: ReactNode;
}

export function Callout({ variant = "info", title, children }: CalloutProps) {
  const { icon: Icon, border, bg, iconColor, titleColor } = variants[variant];
  return (
    <div className={`my-6 flex gap-3.5 rounded-xl border ${border} ${bg} px-4 py-3.5`}>
      <div className={`${iconColor} mt-0.5 shrink-0`}>
        <Icon size={16} strokeWidth={2.25} />
      </div>
      <div className="min-w-0 text-sm leading-relaxed">
        {title && (
          <p className={`font-semibold ${titleColor} mb-1`}>{title}</p>
        )}
        <div className="text-muted-foreground [&>p]:m-0 [&>p]:leading-relaxed">{children}</div>
      </div>
    </div>
  );
}
