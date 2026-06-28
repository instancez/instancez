import type { Config, StatsResponse } from "./types";

export type Advisory = { level: "warn"; table: string; message: string };

export function exposedTables(config: Config): string[] {
  return Object.entries(config.tables ?? {})
    .filter(([, t]) => (t.rls?.length ?? 0) === 0)
    .map(([name]) => name)
    .sort();
}

export function securityAdvisories(config: Config): Advisory[] {
  return exposedTables(config).map((table) => ({
    level: "warn",
    table,
    message: `${table} has no RLS policy and is publicly readable through the API.`,
  }));
}

export function databaseSummary(
  config: Config,
  stats: StatsResponse | null,
): { name: string; rlsCount: number; rowCount: number | null }[] {
  return Object.entries(config.tables ?? {})
    .map(([name, t]) => ({
      name,
      rlsCount: t.rls?.length ?? 0,
      rowCount: stats?.tables?.[name]?.row_count ?? null,
    }))
    .sort((a, b) => a.name.localeCompare(b.name));
}
