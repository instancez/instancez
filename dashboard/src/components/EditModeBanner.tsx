import type { ConfigStatus } from "../lib/types";

type Props = { status: ConfigStatus | null };

export function EditModeBanner({ status }: Props) {
  if (!status || status.dashboard_mode !== "readwrite") return null;
  return (
    <div
      role="status"
      className="bg-blue-50 border-b border-blue-200 text-blue-900 px-4 py-3 text-sm"
    >
      <strong>Live edit mode.</strong>{" "}
      Changes you make here are written directly to <code>{status.config_source}</code>{" "}
      and applied to the database. If your team manages <code>instancez.yaml</code>{" "}
      in git, mirror these changes there — anything written here will be overwritten the
      next time the source is updated outside the dashboard.
    </div>
  );
}
