// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { EditorState } from "@codemirror/state";
import { sql, PostgreSQL } from "@codemirror/lang-sql";
import { ensureSyntaxTree } from "@codemirror/language";
import { formatSql, lintSql, sqlToRun } from "./sqlTools";

function stateAt(doc: string, head: number, anchor = head) {
  const state = EditorState.create({ doc, selection: { anchor, head }, extensions: [sql({ dialect: PostgreSQL })] });
  ensureSyntaxTree(state, doc.length, 5000);
  return state;
}

describe("sqlToRun", () => {
  const doc = "select 1;\n\nselect ';' as x;\ncreate function f() returns int as $$ select 1; select 2; $$ language sql;\nselect 3";

  it("runs the selection when there is one", () => {
    expect(sqlToRun(stateAt(doc, 0, 6))).toBe("select");
  });

  it("runs the statement under the cursor", () => {
    expect(sqlToRun(stateAt(doc, doc.indexOf("as x")))).toBe("select ';' as x;");
    expect(sqlToRun(stateAt(doc, doc.length))).toBe("select 3");
    expect(sqlToRun(stateAt(doc, 3))).toBe("select 1;");
  });

  it("does not split on semicolons in strings or $$ bodies", () => {
    expect(sqlToRun(stateAt(doc, doc.indexOf("select 2")))).toContain("create function f()");
    expect(sqlToRun(stateAt(doc, doc.indexOf("select 2")))).toContain("language sql;");
  });

  it("treats a whitespace-only selection as no selection, never the whole buffer", () => {
    const two = "select 1;\n\n   \nselect 2;";
    const gap = two.indexOf("\n");
    expect(sqlToRun(stateAt(two, gap + 4, gap + 1))).toBe("select 1;");
  });

  it("finds a statement past the parsed range in a fresh state", () => {
    const long = Array.from({ length: 3000 }, (_, i) => `select ${i};`).join("\n");
    const state = EditorState.create({ doc: long, selection: { anchor: long.length }, extensions: [sql({ dialect: PostgreSQL })] });
    expect(sqlToRun(state)).toBe("select 2999;");
  });

  it("uses the previous statement when the cursor sits in the gap after it", () => {
    expect(sqlToRun(stateAt(doc, 10))).toBe("select 1;");
  });

  it("falls back to the whole document for empty or whitespace-only input", () => {
    expect(sqlToRun(stateAt("", 0))).toBe("");
    expect(sqlToRun(stateAt("   ", 1))).toBe("   ");
    expect(sqlToRun(stateAt("select 1", 0, 3).update({ selection: { anchor: 2, head: 2 } }).state)).toBe("select 1");
  });
});

describe("formatSql", () => {
  it("formats Postgres SQL onto readable lines", async () => {
    const out = await formatSql("SELECT id,title FROM todos WHERE done = true ORDER BY id");
    expect(out).toBe("select\n  id,\n  title\nfrom\n  todos\nwhere\n  done = true\norder by\n  id");
  });

  it("returns empty and whitespace input untouched", async () => {
    expect(await formatSql("")).toBe("");
    expect(await formatSql("  \n")).toBe("  \n");
  });

  it("returns the input unchanged when the formatter cannot cope", async () => {
    const weird = "select 'unterminated";
    expect(await formatSql(weird)).toBe(weird);
  });
});

describe("lintSql", () => {
  it("accepts valid and empty SQL", async () => {
    expect(await lintSql("select 1; select 2")).toEqual([]);
    expect(await lintSql("")).toEqual([]);
    expect(await lintSql("  ")).toEqual([]);
  });

  it("underlines the offending token", async () => {
    const [d] = await lintSql("selec 1");
    expect(d).toMatchObject({ from: 0, to: 5, severity: "error" });
    expect(d!.message).toMatch(/syntax error at or near "selec"/);
  });

  it("underlines the last character for an error at the end of input", async () => {
    const [d] = await lintSql("select * from");
    expect(d).toMatchObject({ from: 12, to: 13 });
  });

  it("reports a mid-statement error at the right place", async () => {
    const [d] = await lintSql("select from where");
    expect(d).toMatchObject({ from: 12, to: 17 });
  });
});
