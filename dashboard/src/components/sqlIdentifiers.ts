import { syntaxTree } from "@codemirror/language";
import type { Completion, CompletionContext, CompletionResult } from "@codemirror/autocomplete";
import { RangeSetBuilder, type Extension } from "@codemirror/state";
import { Decoration, ViewPlugin, type DecorationSet, type EditorView, type ViewUpdate } from "@codemirror/view";
import type { SQLNamespace } from "@codemirror/lang-sql";
import type { Config, Field } from "../lib/types";

export type IdentifierKind = "table" | "column" | "function";

export interface SqlIdentifiers {
  tables: string[];
  columns: string[];
  functions: string[];
  /** Function name -> "(arg type, ...) -> return type", shown beside the suggestion. */
  signatures?: Record<string, string>;
}

export function identifiersFromConfig(cfg: Pick<Config, "tables" | "rpc">): SqlIdentifiers {
  const tables = Object.entries(cfg.tables ?? {});
  return {
    tables: tables.map(([name]) => name),
    columns: tables.flatMap(([, t]) => (t.fields ?? []).map((f) => f.name)),
    functions: Object.keys(cfg.rpc ?? {}),
    signatures: Object.fromEntries(
      Object.entries(cfg.rpc ?? {}).map(([name, fn]) => [
        name,
        `(${(fn.args ?? []).map((a) => `${a.name} ${a.type}`).join(", ")}) → ${fn.returns?.type ?? "void"}`,
      ])
    ),
  };
}

/** Unquoted names fold to lower case in Postgres; quoted ones match exactly. */
export function buildLookup(ids: SqlIdentifiers): Map<string, IdentifierKind> {
  const m = new Map<string, IdentifierKind>();
  // Later sets win on a clash, so tables beat functions beat columns.
  for (const [names, kind] of [
    [ids.columns, "column"],
    [ids.functions, "function"],
    [ids.tables, "table"],
  ] as const)
    for (const n of names) m.set(n.toLowerCase(), kind);
  return m;
}

const marks: Record<IdentifierKind, Decoration> = {
  table: Decoration.mark({ class: "cm-id-table" }),
  column: Decoration.mark({ class: "cm-id-column" }),
  function: Decoration.mark({ class: "cm-id-function" }),
};

function decorate(view: EditorView, lookup: Map<string, IdentifierKind>): DecorationSet {
  const builder = new RangeSetBuilder<Decoration>();
  const { state } = view;
  for (const { from, to } of view.visibleRanges) {
    syntaxTree(state).iterate({
      from,
      to,
      enter(node) {
        if (node.name !== "Identifier" && node.name !== "QuotedIdentifier") return;
        const text = state.doc.sliceString(node.from, node.to).replace(/^["`]|["`]$/g, "");
        const kind = lookup.get(text.toLowerCase());
        if (kind) builder.add(node.from, node.to, marks[kind]);
      },
    });
  }
  return builder.finish();
}

/** Colors identifiers that match a known table, column or function. */
export function sqlIdentifierHighlight(ids: SqlIdentifiers): Extension {
  const lookup = buildLookup(ids);
  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet;
      constructor(view: EditorView) {
        this.decorations = decorate(view, lookup);
      }
      update(u: ViewUpdate) {
        if (u.docChanged || u.viewportChanged || syntaxTree(u.startState) !== syntaxTree(u.state))
          this.decorations = decorate(u.view, lookup);
      }
    },
    { decorations: (v) => v.decorations }
  );
}

/** Suggests rpc functions as `name()` with the cursor between the parens. */
export function functionCompletion(ids: SqlIdentifiers) {
  const options: Completion[] = ids.functions.map((name) => ({
    label: name,
    type: "function",
    detail: ids.signatures?.[name],
    apply: (view, _c, from, to) =>
      view.dispatch({
        changes: { from, to, insert: `${name}()` },
        selection: { anchor: from + name.length + 1 },
        userEvent: "input.complete",
      }),
  }));
  return (ctx: CompletionContext): CompletionResult | null => {
    const word = ctx.matchBefore(/\w*/);
    if (!word || (word.from === word.to && !ctx.explicit) || options.length === 0) return null;
    const node = syntaxTree(ctx.state).resolveInner(ctx.pos, -1).name;
    if (node === "String" || node === "LineComment" || node === "BlockComment") return null;
    return { from: word.from, options, validFor: /^\w*$/ };
  };
}

function columnDetail(f: Field): string {
  const flags = [f.primary_key && "pk", f.required && "required", f.unique && "unique"].filter(Boolean);
  return [f.type, ...flags].join(" · ");
}

/** Schema for lang-sql where tables and columns carry a kind icon and type detail. */
export function richSchema(cfg: Pick<Config, "tables">): SQLNamespace {
  return Object.fromEntries(
    Object.entries(cfg.tables ?? {}).map(([name, t]) => {
      const fields = t.fields ?? [];
      return [
        name,
        {
          self: { label: name, type: "class", detail: `${fields.length} column${fields.length === 1 ? "" : "s"}` },
          children: fields.map((f) => ({ label: f.name, type: "property", detail: columnDetail(f) })),
        },
      ];
    })
  );
}
