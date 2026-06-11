import { useRef, useEffect } from "react";
import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { sql } from "@codemirror/lang-sql";
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
});

interface CodeEditorProps {
  value: string;
  onChange: (value: string) => void;
  language?: "sql" | "text";
  placeholder?: string;
  minHeight?: string;
  readOnly?: boolean;
}

export function CodeEditor({
  value,
  onChange,
  language = "sql",
  placeholder = "",
  minHeight = "120px",
  readOnly = false,
}: CodeEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useEffect(() => {
    if (!containerRef.current) return;

    const extensions = [
      basicSetup,
      brandTheme,
      syntaxHighlighting(brandHighlight),
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
    if (readOnly) extensions.push(EditorState.readOnly.of(true));
    if (placeholder) {
      extensions.push(
        EditorView.contentAttributes.of({ "aria-placeholder": placeholder })
      );
    }

    const state = EditorState.create({ doc: value, extensions });
    const view = new EditorView({ state, parent: containerRef.current });
    viewRef.current = view;

    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // Only re-create on language/readOnly change, not value
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [language, readOnly, minHeight]);

  // Sync external value changes
  useEffect(() => {
    const view = viewRef.current;
    if (view && view.state.doc.toString() !== value) {
      view.dispatch({
        changes: { from: 0, to: view.state.doc.length, insert: value },
      });
    }
  }, [value]);

  return <div ref={containerRef} className="overflow-hidden" />;
}
