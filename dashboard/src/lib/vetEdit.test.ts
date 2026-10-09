import { describe, expect, it } from "vitest";
import { applyVetEdit } from "./vetEdit";
import type { Config } from "./types";

const cfg = (over: object = {}) =>
  ({ version: 1, tables: { a: { rls_enabled: false } }, auth: null, server: { cors: { origins: ["x", "null", "y"] }, max_limit: -1 }, ...over }) as unknown as Config;

describe("applyVetEdit", () => {
  it("sets a value and keeps false and zero", () => {
    expect(applyVetEdit(cfg(), { path: ["tables", "a", "rls_enabled"], value: true })!.tables["a"]!.rls_enabled).toBe(true);
    const off = applyVetEdit(cfg({ auth: { allow_anonymous: true } }), { path: ["auth", "allow_anonymous"], value: false })!;
    expect(off.auth).toEqual({ allow_anonymous: false });
    expect(applyVetEdit(cfg(), { path: ["server", "max_limit"], value: 0 })!.server.max_limit).toBe(0);
  });

  it("does not mutate its input", () => {
    const input = cfg();
    const snapshot = JSON.stringify(input);
    applyVetEdit(input, { path: ["tables", "a", "rls_enabled"], value: true });
    applyVetEdit(input, { path: ["server", "cors", "origins", 1], value: null, remove: true });
    expect(JSON.stringify(input)).toBe(snapshot);
  });

  it("creates missing objects, replacing a null parent", () => {
    const out = applyVetEdit(cfg(), { path: ["auth", "email", "verify_email"], value: true })!;
    expect(out.auth).toEqual({ email: { verify_email: true } });
  });

  it("creates an array when the next segment is an index", () => {
    const out = applyVetEdit(cfg(), { path: ["list", 0], value: "v" })!;
    expect((out as unknown as { list: unknown[] }).list).toEqual(["v"]);
  });

  it("removes an array item by index and leaves the rest in order", () => {
    expect(applyVetEdit(cfg(), { path: ["server", "cors", "origins", 1], value: "null", remove: true })!.server.cors.origins).toEqual(["x", "y"]);
  });

  it("refuses a remove whose expected value moved, so a stale index cannot delete another entry", () => {
    expect(applyVetEdit(cfg(), { path: ["server", "cors", "origins", 0], value: "null", remove: true })).toBeNull();
    expect(applyVetEdit(cfg(), { path: ["server", "cors", "origins", 7], value: "null", remove: true })).toBeNull();
  });

  it("removes one of several null entries and leaves the others for their own findings", () => {
    const two = cfg({ server: { cors: { origins: ["null", "a", "null"] } } });
    const once = applyVetEdit(two, { path: ["server", "cors", "origins", 0], value: "null", remove: true })!;
    expect(once.server.cors.origins).toEqual(["a", "null"]);
    expect(applyVetEdit(once, { path: ["server", "cors", "origins", 2], value: "null", remove: true })).toBeNull();
  });

  it("removes an object key", () => {
    const out = applyVetEdit(cfg(), { path: ["server", "max_limit"], value: null, remove: true })!;
    expect("max_limit" in out.server).toBe(false);
  });

  it("is a no-op for an empty path", () => {
    expect(applyVetEdit(cfg(), { path: [], value: 1 })).toEqual(cfg());
  });

  it("removing from a missing parent does not throw", () => {
    expect(() => applyVetEdit(cfg(), { path: ["nope", "x"], value: null, remove: true })).not.toThrow();
  });
});
