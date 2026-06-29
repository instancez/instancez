import { describe, it, expect } from "vitest";
import { isValidRedirectOrigin } from "./redirectOrigin";

describe("isValidRedirectOrigin", () => {
  it("accepts absolute http(s) origins", () => {
    expect(isValidRedirectOrigin("https://app.example.com")).toBe(true);
    expect(isValidRedirectOrigin("http://localhost:3000")).toBe(true);
  });

  it("ignores the path component (still an origin)", () => {
    expect(isValidRedirectOrigin("https://app.example.com/auth/callback")).toBe(true);
  });

  it("rejects relative and protocol-relative entries", () => {
    expect(isValidRedirectOrigin("/callback")).toBe(false);
    expect(isValidRedirectOrigin("//evil.com")).toBe(false);
  });

  it("rejects a host without a scheme", () => {
    expect(isValidRedirectOrigin("app.example.com")).toBe(false);
  });

  it("rejects non-http(s) schemes", () => {
    expect(isValidRedirectOrigin("ftp://files.example.com")).toBe(false);
    expect(isValidRedirectOrigin("javascript:alert(1)")).toBe(false);
  });

  it("rejects backslash/NUL parser-differential values", () => {
    expect(isValidRedirectOrigin("https://app.example.com\\@evil.com")).toBe(false);
    expect(isValidRedirectOrigin("https://app.example.com\x00")).toBe(false);
  });

  it("rejects empty input", () => {
    expect(isValidRedirectOrigin("")).toBe(false);
  });
});
