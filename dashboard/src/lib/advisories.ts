import type { Config } from "./types";

export function databaseSummary(config: Config): { name: string; rlsCount: number }[] {
  return Object.entries(config.tables ?? {})
    .map(([name, t]) => ({ name, rlsCount: t.rls?.length ?? 0 }))
    .sort((a, b) => a.name.localeCompare(b.name));
}
