import { useRef, useEffect } from "react";
import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { sql } from "@codemirror/lang-sql";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { tags as t } from "@lezer/highlight";

/* Monochrome "ink" syntax theme: hierarchy is carried by weight, brightness
   and italics instead of hue, matching the dashboard's black & white design. */
const inkHighlight = HighlightStyle.define([
  { tag: t.keyword, color: "#f4f4f4", fontWeight: "600" },
  { tag: [t.string, t.special(t.string)], color: "#bdbdbd", fontStyle: "italic" },
  { tag: t.comment, color: "#5c5c5c", fontStyle: "italic" },
  { tag: [t.number, t.bool, t.null], color: "#e8e8e8" },
  { tag: [t.operator, t.punctuation], color: "#8f8f8f" },
  { tag: [t.typeName, t.className], color: "#dcdcdc" },
  { tag: [t.function(t.variableName), t.propertyName], color: "#cfcfcf" },
]);

const inkTheme = EditorView.theme(
  {
    "&": { color: "#e6e6e6" },
    ".cm-cursor, .cm-dropCursor": { borderLeftColor: "#ffffff" },
    ".cm-activeLine": { backgroundColor: "rgb(255 255 255 / 0.04)" },
    ".cm-activeLineGutter": { backgroundColor: "rgb(255 255 255 / 0.06)" },
    "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection":
      { backgroundColor: "rgb(255 255 255 / 0.18) !important" },
    ".cm-gutters": { color: "#5c5c5c" },
  },
  { dark: true }
);

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
      inkTheme,
      syntaxHighlighting(inkHighlight),
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
