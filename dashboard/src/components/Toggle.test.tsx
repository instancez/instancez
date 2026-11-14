import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithChakra } from "../test/helpers";
import { Toggle } from "./Toggle";

describe("Toggle", () => {
  it("bare toggle is a named switch that shows its state", () => {
    renderWithChakra(<Toggle aria-label="required" checked={false} onChange={vi.fn()} />);
    expect(screen.getByRole("switch", { name: "required" })).not.toBeChecked();
    expect(screen.getByText("Off")).toBeInTheDocument();
  });

  it("bare toggle renders On/Off following the value", () => {
    const { unmount } = renderWithChakra(<Toggle aria-label="x" checked onChange={vi.fn()} />);
    expect(screen.getByText("On")).toBeInTheDocument();
    unmount();
    renderWithChakra(<Toggle aria-label="x" checked={false} onChange={vi.fn()} />);
    expect(screen.getByText("Off")).toBeInTheDocument();
  });

  it("labelled toggle always shows the state and the label toggles it", async () => {
    const onChange = vi.fn();
    renderWithChakra(<Toggle label="Banned" checked={false} onChange={onChange} />);
    expect(screen.getByText("Off")).toBeInTheDocument();
    await userEvent.click(screen.getByText("Banned"));
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("disabled toggle ignores label clicks", async () => {
    const onChange = vi.fn();
    renderWithChakra(<Toggle label="Anonymous" disabled checked={false} onChange={onChange} />);
    await userEvent.click(screen.getByText("Anonymous"));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("switch")).toBeDisabled();
  });
});
