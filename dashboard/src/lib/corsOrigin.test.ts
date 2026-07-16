import { describe, it, expect } from "vitest";
import { isValidCorsOrigin } from "./corsOrigin";

describe("isValidCorsOrigin", () => {
  it("accepts the wildcard", () => {
    expect(isValidCorsOrigin("*")).toBe(true);
  });

  it("accepts an absolute http(s) origin", () => {
    expect(isValidCorsOrigin("https://app.example.com")).toBe(true);
    expect(isValidCorsOrigin("http://localhost:5173")).toBe(true);
  });

  it("rejects a bare host or non-http(s) scheme", () => {
    expect(isValidCorsOrigin("app.example.com")).toBe(false);
    expect(isValidCorsOrigin("ftp://app.example.com")).toBe(false);
  });

  it("rejects a URL with a path, query, or hash", () => {
    expect(isValidCorsOrigin("https://app.example.com/callback")).toBe(false);
    expect(isValidCorsOrigin("https://app.example.com?x=1")).toBe(false);
    expect(isValidCorsOrigin("https://app.example.com#x")).toBe(false);
  });

  it("rejects empty and malformed values", () => {
    expect(isValidCorsOrigin("")).toBe(false);
    expect(isValidCorsOrigin("not a url")).toBe(false);
  });
});
