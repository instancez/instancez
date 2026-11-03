import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { ReadOnlyBanner } from "./ReadOnlyBanner";
import { EditModeBanner } from "./EditModeBanner";

const base = {
  status: "ok" as const,
  config_source: "instancez.yaml",
  running: { checksum: "a", applied_at: "" },
  source: { checksum: "", last_seen_at: "" },
  last_error: null,
  dashboard_mode: "readwrite" as const,
};

describe("ReadOnlyBanner", () => {
  it("explains that include configs are edited as files", () => {
    renderWithChakra(<ReadOnlyBanner status={{ ...base, read_only: true }} />);
    expect(screen.getByRole("status")).toHaveTextContent(/include:/);
    expect(screen.getByRole("status")).toHaveTextContent(/edit the YAML files/i);
  });

  it("renders nothing when the config is writable or the status is missing", () => {
    renderWithChakra(<ReadOnlyBanner status={{ ...base, read_only: false }} />);
    renderWithChakra(<ReadOnlyBanner status={null} />);
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("replaces the live-edit warning, which would be false for include configs", () => {
    renderWithChakra(<EditModeBanner status={{ ...base, read_only: true }} />);
    expect(screen.queryByRole("status")).toBeNull();
  });
});
