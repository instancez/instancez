import type { Config, StatsResponse } from "./types";

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
