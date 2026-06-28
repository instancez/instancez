import { describe, it, expect } from "vitest";
import { EditorState } from "@codemirror/state";
import { history, undo } from "@codemirror/commands";
import {
  regionLengths,
  composeDoc,
  extractBody,
  protectedRanges,
  scaffoldExtensions,
  reframeSpec,
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
