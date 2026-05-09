import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { DriftBanner } from "./DriftBanner";

describe("DriftBanner", () => {
  it("renders nothing when status is ok", () => {
    const { container } = render(
      <DriftBanner status={{
        status: "ok", config_source: "f",
        running: { checksum: "a", applied_at: "" },
        source: { checksum: "", last_seen_at: "" },
        last_error: null,
        dashboard_mode: "readwrite",
      }} />
    );
    expect(container.firstChild).toBeNull();
  });

  it("shows the source and error when drifted", () => {
    render(
      <DriftBanner status={{
        status: "drift",
        config_source: "s3://bucket/ultrabase.yaml",
        running: { checksum: "a", applied_at: "2026-05-08T12:00:00Z" },
        source: { checksum: "b", last_seen_at: "2026-05-08T12:01:00Z" },
        last_error: "ERROR: column \"foo\" cannot be cast",
        dashboard_mode: "readwrite",
      }} />
    );
    expect(screen.getByText(/Configuration drift/i)).toBeTruthy();
    expect(screen.getByText(/s3:\/\/bucket\/ultrabase.yaml/)).toBeTruthy();
    expect(screen.getByText(/cannot be cast/)).toBeTruthy();
  });
});
