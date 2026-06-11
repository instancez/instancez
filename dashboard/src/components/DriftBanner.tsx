import { TriangleAlert } from "lucide-react";
import type { ConfigStatus } from "../lib/types";

type Props = { status: ConfigStatus | null };

export function DriftBanner({ status }: Props) {
  if (!status || status.status !== "drift") return null;
  return (
    <div
      role="alert"
      className="border-t border-warning/30 bg-warning/10 px-4 py-2.5 text-sm text-foreground"
    >
      <span className="inline-flex items-start gap-2">
        <TriangleAlert size={14} className="mt-0.5 shrink-0 text-warning" aria-hidden="true" />
        <span>
          <strong>Configuration drift.</strong>{" "}
          The source <code className="font-mono">{status.config_source}</code> has changes that
          failed to apply: <code className="font-mono">{status.last_error}</code>. The server is
          running on the last successful config from{" "}
          <time dateTime={status.running.applied_at}>{status.running.applied_at}</time>.{" "}
          Fix the source and restart, or revert the failing change.
        </span>
      </span>
    </div>
  );
}
