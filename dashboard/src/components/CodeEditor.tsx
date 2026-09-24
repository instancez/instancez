import { useRef, useEffect } from "react";
import { Box } from "@chakra-ui/react";
import { EditorView, basicSetup } from "codemirror";
import { Decoration, keymap, type DecorationSet } from "@codemirror/view";
import {
  EditorState,
  EditorSelection,
  StateField,
  StateEffect,
  Annotation,
  Transaction,
  Prec,
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
  // The autocomplete popup. Without a darkTheme facet CodeMirror paints it with
  // its light defaults, so force our vars for both modes.
  ".cm-tooltip": {
    backgroundColor: "var(--c-surface)",
    color: "var(--c-foreground)",
    border: "1px solid var(--c-border)",
    borderRadius: "6px",
  },
  ".cm-tooltip.cm-tooltip-autocomplete > ul": {
    fontFamily: "var(--font-mono)",
    fontSize: "12px",
  },
  ".cm-tooltip-autocomplete ul li[aria-selected]": {
    backgroundColor: "var(--syn-selection)",
    color: "var(--c-foreground)",
  },
  ".cm-completionDetail": { color: "var(--c-muted-foreground)" },
  ".cm-completionMatchedText": {
    color: "var(--syn-keyword)",
    textDecoration: "none",
    fontWeight: "600",
  },
});

/** A single editable line cut into the header (e.g. RPC `RETURNS <type>`); `header` is ignored when set. */
export interface FrameHole {
  before: string;
  after: string;
  value: string;
  onChange: (value: string) => void;
}

export interface Frame {
  header?: string;
  footer?: string;
  hole?: FrameHole;
}

// Doc layout: `<header>\n<body>\n<footer>`, or with a hole, `<hole.before><hole.value><hole.after>\n<body>\n<footer>`.
export function regionLengths(frame: Frame | null) {
  const prefixLen = frame?.hole
    ? frame.hole.before.length + frame.hole.value.length + frame.hole.after.length + 1
    : frame?.header != null
      ? frame.header.length + 1
      : 0;
  const suffixLen = frame?.footer != null ? frame.footer.length + 1 : 0;
  return { prefixLen, suffixLen };
}

/** The hole's [from, to) range in a doc freshly composed from `frame` (stale once the user types; see `holeField`). */
export function holeRange(frame: Frame | null): { from: number; to: number } | null {
  if (!frame?.hole) return null;
  const from = frame.hole.before.length;
  return { from, to: from + frame.hole.value.length };
}

export function composeDoc(frame: Frame | null, body: string) {
  const header = frame?.hole
    ? frame.hole.before + frame.hole.value + frame.hole.after
    : (frame?.header ?? null);
  const headerPart = header != null ? header + "\n" : "";
  const footer = frame?.footer != null ? "\n" + frame.footer : "";
  return headerPart + body + footer;
}

export function extractBody(frame: Frame | null, doc: string) {
  const { prefixLen, suffixLen } = regionLengths(frame);
  return doc.slice(prefixLen, doc.length - suffixLen);
}

export function extractHole(frame: Frame | null, doc: string): string | null {
  const range = holeRange(frame);
  return range ? doc.slice(range.from, range.to) : null;
}

// The locked [from, to, …] ranges around a hole (if any) and the body, shared by `protectedRanges` and `liveProtectedRanges`.
function lockedRanges(
  hole: { from: number; to: number } | null,
  bodyFrom: number,
  suffixLen: number,
  docLen: number
): number[] {
  const ranges: number[] = [];
  if (hole) {
    if (hole.from > 0) ranges.push(0, hole.from);
    if (hole.to < bodyFrom) ranges.push(hole.to, bodyFrom);
  } else if (bodyFrom > 0) {
    ranges.push(0, bodyFrom);
  }
  if (suffixLen) ranges.push(docLen - suffixLen, docLen);
  return ranges;
}

export function protectedRanges(frame: Frame | null, docLen: number): number[] {
  const { prefixLen, suffixLen } = regionLengths(frame);
  return lockedRanges(holeRange(frame), prefixLen, suffixLen, docLen);
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

// The hole's live [from, to) range, kept accurate across ordinary typing by mapping through each transaction's changes.
const setHole = StateEffect.define<{ from: number; to: number } | null>();

const holeField = StateField.define<{ from: number; to: number } | null>({
  create: () => null,
  update(value, tr) {
    for (const e of tr.effects) if (e.is(setHole)) return e.value;
    if (!value) return value;
    return { from: tr.changes.mapPos(value.from, -1), to: tr.changes.mapPos(value.to, 1) };
  },
});

/** The live hole range tracked by a running editor's state, or null. */
export function liveHoleRange(state: EditorState): { from: number; to: number } | null {
  return state.field(holeField, false) ?? null;
}

/** The body's live [from, to) bounds, accounting for the hole's current live length. */
function liveBodyBounds(state: EditorState): { from: number; to: number } {
  const frame = state.field(frameField);
  const hole = state.field(holeField);
  const to = state.doc.length - regionLengths(frame).suffixLen;
  if (hole && frame?.hole) return { from: hole.to + frame.hole.after.length + 1, to };
  return { from: regionLengths(frame).prefixLen, to };
}

function liveProtectedRanges(state: EditorState): number[] {
  const frame = state.field(frameField);
  const body = liveBodyBounds(state);
  const suffixLen = regionLengths(frame).suffixLen;
  return lockedRanges(state.field(holeField), body.from, suffixLen, state.doc.length);
}

// Clamps a selection into a single editable window (hole or body); both ends resolve to whichever window the anchor is nearest, so a range can't straddle the locked gap between them.
function clampToWindows(
  sel: EditorSelection,
  windows: Array<[number, number]>
): EditorSelection {
  const windowFor = (n: number): [number, number] => {
    for (const w of windows) if (n >= w[0] && n <= w[1]) return w;
    return windows.reduce((best, w) => {
      const edge = n < w[0] ? w[0] : w[1];
      const bestEdge = n < best[0] ? best[0] : best[1];
      return Math.abs(edge - n) < Math.abs(bestEdge - n) ? w : best;
    });
  };
  const ranges = sel.ranges.map((r: SelectionRange) => {
    const [lo, hi] = windowFor(r.anchor);
    const clamp = (n: number) => Math.min(Math.max(n, lo), hi);
    return EditorSelection.range(clamp(r.anchor), clamp(r.head));
  });
  return EditorSelection.create(ranges, sel.mainIndex);
}

// Locks the scaffold: drops edits inside the header/footer and fences the caret into the body or hole.
const lockScaffold: Extension = [
  EditorState.changeFilter.of((tr) => {
    if (tr.annotation(reframe)) return true; // our own scaffold rewrite
    const ranges = liveProtectedRanges(tr.startState);
    return ranges.length ? ranges : true;
  }),
  EditorState.transactionFilter.of((tr) => {
    if (!tr.selection || tr.annotation(reframe)) return tr;
    const frame = tr.state.field(frameField);
    if (!frame) return tr;
    const body = liveBodyBounds(tr.state);
    const hole = tr.state.field(holeField);
    const windows: Array<[number, number]> = [];
    if (hole && frame.hole) windows.push([hole.from, hole.to]);
    windows.push([body.from, body.to]);
    const clamped = clampToWindows(tr.selection, windows);
    return clamped.eq(tr.selection)
      ? tr
      : [tr, { selection: clamped, sequential: true }];
  }),
];

// Rewrites the scaffold in place when the header/footer/hole shape changes, preserving body and hole content; kept out of undo history since the setFrame/setHole effects aren't invertible.
export function reframeSpec(
  oldFrame: Frame | null,
  next: Frame | null,
  docLen: number,
  liveHole: { from: number; to: number } | null = holeRange(oldFrame)
): TransactionSpec {
  const changes: { from: number; to: number; insert: string }[] = [];
  let newHole: { from: number; to: number } | null = null;

  if (oldFrame?.hole && liveHole) {
    const oldAfterEnd = liveHole.to + oldFrame.hole.after.length + 1;
    if (next?.hole) {
      // hole -> hole: rewrite only the locked before/after text; the hole's
      // own (possibly since-grown) content is left in place, untouched.
      changes.push({ from: 0, to: liveHole.from, insert: next.hole.before });
      changes.push({ from: liveHole.to, to: oldAfterEnd, insert: next.hole.after + "\n" });
      newHole = {
        from: next.hole.before.length,
        to: next.hole.before.length + (liveHole.to - liveHole.from),
      };
    } else {
      // hole -> none: nothing on-doc to preserve, collapse in one shot.
      changes.push({
        from: 0,
        to: oldAfterEnd,
        insert: next?.header != null ? next.header + "\n" : "",
      });
    }
  } else {
    const oldPrefixLen = regionLengths(oldFrame).prefixLen;
    if (next?.hole) {
      // none -> hole: fresh insert, nothing on-doc to preserve, so the value
      // prop's length is exactly right here.
      changes.push({
        from: 0,
        to: oldPrefixLen,
        insert: next.hole.before + next.hole.value + next.hole.after + "\n",
      });
      newHole = holeRange(next);
    } else {
      changes.push({
        from: 0,
        to: oldPrefixLen,
        insert: next?.header != null ? next.header + "\n" : "",
      });
    }
  }

  const oldSuffixLen = regionLengths(oldFrame).suffixLen;
  changes.push({
    from: docLen - oldSuffixLen,
    to: docLen,
    insert: next?.footer != null ? "\n" + next.footer : "",
  });

  return {
    changes,
    effects: [setFrame.of(next), setHole.of(newHole)],
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
  return [
    frameField.init(() => initialFrame),
    holeField.init(() => holeRange(initialFrame)),
    scaffoldLines,
    lockScaffold,
  ];
}

function buildLineDeco(state: EditorState): DecorationSet {
  const frame = state.field(frameField);
  if (!frame) return Decoration.none;
  // ponytail: a hole's line is tinted like the rest of the locked header; add a lighter sub-tint if that reads as misleadingly locked.
  const body = liveBodyBounds(state);
  const suffixLen = regionLengths(frame).suffixLen;
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
  // Header chars are [0, body.from); the trailing newline sits on the header's
  // last line, so stop there and the body line stays clean.
  if (body.from > 0) mark(0, body.from - 1);
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
  /** Fired on Cmd/Ctrl+Enter, e.g. to run the current query. */
  onSubmit?: () => void;
  /** Table → column names, fed to SQL autocompletion as known identifiers. */
  sqlSchema?: Record<string, string[]>;
}

export function CodeEditor({
  value,
  onChange,
  language = "sql",
  placeholder = "",
  minHeight = "120px",
  readOnly = false,
  frame,
  onSubmit,
  sqlSchema,
}: CodeEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const onSubmitRef = useRef(onSubmit);
  onSubmitRef.current = onSubmit;

  useEffect(() => {
    if (!containerRef.current) return;
    const initFrame = frame ?? null;

    const extensions = [
      // Prec.highest so Mod-Enter wins over basicSetup, which binds it to
      // insertBlankLine.
      Prec.highest(
        keymap.of([
          {
            key: "Mod-Enter",
            run: () => {
              if (!onSubmitRef.current) return false; // fall through to basicSetup
              onSubmitRef.current();
              return true;
            },
          },
        ])
      ),
      basicSetup,
      EditorView.lineWrapping,
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
        // text) without touching the body, and shouldn't fire onChange. Uses
        // the live bounds, not the (possibly hole-stale) frame-only ones.
        const beforeBounds = liveBodyBounds(update.startState);
        const afterBounds = liveBodyBounds(update.state);
        const before = update.startState.doc.sliceString(beforeBounds.from, beforeBounds.to);
        const after = update.state.doc.sliceString(afterBounds.from, afterBounds.to);
        if (before !== after) onChangeRef.current(after);

        const afterFrame = update.state.field(frameField);
        const holeAfter = update.state.field(holeField);
        if (afterFrame?.hole && holeAfter) {
          const holeBefore = update.startState.field(holeField);
          const afterHoleText = update.state.doc.sliceString(holeAfter.from, holeAfter.to);
          const beforeHoleText = holeBefore
            ? update.startState.doc.sliceString(holeBefore.from, holeBefore.to)
            : null;
          if (afterHoleText !== beforeHoleText) afterFrame.hole.onChange(afterHoleText);
        }
      }),
    ];

    if (language === "sql")
      extensions.push(sql(sqlSchema ? { schema: sqlSchema, upperCaseKeywords: false } : undefined));
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
  }, [language, readOnly, minHeight, sqlSchema]);

  // Rewrite the scaffold in place when the frame's structure changes (header,
  // footer, or the hole's before/after text — e.g. the RPC header recomputing
  // from form fields). The body, the hole's own content, and both carets stay
  // put; a hole's live growth since the last reframe is passed through so it
  // survives (see reframeSpec).
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const oldFrame = view.state.field(frameField);
    const next = frame ?? null;
    const sameHeaderFooter =
      (oldFrame?.header ?? null) === (next?.header ?? null) &&
      (oldFrame?.footer ?? null) === (next?.footer ?? null);
    const sameHole =
      !oldFrame?.hole === !next?.hole &&
      (oldFrame?.hole?.before ?? null) === (next?.hole?.before ?? null) &&
      (oldFrame?.hole?.after ?? null) === (next?.hole?.after ?? null);
    if (sameHeaderFooter && sameHole) return;
    view.dispatch(
      reframeSpec(oldFrame, next, view.state.doc.length, liveHoleRange(view.state))
    );
  }, [frame?.header, frame?.footer, frame?.hole?.before, frame?.hole?.after, !!frame?.hole]);

  // Sync external value changes into the body region only.
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const body = liveBodyBounds(view.state);
    const doc = view.state.doc.toString();
    if (doc.slice(body.from, body.to) === value) return;
    view.dispatch({
      changes: { from: body.from, to: body.to, insert: value },
      annotations: Transaction.addToHistory.of(false),
    });
  }, [value]);

  // Sync external hole-value changes (e.g. picking a different preset while
  // still in custom mode) into the hole region only.
  useEffect(() => {
    const view = viewRef.current;
    if (!view || !frame?.hole) return;
    const hole = liveHoleRange(view.state);
    if (!hole) return;
    const doc = view.state.doc.toString();
    if (doc.slice(hole.from, hole.to) === frame.hole.value) return;
    view.dispatch({
      changes: { from: hole.from, to: hole.to, insert: frame.hole.value },
      annotations: Transaction.addToHistory.of(false),
    });
  }, [frame?.hole?.value]);

  return <Box ref={containerRef} overflow="hidden" borderRadius="lg" />;
}
