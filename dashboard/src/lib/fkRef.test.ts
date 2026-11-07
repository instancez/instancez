import { describe, it, expect } from "vitest";
import { fkOptions, fkColumnType } from "./fkRef";

const tables = {
  posts: { fields: [{ name: "id", type: "uuid" }, { name: "parent", type: "uuid" }, { name: "n", type: "" }] },
  empty: { fields: [] },
  nofields: {},
} as never;

describe("fkOptions", () => {
  it("lists table.column and auth.users.id, never users.id", () => {
    const o = fkOptions(tables);
    expect(o).toContain("posts.id");
    expect(o).toContain("auth.users.id");
    expect(o).not.toContain("users.id");
  });
  it("handles zero tables and zero columns", () => {
    expect(fkOptions({})).toEqual(["auth.users.id"]);
    expect(fkOptions({ empty: { fields: [] }, nofields: {} })).toEqual(["auth.users.id"]);
  });
});

describe("fkColumnType", () => {
  it("resolves auth.users.id to uuid", () => {
    expect(fkColumnType("auth.users.id", {})).toBe("uuid");
  });
  it("resolves table.column and public.table.column", () => {
    expect(fkColumnType("posts.id", tables)).toBe("uuid");
    expect(fkColumnType("public.posts.id", tables)).toBe("uuid");
  });
  it("resolves self-references", () => {
    expect(fkColumnType("posts.parent", tables)).toBe("uuid");
  });
  it("is undefined for empty, null, undefined", () => {
    expect(fkColumnType("", tables)).toBeUndefined();
    expect(fkColumnType(null, tables)).toBeUndefined();
    expect(fkColumnType(undefined, tables)).toBeUndefined();
  });
  it("is undefined for malformed refs", () => {
    expect(fkColumnType("posts", tables)).toBeUndefined();
    expect(fkColumnType("a.b.c.d", tables)).toBeUndefined();
  });
  it("is undefined for unknown table, column, or untyped column", () => {
    expect(fkColumnType("nope.id", tables)).toBeUndefined();
    expect(fkColumnType("posts.nope", tables)).toBeUndefined();
    expect(fkColumnType("posts.n", tables)).toBeUndefined();
    expect(fkColumnType("empty.id", tables)).toBeUndefined();
    expect(fkColumnType("nofields.id", tables)).toBeUndefined();
  });
  it("does not guess for other schemas or unknown auth columns", () => {
    expect(fkColumnType("auth.users.email", tables)).toBeUndefined();
    expect(fkColumnType("other.posts.id", tables)).toBeUndefined();
    expect(fkColumnType("users.id", tables)).toBeUndefined();
  });
});
