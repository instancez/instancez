import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { DriftBanner } from "./DriftBanner";

describe("DriftBanner", () => {
  it("renders nothing when status is ok", () => {
    renderWithChakra(
      <DriftBanner status={{
        status: "ok", config_source: "f",
        running: { checksum: "a", applied_at: "" },
        source: { checksum: "", last_seen_at: "" },
        last_error: null,
        dashboard_mode: "readwrite",
      }} />
    );
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows the source and error when drifted", () => {
    renderWithChakra(
      <DriftBanner status={{
        status: "drift",
        config_source: "s3://bucket/instancez.yaml",
        running: { checksum: "a", applied_at: "2026-05-08T12:00:00Z" },
        source: { checksum: "b", last_seen_at: "2026-05-08T12:01:00Z" },
        last_error: "ERROR: column \"foo\" cannot be cast",
        dashboard_mode: "readwrite",
      }} />
    );
    expect(screen.getByText(/Configuration drift/i)).toBeTruthy();
    expect(screen.getByText(/s3:\/\/bucket\/instancez.yaml/)).toBeTruthy();
    expect(screen.getByText(/cannot be cast/)).toBeTruthy();
  });
});
