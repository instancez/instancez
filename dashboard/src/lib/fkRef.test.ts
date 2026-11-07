import { describe, it, expect } from "vitest";
import { fkOptions, fkColumnType } from "./fkRef";

const f = (name: string, type = "", ref?: string) => ({
  name,
  type,
  ...(ref ? { foreign_key: { references: ref, on_delete: "cascade" } } : {}),
});
const tables = {
  posts: { fields: [f("id", "uuid"), f("parent", "uuid"), f("n"), f("loop", "", "posts.loop")] },
  big: { fields: [f("id", "bigserial")] },
  ser: { fields: [f("id", "serial")] },
  int: { fields: [f("id", "integer")] },
  small: { fields: [f("id", "smallint")] },
  vc: { fields: [f("id", "varchar(64)")] },
  up: { fields: [f("id", "UUID")] },
  bl: { fields: [f("id", "BOOL")] },
  child: { fields: [f("pid", "", "posts.id")] },
  priv: { schema: "app", fields: [f("id", "uuid")] },
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
  it("qualifies non-public schemas", () => {
    const o = fkOptions(tables);
    expect(o).toContain("app.priv.id");
    expect(o).not.toContain("priv.id");
  });
  it("handles zero tables, zero columns and empty names", () => {
    expect(fkOptions({})).toEqual(["auth.users.id"]);
    expect(fkOptions({ empty: { fields: [] }, nofields: {} })).toEqual(["auth.users.id"]);
    expect(fkOptions({ "": { fields: [f("id")] } })).toEqual(["auth.users.id"]);
  });
});

describe("fkColumnType", () => {
  it("resolves auth.users.id to uuid", () => {
    expect(fkColumnType("auth.users.id", {})).toBe("uuid");
    expect(fkColumnType("AUTH.USERS.ID", {})).toBe("uuid");
  });
  it("resolves table.column and public.table.column", () => {
    expect(fkColumnType("posts.id", tables)).toBe("uuid");
    expect(fkColumnType("public.posts.id", tables)).toBe("uuid");
  });
  it("resolves self-references", () => {
    expect(fkColumnType("posts.parent", tables)).toBe("uuid");
  });
  it("collapses integer-class targets to bigint like the server", () => {
    for (const t of ["big", "ser", "int", "small"]) expect(fkColumnType(`${t}.id`, tables)).toBe("bigint");
  });
  it("normalizes other types like the server", () => {
    expect(fkColumnType("vc.id", tables)).toBe("varchar");
    expect(fkColumnType("up.id", tables)).toBe("uuid");
    expect(fkColumnType("bl.id", tables)).toBe("boolean");
  });
  it("follows a chain of untyped FKs", () => {
    expect(fkColumnType("child.pid", tables)).toBe("uuid");
  });
  it("stops on cyclic references", () => {
    expect(fkColumnType("posts.loop", tables)).toBeUndefined();
  });
  it("resolves schema-qualified refs only against the matching schema", () => {
    expect(fkColumnType("app.priv.id", tables)).toBe("uuid");
    expect(fkColumnType("priv.id", tables)).toBeUndefined();
    expect(fkColumnType("other.priv.id", tables)).toBeUndefined();
    expect(fkColumnType("app.posts.id", tables)).toBeUndefined();
  });
  it("is undefined for empty, null, undefined", () => {
    expect(fkColumnType("", tables)).toBeUndefined();
    expect(fkColumnType(null, tables)).toBeUndefined();
    expect(fkColumnType(undefined, tables)).toBeUndefined();
  });
  it("is undefined for malformed refs", () => {
    expect(fkColumnType("posts", tables)).toBeUndefined();
    expect(fkColumnType("a.b.c.d", tables)).toBeUndefined();
    expect(fkColumnType("posts.", tables)).toBeUndefined();
  });
  it("is undefined for unknown table, column, or untyped column", () => {
    expect(fkColumnType("nope.id", tables)).toBeUndefined();
    expect(fkColumnType("posts.nope", tables)).toBeUndefined();
    expect(fkColumnType("posts.n", tables)).toBeUndefined();
    expect(fkColumnType("empty.id", tables)).toBeUndefined();
    expect(fkColumnType("nofields.id", tables)).toBeUndefined();
  });
  it("does not guess unknown auth columns or users.id", () => {
    expect(fkColumnType("auth.users.email", tables)).toBeUndefined();
    expect(fkColumnType("users.id", tables)).toBeUndefined();
  });
});
