import { linter, type Diagnostic } from "@codemirror/lint";
import { ensureSyntaxTree, syntaxTree } from "@codemirror/language";
import type { EditorState, Extension } from "@codemirror/state";

/** Formats Postgres SQL; returns the input unchanged if it can't be parsed. */
export async function formatSql(sql: string): Promise<string> {
  if (!sql.trim()) return sql;
  const { format } = await import("sql-formatter");
  try {
    return format(sql, { language: "postgresql", keywordCase: "lower", tabWidth: 2 });
  } catch {
    return sql;
  }
}

import wasmUrl from "@libpg-query/parser/wasm/libpg-query.wasm?url";

type PgParser = typeof import("@libpg-query/parser");
let parser: Promise<PgParser | null> | undefined;

/** The package fetches its wasm relative to the page, which breaks under a bundler; point it at the bundled asset instead. */
async function loadWithBundledWasm(): Promise<PgParser> {
  const realFetch = globalThis.fetch;
  globalThis.fetch = (input, init) => {
    const url = input instanceof Request ? input.url : String(input);
    return realFetch(url.endsWith("libpg-query.wasm") ? wasmUrl : input, init);
  };
  const redirect = globalThis.fetch;
  try {
    const m = await import("@libpg-query/parser");
    await m.loadModule();
    return m;
  } finally {
    // Leave alone any fetch wrapper someone else installed meanwhile.
    if (globalThis.fetch === redirect) globalThis.fetch = realFetch;
  }
}

function loadParser(): Promise<PgParser | null> {
  parser ??= loadWithBundledWasm().catch(() => null); // lint is a nicety; without the wasm the editor still works
  return parser;
}

/** Real Postgres syntax errors for `sql`, as editor diagnostics. */
export async function lintSql(sql: string): Promise<Diagnostic[]> {
  if (!sql.trim()) return [];
  const pg = await loadParser();
  if (!pg) return [];
  try {
    await pg.parse(sql);
    return [];
  } catch (e) {
    const pos = (e as { sqlDetails?: { cursorPosition?: number } }).sqlDetails?.cursorPosition;
    if (pos === undefined || pos < 0) return [];
    const at = Math.min(pos, sql.length);
    const from = at === sql.length ? Math.max(0, at - 1) : at;
    const word = /^\w+/.exec(sql.slice(from))?.[0].length ?? 1;
    return [{ from, to: Math.min(sql.length, from + word), severity: "error", message: (e as Error).message }];
  }
}

export const sqlLint: Extension = linter((view) => lintSql(view.state.doc.toString()), { delay: 600 });

/** The selection if it has text, else the statement under the cursor, else the whole document. */
export function sqlToRun(state: EditorState): string {
  const doc = state.doc.toString();
  const { from, to, head } = state.selection.main;
  const selected = doc.slice(from, to).trim();
  if (selected) return selected;

  const statements: { from: number; to: number }[] = [];
  (ensureSyntaxTree(state, state.doc.length, 200) ?? syntaxTree(state)).iterate({
    enter(n) {
      if (n.name !== "Statement") return;
      statements.push({ from: n.from, to: n.to });
      return false;
    },
  });
  if (statements.length === 0) return doc;
  const hit =
    statements.find((st) => st.from <= head && head <= st.to) ??
    [...statements].reverse().find((st) => st.to < head) ??
    statements[0]!;
  return doc.slice(hit.from, hit.to).trim() || doc;
}
