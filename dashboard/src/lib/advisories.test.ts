import { test, expect } from "vitest";
import { databaseSummary } from "./advisories";

const cfg = {
  tables: {
    deals: { fields: [], indexes: [], rls: [{ operations: ["select"], using: "true" }] },
    activities: { fields: [], indexes: [], rls: [] },
  },
} as any;

test("databaseSummary sorts tables and reports rls counts", () => {
  expect(databaseSummary(cfg)).toEqual([
    { name: "activities", rlsCount: 0 },
    { name: "deals", rlsCount: 1 },
  ]);
});
