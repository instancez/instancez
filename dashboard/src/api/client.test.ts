import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  getConfig,
  getConfigStatus,
  getStats,
  getStatus,
  putConfig,
  validateAdminKey,
} from "./client";

const mockFetch = vi.fn();

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("fetch", mockFetch);
  sessionStorage.setItem("ultrabase_admin_key", "test-key");
});

afterEach(() => {
  vi.unstubAllGlobals();
  sessionStorage.clear();
});

function jsonResponse(data: unknown, status = 200) {
  return Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(data),
  });
}

describe("getConfig", () => {
  it("calls /api/_admin/config with auth header", async () => {
    const config = { version: 1, project: { name: "Test" }, _checksum: "sha256:abc" };
    mockFetch.mockReturnValueOnce(jsonResponse(config));

    const result = await getConfig();

    expect(mockFetch).toHaveBeenCalledWith(
      "/api/_admin/config",
      expect.objectContaining({
        headers: expect.objectContaining({
          Authorization: "Bearer test-key",
        }),
      })
    );
    expect(result).toEqual(config);
  });

  it("throws when no admin key is set", async () => {
    sessionStorage.clear();
    await expect(getConfig()).rejects.toThrow("No admin key configured");
  });
});

describe("putConfig", () => {
  it("sends PUT with If-Match header", async () => {
    mockFetch.mockReturnValueOnce(jsonResponse({ message: "Config saved" }));

    const config = { version: 1 } as any;
    await putConfig(config, "sha256:abc123");

    expect(mockFetch).toHaveBeenCalledWith(
      "/api/_admin/config",
      expect.objectContaining({
        method: "PUT",
        headers: expect.objectContaining({
          "If-Match": "sha256:abc123",
        }),
        body: JSON.stringify(config),
      })
    );
  });
});

describe("getStats", () => {
  it("fetches stats", async () => {
    const stats = {
      tables: { todos: { row_count: 42 } },
      storage: {},
    };
    mockFetch.mockReturnValueOnce(jsonResponse(stats));

    const result = await getStats();
    expect(result).toEqual(stats);
  });
});

describe("getStatus", () => {
  it("fetches status", async () => {
    const status = { status: "ok", database: "connected" };
    mockFetch.mockReturnValueOnce(jsonResponse(status));

    const result = await getStatus();
    expect(result).toEqual(status);
  });
});

describe("validateAdminKey", () => {
  it("returns true for valid key", async () => {
    mockFetch.mockReturnValueOnce(Promise.resolve({ ok: true }));
    const result = await validateAdminKey("valid-key");
    expect(result).toBe(true);
    expect(mockFetch).toHaveBeenCalledWith(
      "/api/_admin/status",
      expect.objectContaining({
        headers: { Authorization: "Bearer valid-key" },
      })
    );
  });

  it("returns false for invalid key", async () => {
    mockFetch.mockReturnValueOnce(Promise.resolve({ ok: false }));
    const result = await validateAdminKey("bad-key");
    expect(result).toBe(false);
  });

  it("returns false on network error", async () => {
    mockFetch.mockRejectedValueOnce(new Error("Network error"));
    const result = await validateAdminKey("any-key");
    expect(result).toBe(false);
  });
});

describe("getConfigStatus", () => {
  beforeEach(() => {
    sessionStorage.setItem("ultrabase_admin_key", "test-key");
  });

  afterEach(() => {
    sessionStorage.clear();
    vi.unstubAllGlobals();
  });

  it("fetches and returns the status payload", async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({
        status: "drift",
        config_source: "s3://bucket/key",
        running: { checksum: "abc", applied_at: "2026-05-08T12:00:00Z" },
        source: { checksum: "def", last_seen_at: "2026-05-08T12:01:00Z" },
        last_error: "boom",
        dashboard_mode: "readwrite",
      }),
    });
    vi.stubGlobal("fetch", mockFetch);

    const got = await getConfigStatus();
    expect(got.status).toBe("drift");
    expect(got.last_error).toBe("boom");
    expect(mockFetch).toHaveBeenCalledWith(
      "/api/_admin/config/status",
      expect.objectContaining({
        headers: expect.objectContaining({ Authorization: "Bearer test-key" }),
      }),
    );
  });
});

describe("error handling", () => {
  it("clears session on 401", async () => {
    // Mock reload to prevent jsdom errors
    const reloadMock = vi.fn();
    Object.defineProperty(window, "location", {
      value: { reload: reloadMock },
      writable: true,
    });

    mockFetch.mockReturnValueOnce(
      Promise.resolve({
        ok: false,
        status: 401,
        json: () => Promise.resolve({}),
      })
    );

    await expect(getStatus()).rejects.toThrow("Unauthorized");
    expect(sessionStorage.getItem("ultrabase_admin_key")).toBeNull();
  });

  it("throws with error body on non-401 errors", async () => {
    mockFetch.mockReturnValueOnce(
      Promise.resolve({
        ok: false,
        status: 500,
        json: () => Promise.resolve({ message: "Internal server error" }),
      })
    );

    await expect(getStatus()).rejects.toThrow("Internal server error");
  });
});
