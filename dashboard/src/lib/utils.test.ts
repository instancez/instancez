import { describe, it, expect } from "vitest";
import {
  formatBytes,
  formatNumber,
  timeAgo,
  POSTGRES_TYPES,
  SQL_DEFAULTS,
  RLS_OPERATIONS,
  CORS_METHODS,
} from "./utils";

describe("formatBytes", () => {
  it("formats 0 bytes", () => {
    expect(formatBytes(0)).toBe("0 B");
  });

  it("formats bytes", () => {
    expect(formatBytes(500)).toBe("500 B");
  });

  it("formats kilobytes", () => {
    expect(formatBytes(1024)).toBe("1 KB");
    expect(formatBytes(1536)).toBe("1.5 KB");
  });

  it("formats megabytes", () => {
    expect(formatBytes(1048576)).toBe("1 MB");
    expect(formatBytes(5242880)).toBe("5 MB");
  });

  it("formats gigabytes", () => {
    expect(formatBytes(1073741824)).toBe("1 GB");
  });
});

describe("formatNumber", () => {
  it("formats small numbers", () => {
    expect(formatNumber(42)).toBe("42");
  });

  it("formats thousands with locale separator", () => {
    const result = formatNumber(1234567);
    // Locale-dependent, but should contain digits
    expect(result).toContain("1");
    expect(result).toContain("234");
    expect(result).toContain("567");
  });

  it("formats zero", () => {
    expect(formatNumber(0)).toBe("0");
  });
});

describe("timeAgo", () => {
  it("formats seconds ago", () => {
    const now = new Date();
    now.setSeconds(now.getSeconds() - 30);
    expect(timeAgo(now.toISOString())).toBe("30s ago");
  });

  it("formats minutes ago", () => {
    const now = new Date();
    now.setMinutes(now.getMinutes() - 5);
    expect(timeAgo(now.toISOString())).toBe("5m ago");
  });

  it("formats hours ago", () => {
    const now = new Date();
    now.setHours(now.getHours() - 3);
    expect(timeAgo(now.toISOString())).toBe("3h ago");
  });

  it("formats days ago", () => {
    const now = new Date();
    now.setDate(now.getDate() - 2);
    expect(timeAgo(now.toISOString())).toBe("2d ago");
  });
});

describe("constants", () => {
  it("POSTGRES_TYPES has expected types", () => {
    expect(POSTGRES_TYPES).toContain("text");
    expect(POSTGRES_TYPES).toContain("integer");
    expect(POSTGRES_TYPES).toContain("boolean");
    expect(POSTGRES_TYPES).toContain("uuid");
    expect(POSTGRES_TYPES).toContain("jsonb");
    expect(POSTGRES_TYPES).toContain("timestamptz");
    expect(POSTGRES_TYPES.length).toBeGreaterThan(10);
  });

  it("SQL_DEFAULTS has common defaults", () => {
    expect(SQL_DEFAULTS).toContain("now()");
    expect(SQL_DEFAULTS).toContain("true");
    expect(SQL_DEFAULTS).toContain("false");
  });

  it("RLS_OPERATIONS has CRUD operations", () => {
    expect(RLS_OPERATIONS).toEqual(["select", "insert", "update", "delete"]);
  });

  it("CORS_METHODS has standard methods", () => {
    expect(CORS_METHODS).toContain("GET");
    expect(CORS_METHODS).toContain("OPTIONS");
  });
});
