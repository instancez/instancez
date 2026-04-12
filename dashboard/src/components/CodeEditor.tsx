import { useRef, useEffect } from "react";
import { EditorView, basicSetup } from "codemirror";
import { EditorState } from "@codemirror/state";
import { sql } from "@codemirror/lang-sql";
import { oneDark } from "@codemirror/theme-one-dark";

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
      oneDark,
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

  return <div ref={containerRef} className="rounded-lg overflow-hidden" />;
}
