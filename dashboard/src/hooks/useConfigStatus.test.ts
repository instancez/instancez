import { describe, expect, it, vi, afterEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { useConfigStatus } from "./useConfigStatus";
import * as api from "../api/client";

describe("useConfigStatus", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns the current status and re-polls", async () => {
    const get = vi.spyOn(api, "getConfigStatus")
      .mockResolvedValueOnce({
        status: "ok", config_source: "f", running: { checksum: "a", applied_at: "" },
        source: { checksum: "", last_seen_at: "" }, last_error: null, dashboard_mode: "readwrite",
      })
      .mockResolvedValueOnce({
        status: "drift", config_source: "f", running: { checksum: "a", applied_at: "" },
        source: { checksum: "b", last_seen_at: "" }, last_error: "boom", dashboard_mode: "readwrite",
      })
      .mockResolvedValue({
        status: "drift", config_source: "f", running: { checksum: "a", applied_at: "" },
        source: { checksum: "b", last_seen_at: "" }, last_error: "boom", dashboard_mode: "readwrite",
      });

    const { result, unmount } = renderHook(() => useConfigStatus(50));
    await waitFor(() => expect(result.current.data?.status).toBe("ok"));

    await waitFor(() => expect(result.current.data?.status).toBe("drift"));
    expect(get.mock.calls.length).toBeGreaterThanOrEqual(2);
    unmount();
  });
});
