import { describe, it, expect } from "vitest";
import { pageQuery } from "./tableQuery";

describe("pageQuery", () => {
  it("quotes identifiers and escapes embedded quotes", () => {
    expect(pageQuery('we"ird', 'i"d', 0)).toBe('SELECT * FROM "we""ird" ORDER BY "i""d" LIMIT 21 OFFSET 0');
  });
  it("orders by the first column without a primary key", () => {
    expect(pageQuery("t", undefined, 0)).toBe('SELECT * FROM "t" ORDER BY 1 LIMIT 21 OFFSET 0');
  });
  it("offsets by page", () => {
    expect(pageQuery("t", "id", 2)).toContain("OFFSET 40");
  });
});
