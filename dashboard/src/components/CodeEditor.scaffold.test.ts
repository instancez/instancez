import { describe, it, expect, vi } from "vitest";
import { EditorState } from "@codemirror/state";
import { history, undo } from "@codemirror/commands";
import {
  regionLengths,
  composeDoc,
  extractBody,
  extractHole,
  protectedRanges,
  scaffoldExtensions,
  reframeSpec,
  liveHoleRange,
  type Frame,
} from "./CodeEditor";

/**
 * The frame's header/footer are real, locked document lines. These tests drive
 * the lock against a bare EditorState (no DOM), since the interesting behaviour
 * (what survives the change filter, where the caret lands) is pure state.
 */

const frame: Frame = { header: "HDR(", footer: ")" };
const body = "x = 1";
// doc: "HDR(\nx = 1\n)" with header [0,4), body [5,10), footer (10,12)
const make = (f: Frame | null, b: string) =>
  EditorState.create({ doc: composeDoc(f, b), extensions: scaffoldExtensions(f) });

describe("scaffold region math", () => {
  it("round-trips body through compose/extract", () => {
    expect(composeDoc(frame, body)).toBe("HDR(\nx = 1\n)");
    expect(extractBody(frame, composeDoc(frame, body))).toBe(body);
  });

  it("counts the separating newlines as part of the locked regions", () => {
    expect(regionLengths(frame)).toEqual({ prefixLen: 5, suffixLen: 2 });
    expect(regionLengths(null)).toEqual({ prefixLen: 0, suffixLen: 0 });
    expect(regionLengths({ header: "H" })).toEqual({ prefixLen: 2, suffixLen: 0 });
  });

  it("derives the protected char ranges", () => {
    expect(protectedRanges(frame, 12)).toEqual([0, 5, 10, 12]);
    expect(protectedRanges(null, 3)).toEqual([]);
  });
});

describe("scaffold edit lock", () => {
  it("drops edits inside the header", () => {
    const next = make(frame, body).update({ changes: { from: 1, to: 2 } }).state;
    expect(next.doc.toString()).toBe("HDR(\nx = 1\n)");
  });

  it("drops edits inside the footer", () => {
    const next = make(frame, body).update({ changes: { from: 10, to: 11 } }).state;
    expect(next.doc.toString()).toBe("HDR(\nx = 1\n)");
  });

  it("blocks backspace that would eat into the header", () => {
    // Caret at body start (5); backspace removes the separating newline at [4,5].
    const next = make(frame, body).update({ changes: { from: 4, to: 5 } }).state;
    expect(next.doc.toString()).toBe("HDR(\nx = 1\n)");
  });

  it("allows ordinary edits in the body", () => {
    const next = make(frame, body).update({ changes: { from: 6, insert: "Y" } }).state;
    expect(extractBody(frame, next.doc.toString())).toBe("xY = 1");
  });

  it("keeps an insertion at the very start of the body", () => {
    const next = make(frame, body).update({ changes: { from: 5, insert: "Z" } }).state;
    expect(extractBody(frame, next.doc.toString())).toBe("Zx = 1");
  });

  it("keeps an insertion at the very end of the body", () => {
    const next = make(frame, body).update({ changes: { from: 10, insert: "Z" } }).state;
    expect(extractBody(frame, next.doc.toString())).toBe("x = 1Z");
  });

  it("lets the body be typed into when it starts empty", () => {
    // doc: "H\n\nF", body is the empty middle line at position 2.
    const next = make({ header: "H", footer: "F" }, "").update({
      changes: { from: 2, insert: "q" },
    }).state;
    expect(extractBody({ header: "H", footer: "F" }, next.doc.toString())).toBe("q");
  });

  it("does not lock anything without a frame", () => {
    const next = make(null, "abc").update({ changes: { from: 0, to: 1 } }).state;
    expect(next.doc.toString()).toBe("bc");
  });
});

describe("scaffold reframe + undo", () => {
  const prefixLen = regionLengths(frame).prefixLen;

  it("reframes the scaffold while keeping the body", () => {
    const next: Frame = { header: "HDR2(", footer: ")" };
    let state = make(frame, "x");
    state = state.update(reframeSpec(frame, next, state.doc.length)).state;
    expect(state.doc.toString()).toBe("HDR2(\nx\n)");
    expect(extractBody(next, state.doc.toString())).toBe("x");
  });

  it("does not let undo revert the scaffold and desync the body", () => {
    // A longer replacement header so a stale frame field would slice the body at
    // the wrong offset — without the fix, the body extracts as "" here, not "abc".
    const next: Frame = { header: "LONGHDR(", footer: ")" };
    let state = EditorState.create({
      doc: composeDoc(frame, "abc"),
      extensions: [history(), scaffoldExtensions(frame)],
    });
    // A body edit (recorded in history)...
    state = state.update({ changes: { from: prefixLen, insert: "Z" } }).state;
    // ...then a reframe (kept out of history).
    state = state.update(reframeSpec(frame, next, state.doc.length)).state;
    expect(state.doc.toString()).toBe("LONGHDR(\nZabc\n)");
    // Undo should peel back the body edit, not the scaffold rewrite, so the
    // frame field still matches the doc prefix and the body extracts cleanly.
    undo({
      state,
      dispatch: (tr) => {
        state = tr.state;
      },
    });
    expect(state.doc.toString()).toBe("LONGHDR(\nabc\n)");
    expect(extractBody(next, state.doc.toString())).toBe("abc");
  });
});

/**
 * `frame.hole` cuts one extra editable island into the header, for a value
 * that should be typed in place (e.g. RPC RETURNS <type>) instead of through
 * a separate form field. `hole.before`/`hole.after` replace `header`; the
 * hole and the body are both editable, everything else stays locked.
 */
describe("hole region math", () => {
  const hframe: Frame = {
    hole: { before: "RETURNS ", after: " LANGUAGE sql", value: "int", onChange: vi.fn() },
    footer: "END",
  };
  // doc: "RETURNS int LANGUAGE sql\nbody\nEND"
  //       [0      8)[8 11)[11          24)   25  29  30    33

  it("composes before+value+after, then body and footer, around the hole", () => {
    expect(composeDoc(hframe, "body")).toBe("RETURNS int LANGUAGE sql\nbody\nEND");
  });

  it("extracts the hole value from the doc", () => {
    expect(extractHole(hframe, composeDoc(hframe, "body"))).toBe("int");
  });

  it("extracts null when there is no hole", () => {
    expect(extractHole({ header: "H" }, composeDoc({ header: "H" }, "body"))).toBeNull();
  });

  it("extracts the body around a hole same as without one", () => {
    expect(extractBody(hframe, composeDoc(hframe, "body"))).toBe("body");
  });

  it("locks everything except the hole and the body", () => {
    const doc = composeDoc(hframe, "body");
    expect(protectedRanges(hframe, doc.length)).toEqual([0, 8, 11, 25, 29, 33]);
  });
});

describe("hole edit lock", () => {
  const hframe: Frame = {
    hole: { before: "RETURNS ", after: " AS", value: "int", onChange: vi.fn() },
    footer: "X",
  };
  const body = "b";
  const make2 = () =>
    EditorState.create({ doc: composeDoc(hframe, body), extensions: scaffoldExtensions(hframe) });
  // doc: "RETURNS int AS\nb\nX"; before [0,8) hole [8,11) after [11,14) \n body [15,16) \n footer

  it("drops edits inside the locked 'before' text", () => {
    const next = make2().update({ changes: { from: 1, to: 2 } }).state;
    expect(next.doc.toString()).toBe(composeDoc(hframe, body));
  });

  it("drops edits inside the locked 'after' text", () => {
    const next = make2().update({ changes: { from: 12, to: 13 } }).state;
    expect(next.doc.toString()).toBe(composeDoc(hframe, body));
  });

  it("allows typing at the end of the hole and grows it", () => {
    const next = make2().update({ changes: { from: 11, insert: "8" } }).state;
    expect(next.doc.toString()).toBe("RETURNS int8 AS\nb\nX");
  });

  it("allows typing at the start of the hole", () => {
    const next = make2().update({ changes: { from: 8, insert: "u" } }).state;
    expect(next.doc.toString()).toBe("RETURNS uint AS\nb\nX");
  });

  it("keeps locking correct after several keystrokes inside the hole (no reframe in between)", () => {
    // Real typing is one transaction per keystroke; the hole's tracked range
    // must grow live rather than trusting the (by-then stale) frame prop.
    let state = make2();
    state = state.update({ changes: { from: 11, insert: "1" } }).state; // "int1"
    state = state.update({ changes: { from: 12, insert: "2" } }).state; // "int12"
    state = state.update({ changes: { from: 13, insert: "8" } }).state; // "int128"
    expect(state.doc.toString()).toBe("RETURNS int128 AS\nb\nX");
    // The body boundary must have shifted with the hole's growth: an edit
    // right after "AS\n" must land in the body, not get silently dropped.
    const bodyStart = state.doc.toString().indexOf("\n") + 1;
    state = state.update({ changes: { from: bodyStart, insert: "Z" } }).state;
    expect(state.doc.toString()).toBe("RETURNS int128 AS\nZb\nX");
    // And the locked 'after' text must still be locked at its new offset.
    const afterStart = "RETURNS int128".length;
    state = state.update({ changes: { from: afterStart + 1, to: afterStart + 2 } }).state;
    expect(state.doc.toString()).toBe("RETURNS int128 AS\nZb\nX");
  });
});

describe("hole reframe", () => {
  it("adds a hole where there was none, keeping the body", () => {
    const old: Frame = { header: "FUNC() RETURNS void AS", footer: "END" };
    const next: Frame = {
      hole: { before: "FUNC() RETURNS ", after: " AS", value: "void", onChange: vi.fn() },
      footer: "END",
    };
    let state = EditorState.create({ doc: composeDoc(old, "body"), extensions: scaffoldExtensions(old) });
    state = state.update(reframeSpec(old, next, state.doc.length)).state;
    expect(state.doc.toString()).toBe("FUNC() RETURNS void AS\nbody\nEND");
    expect(extractBody(next, state.doc.toString())).toBe("body");
    expect(extractHole(next, state.doc.toString())).toBe("void");
  });

  it("removes a hole, keeping the body", () => {
    const old: Frame = {
      hole: { before: "FUNC() RETURNS ", after: " AS", value: "table(id int)", onChange: vi.fn() },
      footer: "END",
    };
    const next: Frame = { header: "FUNC() RETURNS text AS", footer: "END" };
    let state = EditorState.create({ doc: composeDoc(old, "body"), extensions: scaffoldExtensions(old) });
    state = state.update(reframeSpec(old, next, state.doc.length)).state;
    expect(state.doc.toString()).toBe("FUNC() RETURNS text AS\nbody\nEND");
    expect(extractBody(next, state.doc.toString())).toBe("body");
  });

  it("keeps the hole's own text untouched while the surrounding header reflows", () => {
    const old: Frame = {
      hole: { before: "FN(", after: ")RET", value: "int", onChange: vi.fn() },
      footer: "END",
    };
    const next: Frame = {
      hole: { before: "FN(arg,", after: ")RET", value: "int", onChange: vi.fn() },
      footer: "END",
    };
    let state = EditorState.create({ doc: composeDoc(old, "body"), extensions: scaffoldExtensions(old) });
    state = state.update(reframeSpec(old, next, state.doc.length)).state;
    expect(state.doc.toString()).toBe("FN(arg,int)RET\nbody\nEND");
    expect(extractHole(next, state.doc.toString())).toBe("int");
  });

  it("preserves hole growth typed since the last reframe when the header reflows again", () => {
    const old: Frame = {
      hole: { before: "FN(", after: ")RET", value: "int", onChange: vi.fn() },
      footer: "END",
    };
    let state = EditorState.create({ doc: composeDoc(old, "body"), extensions: scaffoldExtensions(old) });
    // Type into the hole without a reframe: "int" -> "int8"
    const holeEnd = "FN(int".length;
    state = state.update({ changes: { from: holeEnd, insert: "8" } }).state;
    expect(state.doc.toString()).toBe("FN(int8)RET\nbody\nEND");

    // Now an unrelated reframe (e.g. an arg name field elsewhere changed) fires
    // with a frame.hole.value that still says "int" (React hasn't caught up
    // yet in this hand-built scenario) — the live "int8" on the doc must win.
    const next: Frame = {
      hole: { before: "FN(arg,", after: ")RET", value: "int", onChange: vi.fn() },
      footer: "END",
    };
    state = state.update(
      reframeSpec(old, next, state.doc.length, liveHoleRange(state))
    ).state;
    expect(state.doc.toString()).toBe("FN(arg,int8)RET\nbody\nEND");
    // The tracked hole range itself must reflect the preserved "int8", not
    // `next.hole.value`'s stale "int" — otherwise later typing/locking drifts
    // by the length difference from here on.
    const hole = liveHoleRange(state)!;
    expect(state.doc.sliceString(hole.from, hole.to)).toBe("int8");
  });
});

describe("empty hole", () => {
  it("accepts a keystroke into a zero-width hole", () => {
    // A hole can legitimately start (or be typed down to) empty. The change
    // filter's two adjacent locked ranges meet exactly at that point, but
    // since both are half-open, a zero-width insertion there overlaps
    // neither — it isn't stranded between two locks.
    const empty: Frame = {
      hole: { before: "RETURNS ", after: " AS", value: "", onChange: vi.fn() },
      footer: "X",
    };
    const state = EditorState.create({ doc: composeDoc(empty, "b"), extensions: scaffoldExtensions(empty) });
    const next = state.update({ changes: { from: 8, insert: "t" } }).state;
    expect(next.doc.toString()).toBe("RETURNS t AS\nb\nX");
  });
});

describe("hole caret fence", () => {
  const hframe: Frame = {
    hole: { before: "RETURNS ", after: " AS", value: "int", onChange: vi.fn() },
    footer: "X",
  };
  const make3 = () =>
    EditorState.create({ doc: composeDoc(hframe, "b"), extensions: scaffoldExtensions(hframe) });

  it("allows the caret inside the hole", () => {
    const next = make3().update({ selection: { anchor: 9 } }).state;
    expect(next.selection.main.head).toBe(9);
  });

  it("pulls a caret in the locked 'after' text to the nearer hole/body edge", () => {
    const next = make3().update({ selection: { anchor: 12 } }).state; // inside " AS"
    expect([11, 15]).toContain(next.selection.main.head);
  });

  it("still allows the caret in the body", () => {
    const next = make3().update({ selection: { anchor: 15 } }).state;
    expect(next.selection.main.head).toBe(15);
  });

  it("keeps a select-all inside one window instead of spanning the locked gap", () => {
    // Both ends must resolve to the SAME window (here, the one nearest the
    // anchor at 0) — a naive per-point clamp would put anchor in the hole and
    // head in the body, selecting across the locked "AS" text in between.
    const doc = composeDoc(hframe, "b");
    const next = make3().update({ selection: { anchor: 0, head: doc.length } }).state;
    expect(next.selection.main.from).toBe(8);
    expect(next.selection.main.to).toBe(11);
  });

  it("does not let a select-all-and-retype delete the body", () => {
    const doc = composeDoc(hframe, "b");
    const selected = make3().update({ selection: { anchor: 0, head: doc.length } }).state;
    const retyped = selected.update(selected.replaceSelection("X")).state;
    // Not extractBody(hframe, ...): hframe is now stale (its hole.value still
    // says "int"), so check the actual doc directly.
    expect(retyped.doc.toString()).toBe("RETURNS X AS\nb\nX");
  });
});

describe("scaffold caret fence", () => {
  it("pulls a caret in the header down to the body start", () => {
    const next = make(frame, body).update({ selection: { anchor: 2 } }).state;
    expect(next.selection.main.head).toBe(5);
  });

  it("pulls a caret in the footer up to the body end", () => {
    const next = make(frame, body).update({ selection: { anchor: 11 } }).state;
    expect(next.selection.main.head).toBe(10);
  });

  it("clamps a selection spanning into both regions to the body", () => {
    const next = make(frame, body).update({ selection: { anchor: 1, head: 12 } }).state;
    expect(next.selection.main.from).toBe(5);
    expect(next.selection.main.to).toBe(10);
  });

  it("leaves a selection already inside the body untouched", () => {
    const next = make(frame, body).update({ selection: { anchor: 6, head: 8 } }).state;
    expect(next.selection.main.from).toBe(6);
    expect(next.selection.main.to).toBe(8);
  });
});
