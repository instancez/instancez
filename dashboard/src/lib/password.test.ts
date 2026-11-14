import { describe, it, expect } from "vitest";
import { generatePassword, passwordStrength } from "./password";

describe("generatePassword", () => {
  it("has the requested length and only unambiguous characters", () => {
    const pw = generatePassword(24);
    expect(pw).toHaveLength(24);
    expect(pw).toMatch(/^[A-HJ-NP-Za-km-z2-9]+$/);
  });
  it("defaults to 16 and differs between calls", () => {
    expect(generatePassword()).toHaveLength(16);
    expect(generatePassword()).not.toBe(generatePassword());
  });
  it("is always at least good by its own meter", () => {
    for (let i = 0; i < 50; i++) expect(["good", "strong"]).toContain(passwordStrength(generatePassword()).level);
  });
  it("returns an empty string for length 0", () => {
    expect(generatePassword(0)).toBe("");
  });
});

describe("passwordStrength", () => {
  it.each([
    ["", "empty"],
    ["abc", "short"],
    ["abcde", "short"],
    ["abcdef", "weak"],
    ["abcdefghij", "weak"],
    ["abcdefGH12", "good"],
    ["abcdefGH12!xyz", "strong"],
    ["aaaaaaaaaaaaaaaaaaaa", "weak"],
  ])("%j is %s", (pw, level) => {
    expect(passwordStrength(pw).level).toBe(level);
  });
});
