import { PencilLine } from "lucide-react";
import type { ConfigStatus } from "../lib/types";

type Props = { status: ConfigStatus | null };

export function EditModeBanner({ status }: Props) {
  if (!status || status.dashboard_mode !== "readwrite") return null;
  return (
    <div
      role="status"
      className="bg-muted border-b border-border px-4 py-3 text-sm text-foreground"
    >
      <span className="inline-flex items-start gap-2">
        <PencilLine size={14} className="mt-0.5 shrink-0" aria-hidden="true" />
        <span>
          <strong>Live edit mode.</strong>{" "}
          Changes you make here are written directly to{" "}
          <code className="font-mono">{status.config_source}</code>{" "}
          and applied to the database. If your team manages{" "}
          <code className="font-mono">instancez.yaml</code> in git, mirror these changes there —
          anything written here will be overwritten the next time the source is updated outside
          the dashboard.
        </span>
      </span>
    </div>
  );
}
