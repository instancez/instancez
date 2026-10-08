import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithChakra } from "../test/helpers";
import { SecurityPage } from "./Security";
import { BackendProvider } from "../console/BackendContext";
import { fullCapabilities, type ConsoleBackend } from "../console/backend";
import type { VetFinding, VetReport } from "../lib/types";

const f = (over: Partial<VetFinding>): VetFinding => ({
  rule: "rls-disabled", severity: "high", path: "tables.notes.rls_enabled", line: 12,
  title: "RLS disabled", message: "Anyone can read this table.", fix: "Enable RLS.", ...over,
});

const report = (findings: VetFinding[]): VetReport => ({
  findings,
  counts: { critical: 0, high: 0, medium: 0, low: 0, info: 0 },
});

function renderPage(getVetReport: ConsoleBackend["getVetReport"]) {
  const backend = { capabilities: fullCapabilities(), getVetReport } as unknown as ConsoleBackend;
  return renderWithChakra(<BackendProvider backend={backend}><SecurityPage /></BackendProvider>);
}

const sample = [
  f({ rule: "r-info", severity: "info", title: "Info one" }),
  f({ rule: "r-crit", severity: "critical", title: "Crit one" }),
  f({ rule: "r-high", severity: "high", title: "High one" }),
  f({ rule: "r-high2", severity: "high", title: "High two" }),
];

const order = () => ["Crit one", "High one", "High two", "Info one"].map((t) => screen.queryByText(t)).filter(Boolean);

afterEach(() => { vi.unstubAllGlobals(); });

describe("SecurityPage", () => {
  it("shows a skeleton while loading", () => {
    renderPage(() => new Promise(() => {}));
    expect(screen.getByTestId("list-skeleton")).toBeInTheDocument();
  });

  it("renders per-severity counts and groups findings by severity descending", async () => {
    renderPage(async () => report(sample));
    await screen.findByText("Crit one");
    expect(screen.getByRole("button", { name: "critical 1" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "high 2" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "medium 0" })).toBeDisabled();
    const text = document.body.textContent ?? "";
    const idx = ["Crit one", "High one", "High two", "Info one"].map((t) => text.indexOf(t));
    expect(idx).toEqual([...idx].sort((a, b) => a - b));
    expect(order()).toHaveLength(4);
  });

  it("filters by tile and clears on a second click", async () => {
    const user = userEvent.setup();
    renderPage(async () => report(sample));
    await screen.findByText("Crit one");
    await user.click(screen.getByRole("button", { name: "high 2" }));
    expect(screen.queryByText("Crit one")).not.toBeInTheDocument();
    expect(screen.getByText("High one")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "high 2" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "high 2" }));
    expect(screen.getByText("Crit one")).toBeInTheDocument();
  });

  it("does not filter on a zero-count tile", async () => {
    const user = userEvent.setup();
    renderPage(async () => report(sample));
    await screen.findByText("Crit one");
    await user.click(screen.getByRole("button", { name: "medium 0" }));
    expect(screen.getByText("Crit one")).toBeInTheDocument();
  });

  it("shows the all-clear state for no findings", async () => {
    renderPage(async () => report([]));
    expect(await screen.findByText("No security findings")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /critical/ })).not.toBeInTheDocument();
  });

  it("shows the server message and retries", async () => {
    const user = userEvent.setup();
    const get = vi.fn().mockRejectedValueOnce(new Error("config is invalid: fix it first")).mockResolvedValueOnce(report([]));
    renderPage(get);
    expect(await screen.findByText("config is invalid: fix it first")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("No security findings")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("buckets an unknown severity as info without crashing", async () => {
    renderPage(async () => report([f({ rule: "r-x", severity: "catastrophic" as never, title: "Future one" })]));
    expect(await screen.findByText("Future one")).toBeInTheDocument();
    expect(screen.getByText("catastrophic")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "info 1" })).toBeInTheDocument();
  });

  it("ignores a stale response after a re-scan", async () => {
    const user = userEvent.setup();
    let resolveFirst!: (r: VetReport) => void;
    const get = vi.fn()
      .mockImplementationOnce(() => new Promise<VetReport>((r) => { resolveFirst = r; }))
      .mockResolvedValueOnce(report([f({ title: "Fresh" })]));
    renderPage(get);
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    expect(await screen.findByText("Fresh")).toBeInTheDocument();
    await act(async () => { resolveFirst(report([f({ title: "Stale" })])); });
    expect(screen.queryByText("Stale")).not.toBeInTheDocument();
    expect(screen.getByText("Fresh")).toBeInTheDocument();
  });

  it("does not set state after unmount", async () => {
    const err = vi.spyOn(console, "error").mockImplementation(() => {});
    let resolve!: (r: VetReport) => void;
    const { unmount } = renderPage(() => new Promise<VetReport>((r) => { resolve = r; }));
    unmount();
    await act(async () => { resolve(report([f({})])); });
    expect(err).not.toHaveBeenCalled();
    err.mockRestore();
  });

  it("hides the line when it is 0 and shows it otherwise", async () => {
    renderPage(async () => report([f({ title: "A", line: 0 }), f({ title: "B", line: 7, rule: "r2" })]));
    await screen.findByText("A");
    expect(screen.queryByText("line 0")).not.toBeInTheDocument();
    expect(screen.getByText("line 7")).toBeInTheDocument();
  });

  it("copies the exact ignore comment", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    renderPage(async () => report([f({})]));
    await user.click(await screen.findByText("Ignore"));
    expect(screen.getByText("# inz-vet-ignore: rls-disabled")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Copy ignore comment/ }));
    expect(writeText).toHaveBeenCalledWith("# inz-vet-ignore: rls-disabled");
    expect(await screen.findByText("Copied")).toBeInTheDocument();
  });

  it("reports a failed copy when the clipboard rejects", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText: vi.fn().mockRejectedValue(new Error("denied")) } });
    renderPage(async () => report([f({})]));
    await user.click(await screen.findByText("Ignore"));
    await user.click(screen.getByRole("button", { name: /Copy ignore comment/ }));
    await waitFor(() => { expect(screen.getByText("Copy failed")).toBeInTheDocument(); });
  });
});
