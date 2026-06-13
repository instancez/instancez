import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { EditModeBanner } from "./EditModeBanner";

const base = {
  status: "ok" as const,
  config_source: "s3://bucket/key",
  running: { checksum: "a", applied_at: "" },
  source: { checksum: "", last_seen_at: "" },
  last_error: null,
};

describe("EditModeBanner", () => {
  it("renders nothing when readonly", () => {
    renderWithChakra(
      <EditModeBanner status={{ ...base, dashboard_mode: "readonly" }} />
    );
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("renders nothing when disabled", () => {
    renderWithChakra(
      <EditModeBanner status={{ ...base, dashboard_mode: "disabled" }} />
    );
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("shows live edit warning when readwrite", () => {
    renderWithChakra(<EditModeBanner status={{ ...base, dashboard_mode: "readwrite" }} />);
    expect(screen.getByText(/Live edit mode/i)).toBeTruthy();
    expect(screen.getByText(/s3:\/\/bucket\/key/)).toBeTruthy();
  });
});
