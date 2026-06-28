import { useRef, useEffect } from "react";
import { Box } from "@chakra-ui/react";
import { EditorView, basicSetup } from "codemirror";
import { Decoration, type DecorationSet } from "@codemirror/view";
import {
  EditorState,
  EditorSelection,
  StateField,
  StateEffect,
  Annotation,
  Transaction,
  type Extension,
  type SelectionRange,
  type TransactionSpec,
} from "@codemirror/state";
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
  // !important throughout: CodeMirror's base theme only knows light vs dark via
  // the darkTheme facet, which we never set (colors flip through CSS vars on the
  // `.dark` class instead). So its `&light` defaults for gutter text and the
  // active-line bands always apply and out-specify a plain rule. Forcing the var
  // wins regardless, in both modes.
  ".cm-activeLine": { backgroundColor: "var(--syn-active-line) !important" },
  ".cm-activeLineGutter": { backgroundColor: "var(--syn-active-line) !important" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection":
    { backgroundColor: "var(--syn-selection) !important" },
  ".cm-gutters": { color: "var(--c-muted-foreground) !important" },
  // The locked scaffold lines: real code lines, just tinted so they read as
  // fixed and the caret never lands in them.
  ".cm-readonly-line": { backgroundColor: "var(--syn-readonly-line)" },
});

export interface Frame {
  header?: string;
  footer?: string;
}

/* The frame's header and footer live in the document as real lines so they get
   syntax highlighting and line numbers, but they're locked from editing and the
   caret is kept out of them. The document is laid out as

       <header>\n <body> \n<footer>

   and these helpers are the single source of truth for where the locked regions
   end and the editable body begins. The change filter, the selection filter,
   and the value/onChange plumbing all measure off them. The separating newlines
   count as part of the locked prefix/suffix so the body always keeps its own
   line. */
export function regionLengths(frame: Frame | null) {
  const prefixLen = frame?.header != null ? frame.header.length + 1 : 0;
  const suffixLen = frame?.footer != null ? frame.footer.length + 1 : 0;
  return { prefixLen, suffixLen };
}

export function composeDoc(frame: Frame | null, body: string) {
  const header = frame?.header != null ? frame.header + "\n" : "";
  const footer = frame?.footer != null ? "\n" + frame.footer : "";
  return header + body + footer;
}

export function extractBody(frame: Frame | null, doc: string) {
  const { prefixLen, suffixLen } = regionLengths(frame);
  return doc.slice(prefixLen, doc.length - suffixLen);
}

/* The char ranges CodeMirror must leave untouched, as the flat [from, to, …]
   list its change filter expects. */
export function protectedRanges(frame: Frame | null, docLen: number): number[] {
  const { prefixLen, suffixLen } = regionLengths(frame);
  const ranges: number[] = [];
  if (prefixLen) ranges.push(0, prefixLen);
  if (suffixLen) ranges.push(docLen - suffixLen, docLen);
  return ranges;
}

const setFrame = StateEffect.define<Frame | null>();
// Tags our own rewrites of the scaffold (when the header/footer prop changes) so
// the change filter lets them through instead of treating them as edits.
const reframe = Annotation.define<boolean>();

const frameField = StateField.define<Frame | null>({
  create: () => null,
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setFrame)) return e.value;
    return value;
  },
});

function clampSelection(
  sel: EditorSelection,
  lo: number,
  hi: number
): EditorSelection {
  const clamp = (n: number) => Math.min(Math.max(n, lo), hi);
  const ranges = sel.ranges.map((r: SelectionRange) =>
    EditorSelection.range(clamp(r.anchor), clamp(r.head))
  );
  return EditorSelection.create(ranges, sel.mainIndex);
}

/* Locks the scaffold: drops edits that fall inside the header/footer and pulls
   the caret/selection back into the body if it strays. */
const lockScaffold: Extension = [
  EditorState.changeFilter.of((tr) => {
    if (tr.annotation(reframe)) return true; // our own scaffold rewrite
    const ranges = protectedRanges(
      tr.startState.field(frameField),
      tr.startState.doc.length
    );
    // Returning the protected ranges drops changes inside them; a zero-width
    // insertion sitting exactly on a boundary is attributed to the body side, so
    // typing at the very start or end of the body still goes through.
    return ranges.length ? ranges : true;
  }),
  EditorState.transactionFilter.of((tr) => {
    if (!tr.selection || tr.annotation(reframe)) return tr;
    const frame = tr.startState.field(frameField);
    const { prefixLen, suffixLen } = regionLengths(frame);
    if (!prefixLen && !suffixLen) return tr;
    const clamped = clampSelection(
      tr.selection,
      prefixLen,
      tr.newDoc.length - suffixLen
    );
    return clamped.eq(tr.selection)
      ? tr
      : [tr, { selection: clamped, sequential: true }];
  }),
];

/* The transaction that rewrites the scaffold in place when the header/footer
   changes: it replaces the locked prefix and suffix and moves the frame field to
   match. Kept out of undo history on purpose — the setFrame effect isn't
   invertible, so letting history revert the scaffold text alone would leave the
   field pointing at the wrong region and corrupt the extracted body. */
export function reframeSpec(
  oldFrame: Frame | null,
  next: Frame | null,
  docLen: number
): TransactionSpec {
  const old = regionLengths(oldFrame);
  return {
    changes: [
      { from: 0, to: old.prefixLen, insert: next?.header != null ? next.header + "\n" : "" },
      { from: docLen - old.suffixLen, to: docLen, insert: next?.footer != null ? "\n" + next.footer : "" },
    ],
    effects: setFrame.of(next),
    annotations: [reframe.of(true), Transaction.addToHistory.of(false)],
  };
}

const readonlyLine = Decoration.line({ class: "cm-readonly-line" });

/* Tints every line that belongs to the locked header or footer. Recomputed
   whenever the doc grows or the scaffold changes so the footer's lines stay
   marked as the body moves. */
const scaffoldLines = StateField.define<DecorationSet>({
  create: (state) => buildLineDeco(state),
  update(value, tr) {
    if (tr.docChanged || tr.effects.some((e) => e.is(setFrame)))
      return buildLineDeco(tr.state);
    return value;
  },
  provide: (f) => EditorView.decorations.from(f),
});

/* The scaffold lock as a composable extension: holds the frame, tints its
   lines, and keeps edits and the caret out of them. Exported so it can be
   exercised against a bare EditorState in tests. */
export function scaffoldExtensions(initialFrame: Frame | null): Extension {
  return [frameField.init(() => initialFrame), scaffoldLines, lockScaffold];
}

function buildLineDeco(state: EditorState): DecorationSet {
  const frame = state.field(frameField);
  if (!frame) return Decoration.none;
  const { prefixLen, suffixLen } = regionLengths(frame);
  const docLen = state.doc.length;
  const deco = [] as ReturnType<typeof readonlyLine.range>[];
  const mark = (from: number, to: number) => {
    let pos = from;
    for (;;) {
      const line = state.doc.lineAt(pos);
      deco.push(readonlyLine.range(line.from));
      if (line.to >= to) break;
      pos = line.to + 1;
    }
  };
  // Header chars are [0, header.length); the trailing newline at header.length
  // sits on the header's last line, so stop there and the body line stays clean.
  if (prefixLen) mark(0, prefixLen - 1);
  // Footer chars start one past the separating newline.
  if (suffixLen) mark(docLen - suffixLen + 1, docLen);
  return Decoration.set(deco, true);
}

interface CodeEditorProps {
  value: string;
  onChange: (value: string) => void;
  language?: "sql" | "javascript" | "text";
  placeholder?: string;
  minHeight?: string;
  readOnly?: boolean;
  /** Fixed statement scaffold framing the editable body. Rendered as locked,
      syntax-highlighted lines above and below the body and kept out of
      `value`/`onChange`. */
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

  useEffect(() => {
    if (!containerRef.current) return;
    const initFrame = frame ?? null;

    const extensions = [
      basicSetup,
      brandTheme,
      syntaxHighlighting(brandHighlight),
      scaffoldExtensions(initFrame),
      EditorView.theme({
        "&": { minHeight, backgroundColor: "transparent" },
        ".cm-content": { fontFamily: "var(--font-mono)", fontSize: "13px" },
        ".cm-scroller": { overflow: "auto" },
      }),
      EditorView.updateListener.of((update) => {
        if (!update.docChanged) return;
        // Compare the body before and after: a reframe mutates the doc (header
        // text) without touching the body, and shouldn't fire onChange.
        const before = extractBody(
          update.startState.field(frameField),
          update.startState.doc.toString()
        );
        const after = extractBody(
          update.state.field(frameField),
          update.state.doc.toString()
        );
        if (before !== after) onChangeRef.current(after);
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

    const state = EditorState.create({
      doc: composeDoc(initFrame, value),
      extensions,
    });
    const view = new EditorView({ state, parent: containerRef.current });
    viewRef.current = view;

    return () => {
      view.destroy();
      viewRef.current = null;
    };
    // Only re-create on language/readOnly change, not value. The frame and value
    // flow in through the effects below without tearing down the editor.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [language, readOnly, minHeight]);

  // Rewrite the scaffold in place when the frame prop changes (e.g. the RPC
  // header recomputing from form fields). The body and the caret stay put.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const oldFrame = view.state.field(frameField);
    const next = frame ?? null;
    if (
      (oldFrame?.header ?? null) === (next?.header ?? null) &&
      (oldFrame?.footer ?? null) === (next?.footer ?? null)
    )
      return;
    view.dispatch(reframeSpec(oldFrame, next, view.state.doc.length));
  }, [frame?.header, frame?.footer]);

  // Sync external value changes into the body region only.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const frameNow = view.state.field(frameField);
    const doc = view.state.doc.toString();
    if (extractBody(frameNow, doc) === value) return;
    const { prefixLen, suffixLen } = regionLengths(frameNow);
    view.dispatch({
      changes: { from: prefixLen, to: doc.length - suffixLen, insert: value },
      annotations: Transaction.addToHistory.of(false),
    });
  }, [value]);

  return <Box ref={containerRef} overflow="hidden" borderRadius="lg" />;
}
