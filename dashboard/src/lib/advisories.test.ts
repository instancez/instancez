import { exposedTables, securityAdvisories, databaseSummary } from "./advisories";

const cfg = {
  tables: {
    deals: { fields: [], indexes: [], rls: [{ operations: ["select"], check: "true" }] },
    activities: { fields: [], indexes: [], rls: [] },
  },
} as any;

test("exposedTables lists tables with no RLS", () => {
  expect(exposedTables(cfg)).toEqual(["activities"]);
});

test("securityAdvisories describes each exposed table", () => {
  const a = securityAdvisories(cfg);
  expect(a).toHaveLength(1);
  expect(a[0]).toMatchObject({ level: "warn", table: "activities" });
  expect(a[0].message).toContain("no RLS policy");
});

test("databaseSummary merges rls counts with stats row counts", () => {
  const stats = { tables: { deals: { row_count: 12 } }, storage: {} } as any;
  expect(databaseSummary(cfg, stats)).toEqual([
    { name: "activities", rlsCount: 0, rowCount: null },
    { name: "deals", rlsCount: 1, rowCount: 12 },
  ]);
});

test("databaseSummary yields null rowCount when stats absent", () => {
  expect(databaseSummary(cfg, null).every(r => r.rowCount === null)).toBe(true);
});
