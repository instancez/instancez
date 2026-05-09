import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
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
    const { container } = render(
      <EditModeBanner status={{ ...base, dashboard_mode: "readonly" }} />
    );
    expect(container.firstChild).toBeNull();
  });

  it("renders nothing when disabled", () => {
    const { container } = render(
      <EditModeBanner status={{ ...base, dashboard_mode: "disabled" }} />
    );
    expect(container.firstChild).toBeNull();
  });

  it("shows live edit warning when readwrite", () => {
    render(<EditModeBanner status={{ ...base, dashboard_mode: "readwrite" }} />);
    expect(screen.getByText(/Live edit mode/i)).toBeTruthy();
    expect(screen.getByText(/s3:\/\/bucket\/key/)).toBeTruthy();
  });
});
