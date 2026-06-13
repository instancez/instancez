export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]!}`;
}

export function formatNumber(n: number): string {
  return new Intl.NumberFormat().format(n);
}

export function timeAgo(dateStr: string): string {
  const date = new Date(dateStr);
  const now = new Date();
  const seconds = Math.floor((now.getTime() - date.getTime()) / 1000);

  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

export const POSTGRES_TYPES = [
  "bigserial",
  "uuid",
  "text",
  "varchar(255)",
  "integer",
  "bigint",
  "boolean",
  "numeric(10,2)",
  "timestamptz",
  "date",
  "time",
  "jsonb",
  "text[]",
  "integer[]",
  "inet",
  "interval",
] as const;

export const SQL_DEFAULTS = [
  "now()",
  "uuid_v7()",
  "uuid_v4()",
  "current_date",
  "current_time",
  "true",
  "false",
] as const;

export const RLS_OPERATIONS = [
  "select",
  "insert",
  "update",
  "delete",
] as const;

export const CORS_METHODS = [
  "GET",
  "POST",
  "PATCH",
  "DELETE",
  "OPTIONS",
] as const;
