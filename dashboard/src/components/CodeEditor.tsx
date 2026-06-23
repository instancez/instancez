import { useRef, useEffect } from "react";
import { Box } from "@chakra-ui/react";
import { EditorView, basicSetup } from "codemirror";
import { Decoration, WidgetType, type DecorationSet } from "@codemirror/view";
import { EditorState, StateField, StateEffect } from "@codemirror/state";
import { sql } from "@codemirror/lang-sql";
import { javascript } from "@codemirror/lang-javascript";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags as t } from "@lezer/highlight";

/* Syntax colors are CSS variables flipped by the `.dark` class (see
   index.css), so a single highlight style adapts to both color modes. */
const brandHighlight = HighlightStyle.define([
  { tag: t.keyword, color: "var(--syn-keyword)", fontWeight: "600" },
  { tag: [t.string, t.special(t.string)], color: "var(--syn-string)" },
  { tag: t.comment, color: "var(--syn-comment)", fontStyle: "italic" },
  { tag: [t.number, t.bool, t.null], color: "var(--syn-number)" },
  { tag: [t.operator, t.punctuation], color: "var(--syn-operator)" },
  { tag: [t.typeName, t.className], color: "var(--syn-type)" },
  { tag: [t.function(t.variableName), t.propertyName], color: "var(--syn-function)" },
]);

const brandTheme = EditorView.theme({
  "&": { color: "var(--c-foreground)" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--syn-cursor)" },
  ".cm-activeLine": { backgroundColor: "var(--syn-active-line)" },
  ".cm-activeLineGutter": { backgroundColor: "var(--syn-active-line)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection":
    { backgroundColor: "var(--syn-selection) !important" },
  ".cm-gutters": { color: "var(--c-muted-foreground)" },
  ".cm-scaffold": {
    padding: "8px 14px",
    fontFamily: "var(--font-mono)",
    fontSize: "11px",
    lineHeight: "1.5",
    whiteSpace: "pre-wrap",
    color: "var(--c-muted-foreground)",
    userSelect: "text",
  },
  ".cm-scaffold-header": { borderBottom: "1px solid var(--c-border)" },
  ".cm-scaffold-footer": { borderTop: "1px solid var(--c-border)" },
});

/* A fixed bit of the surrounding statement (the CREATE … header, the closing
   paren) shown as a block above or below the editable body. It's a widget
   rather than document text, so the caret can't land in it and it never reaches
   onChange. The user reads it but only edits the body. */
class ScaffoldWidget extends WidgetType {
  constructor(
    readonly text: string,
    readonly side: "header" | "footer"
  ) {
    super();
  }
  eq(other: ScaffoldWidget) {
    return other.text === this.text && other.side === this.side;
  }
  toDOM() {
    const el = document.createElement("div");
    el.className = `cm-scaffold cm-scaffold-${this.side}`;
    // Belt and suspenders: the widget is already outside the document, and
    // this stops the caret from landing in it so the text reads as fixed.
    el.setAttribute("contenteditable", "false");
    el.textContent = this.text;
    return el;
  }
}

interface Frame {
  header?: string;
  footer?: string;
}

const setFrame = StateEffect.define<Frame | null>();

/* Holds the current scaffold text and turns it into the two block widgets.
   The footer rides at doc.length, so the decorations recompute on every doc
   change to keep it pinned below the body as the body grows. Pushing new
   scaffold text through an effect (rather than rebuilding the editor) keeps
   undo history and the caret intact when the RPC header recomputes from a
   form field. */
const frameField = StateField.define<Frame | null>({
  create() {
    return null;
  },
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setFrame)) return e.value;
    return value;
  },
  provide: (field) =>
    EditorView.decorations.compute([field, "doc"], (state): DecorationSet => {
      const frame = state.field(field);
      if (!frame) return Decoration.none;
      const ranges = [];
      if (frame.header != null) {
        ranges.push(
          Decoration.widget({
            widget: new ScaffoldWidget(frame.header, "header"),
            side: -1,
            block: true,
          }).range(0)
        );
      }
      if (frame.footer != null) {
        ranges.push(
          Decoration.widget({
            widget: new ScaffoldWidget(frame.footer, "footer"),
            side: 1,
            block: true,
          }).range(state.doc.length)
        );
      }
      return Decoration.set(ranges);
    }),
});

interface CodeEditorProps {
  value: string;
  onChange: (value: string) => void;
  language?: "sql" | "javascript" | "text";
  placeholder?: string;
  minHeight?: string;
  readOnly?: boolean;
  /** Fixed statement scaffold framing the editable body, rendered inside the
      editor but kept out of the document (and out of `value`/`onChange`). */
  frame?: Frame;
}

export function CodeEditor({
  value,
  onChange,
  language = "sql",
  placeholder = "",
  minHeight = "120px",
  readOnly = false,
  frame,
}: CodeEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  // Read on mount only; later changes flow through the frameField effect below.
  const initialFrameRef = useRef(frame);

  useEffect(() => {
    if (!containerRef.current) return;

    const extensions = [
      basicSetup,
      brandTheme,
      syntaxHighlighting(brandHighlight),
      frameField,
      EditorView.theme({
        "&": { minHeight, backgroundColor: "transparent" },
        ".cm-content": { fontFamily: "var(--font-mono)", fontSize: "13px" },
        ".cm-scroller": { overflow: "auto" },
      }),
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          onChangeRef.current(update.state.doc.toString());
        }
      }),
    ];

    if (language === "sql") extensions.push(sql());
    if (language === "javascript") extensions.push(javascript());
    if (readOnly) extensions.push(EditorState.readOnly.of(true));
    if (placeholder) {
      extensions.push(
        EditorView.contentAttributes.of({ "aria-placeholder": placeholder })
      );
    }

    const state = EditorState.create({ doc: value, extensions });
    const view = new EditorView({ state, parent: containerRef.current });
    if (initialFrameRef.current) {
      view.dispatch({ effects: setFrame.of(initialFrameRef.current) });
    }
    viewRef.current = view;

    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // Only re-create on language/readOnly change, not value
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [language, readOnly, minHeight]);

  // Re-frame in place when the scaffold text changes (e.g. the RPC header
  // recomputing from the Definition fields) without tearing down the editor.
  useEffect(() => {
    const view = viewRef.current;
    if (view) view.dispatch({ effects: setFrame.of(frame ?? null) });
  }, [frame?.header, frame?.footer]);

  // Sync external value changes
  useEffect(() => {
    const view = viewRef.current;
    if (view && view.state.doc.toString() !== value) {
      view.dispatch({
        changes: { from: 0, to: view.state.doc.length, insert: value },
      });
    }
  }, [value]);

  return <Box ref={containerRef} overflow="hidden" borderRadius="lg" />;
}
