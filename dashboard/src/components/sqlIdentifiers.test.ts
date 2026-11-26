// @vitest-environment jsdom
import { describe, it, expect } from "vitest";
import { EditorView } from "codemirror";
import { sql } from "@codemirror/lang-sql";
import { CompletionContext } from "@codemirror/autocomplete";
import { EditorState } from "@codemirror/state";
import { buildLookup, functionCompletion, richSchema, identifiersFromConfig, sqlIdentifierHighlight, type SqlIdentifiers } from "./sqlIdentifiers";

const ids: SqlIdentifiers = { tables: ["todos"], columns: ["title", "todos"], functions: ["add_todo"] };

function render(doc: string, identifiers: SqlIdentifiers = ids) {
  const parent = document.createElement("div");
  document.body.appendChild(parent);
  const view = new EditorView({ doc, parent, extensions: [sql(), sqlIdentifierHighlight(identifiers)] });
  const texts = (cls: string) => [...parent.querySelectorAll(`.${cls}`)].map((e) => e.textContent);
  return { view, texts };
}

describe("identifiersFromConfig", () => {
  it("collects tables, their columns and rpc names", () => {
    const out = identifiersFromConfig({
      tables: { todos: { fields: [{ name: "id" }, { name: "title" }] } },
      rpc: { add_todo: { args: [{ name: "title", type: "text" }], returns: { type: "uuid" } } },
    } as never);
    expect(out).toEqual({
      tables: ["todos"],
      columns: ["id", "title"],
      functions: ["add_todo"],
      signatures: { add_todo: "(title text) → uuid" },
    });
  });

  it("copes with missing or empty sections", () => {
    expect(identifiersFromConfig({} as never)).toEqual({ tables: [], columns: [], functions: [], signatures: {} });
    expect(identifiersFromConfig({ tables: { t: {} }, rpc: {} } as never).columns).toEqual([]);
  });
});

describe("buildLookup", () => {
  it("lets a table beat a column of the same name and folds case", () => {
    const m = buildLookup(ids);
    expect(m.get("todos")).toBe("table");
    expect(m.get("title")).toBe("column");
    expect(m.get("add_todo")).toBe("function");
    expect(m.has("Title")).toBe(false);
  });
});

describe("sqlIdentifierHighlight", () => {
  it("marks tables, columns and functions by kind", () => {
    const { texts } = render("select title, add_todo(1) from todos");
    expect(texts("cm-id-table")).toEqual(["todos"]);
    expect(texts("cm-id-column")).toEqual(["title"]);
    expect(texts("cm-id-function")).toEqual(["add_todo"]);
  });

  it("matches case-insensitively and through quotes", () => {
    const { texts } = render('select "title" from TODOS');
    expect(texts("cm-id-table")).toEqual(["TODOS"]);
    expect(texts("cm-id-column")).toEqual(['"title"']);
  });

  it("leaves strings, comments and unknown names alone", () => {
    const { texts } = render("select 'todos', other -- todos\nfrom nothing");
    expect(texts("cm-id-table")).toEqual([]);
    expect(texts("cm-id-column")).toEqual([]);
  });

  it("does nothing for empty identifier sets and empty docs", () => {
    const { texts } = render("", { tables: [], columns: [], functions: [] });
    expect(texts("cm-id-table")).toEqual([]);
  });

  it("updates marks when the document changes", () => {
    const { view, texts } = render("select 1");
    view.dispatch({ changes: { from: 8, insert: " from todos" } });
    expect(texts("cm-id-table")).toEqual(["todos"]);
  });
});

describe("functionCompletion", () => {
  const source = functionCompletion({ ...ids, signatures: { add_todo: "(title text) → uuid" } });
  const run = (doc: string, explicit = false) => {
    const state = EditorState.create({ doc, extensions: [sql()] });
    return source(new CompletionContext(state, doc.length, explicit)) as ReturnType<typeof source>;
  };

  it("offers rpc functions with their signature while typing a word", () => {
    const r = run("select add");
    expect(r?.from).toBe(7);
    expect(r?.options.map((o) => [o.label, o.type, o.detail])).toEqual([["add_todo", "function", "(title text) → uuid"]]);
  });

  it("stays quiet with nothing typed unless explicitly asked", () => {
    expect(run("select ")).toBeNull();
    expect(run("select ", true)?.options).toHaveLength(1);
  });

  it("stays quiet inside strings and comments", () => {
    expect(run("select 'add")).toBeNull();
    expect(run("-- add")).toBeNull();
  });

  it("offers nothing when the project has no functions", () => {
    const empty = functionCompletion({ tables: [], columns: [], functions: [] });
    const state = EditorState.create({ doc: "add", extensions: [sql()] });
    expect(empty(new CompletionContext(state, 3, true))).toBeNull();
  });

  it("inserts name() with the cursor inside the parens", () => {
    const state = EditorState.create({ doc: "select add", extensions: [sql()] });
    const view = new EditorView({ state, parent: document.body });
    const apply = run("select add")!.options[0]!.apply as (v: EditorView, c: unknown, f: number, t: number) => void;
    apply(view, null, 7, 10);
    expect(view.state.doc.toString()).toBe("select add_todo()");
    expect(view.state.selection.main.head).toBe(16);
  });
});

describe("richSchema", () => {
  const schema = richSchema({
    tables: {
      todos: {
        fields: [
          { name: "id", type: "uuid", primary_key: true, required: true },
          { name: "title", type: "text", unique: true },
          { name: "note", type: "text" },
        ],
      },
      empty: { fields: [] },
    },
  } as never) as Record<string, { self: { label: string; type: string; detail: string }; children: { label: string; type: string; detail: string }[] }>;

  it("labels tables with a kind and column count", () => {
    expect(schema.todos!.self).toEqual({ label: "todos", type: "class", detail: "3 columns" });
    expect(schema.empty!.self.detail).toBe("0 columns");
  });

  it("gives columns a type and flags, omitting absent flags", () => {
    expect(schema.todos!.children).toEqual([
      { label: "id", type: "property", detail: "uuid · pk · required" },
      { label: "title", type: "property", detail: "text · unique" },
      { label: "note", type: "property", detail: "text" },
    ]);
  });

  it("is empty when there are no tables", () => {
    expect(richSchema({} as never)).toEqual({});
  });

  it("drives lang-sql completion for tables and dotted columns", async () => {
    const lang = sql({ schema: richSchema({ tables: { todos: { fields: [{ name: "id", type: "uuid" }] } } } as never) });
    const complete = async (doc: string) => {
      const state = EditorState.create({ doc, extensions: [lang] });
      const sources = state.languageDataAt<(c: CompletionContext) => unknown>("autocomplete", doc.length);
      const results = await Promise.all(sources.map((src) => src(new CompletionContext(state, doc.length, true))));
      return results.flatMap((r) => ((r as { options?: { label: string; type?: string; detail?: string }[] } | null)?.options ?? []));
    };
    expect(await complete("select * from tod")).toContainEqual(expect.objectContaining({ label: "todos", type: "class" }));
    expect(await complete("select todos.")).toContainEqual(expect.objectContaining({ label: "id", type: "property", detail: "uuid" }));
  });
});
