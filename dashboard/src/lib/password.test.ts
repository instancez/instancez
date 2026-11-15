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
  it("meter rates a mixed 16-character password strong and a single-class one weak", () => {
    expect(passwordStrength("Abcdefgh23456789").level).toBe("strong");
    expect(passwordStrength("abcdefghjkmnpqrs").level).toBe("weak");
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
