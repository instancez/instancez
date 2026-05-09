import type { ConfigStatus } from "../lib/types";

type Props = { status: ConfigStatus | null };

export function DriftBanner({ status }: Props) {
  if (!status || status.status !== "drift") return null;
  return (
    <div
      role="alert"
      className="bg-amber-100 border-b border-amber-300 text-amber-900 px-4 py-3 text-sm"
    >
      <strong>⚠️ Configuration drift.</strong>{" "}
      The source <code>{status.config_source}</code> has changes that failed to apply:{" "}
      <code>{status.last_error}</code>. The server is running on the last successful
      config from{" "}
      <time dateTime={status.running.applied_at}>{status.running.applied_at}</time>.{" "}
      Fix the source and restart, or revert the failing change.
    </div>
  );
}
