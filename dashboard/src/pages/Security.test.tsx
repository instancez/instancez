import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ChakraProvider, createSystem, defaultConfig } from "@chakra-ui/react";
import { ColorModeProvider } from "../components/color-mode";
import { renderWithChakra } from "../test/helpers";
import { SecurityPage } from "./Security";
import { BackendProvider } from "../console/BackendContext";
import { fullCapabilities, type ConsoleBackend } from "../console/backend";
import type { VetFinding, VetReport } from "../lib/types";

const f = (over: Partial<VetFinding>): VetFinding => ({
  rule: "rls-disabled", severity: "high", path: "tables.notes.rls_enabled", line: 12,
  title: "RLS disabled", message: "Anyone can read this table.", fix: "Enable RLS.", ...over,
});

const report = (findings: VetFinding[]): VetReport => {
  const counts = { critical: 0, high: 0, medium: 0, low: 0, info: 0 };
  for (const x of findings) if (x.severity in counts) counts[x.severity]++;
  return { findings, counts };
};

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

  it("ignores a stale response after the backend changes", async () => {
    let resolveFirst!: (r: VetReport) => void;
    const mk = (g: ConsoleBackend["getVetReport"]) => ({ capabilities: fullCapabilities(), getVetReport: g }) as unknown as ConsoleBackend;
    const first = mk(() => new Promise<VetReport>((r) => { resolveFirst = r; }));
    const second = mk(async () => report([f({ title: "Fresh" })]));
    const { rerender } = renderWithChakra(<BackendProvider backend={first}><SecurityPage /></BackendProvider>);
    rerender(
      <ChakraProvider value={createSystem(defaultConfig)}><ColorModeProvider>
        <BackendProvider backend={second}><SecurityPage /></BackendProvider>
      </ColorModeProvider></ChakraProvider>,
    );
    expect(await screen.findByText("Fresh")).toBeInTheDocument();
    await act(async () => { resolveFirst(report([f({ title: "Stale" })])); });
    expect(screen.queryByText("Stale")).not.toBeInTheDocument();
    expect(screen.getByText("Fresh")).toBeInTheDocument();
  });

  it("disables Re-scan with aria-busy while a scan runs", async () => {
    let resolve!: (r: VetReport) => void;
    renderPage(() => new Promise<VetReport>((r) => { resolve = r; }));
    const btn = screen.getByRole("button", { name: /Re-scan/ });
    expect(btn).toBeDisabled();
    expect(btn).toHaveAttribute("aria-busy", "true");
    await act(async () => { resolve(report([])); });
    expect(btn).toBeEnabled();
    expect(btn).toHaveAttribute("aria-busy", "false");
  });

  it("drops the stale count when a re-scan errors", async () => {
    const user = userEvent.setup();
    const get = vi.fn().mockResolvedValueOnce(report(sample)).mockRejectedValueOnce(new Error("boom"));
    renderPage(get);
    expect(await screen.findByText("4 findings")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    expect(await screen.findByText("boom")).toBeInTheDocument();
    expect(screen.queryByText("4 findings")).not.toBeInTheDocument();
  });

  it("takes tile counts from report.counts, missing keys as 0", async () => {
    renderPage(async () => ({ findings: [f({ severity: "high" })], counts: { high: 5 } as never }));
    expect(await screen.findByRole("button", { name: "high 5" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "low 0" })).toBeInTheDocument();
  });

  it("resets the filter when its severity disappears after a re-scan", async () => {
    const user = userEvent.setup();
    const get = vi.fn()
      .mockResolvedValueOnce(report(sample))
      .mockResolvedValueOnce(report([f({ rule: "r-crit", severity: "critical", title: "Crit one" })]))
      .mockResolvedValueOnce(report(sample));
    renderPage(get);
    await screen.findByText("Crit one");
    await user.click(screen.getByRole("button", { name: "high 2" }));
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    await waitFor(() => { expect(screen.getByRole("button", { name: "high 0" })).toBeInTheDocument(); });
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    expect(await screen.findByText("High one")).toBeInTheDocument();
    expect(screen.getByText("Crit one")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "high 2" })).toHaveAttribute("aria-pressed", "false");
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
  });

  it("survives a rejected clipboard write", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockRejectedValue(new Error("denied"));
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    renderPage(async () => report([f({})]));
    await user.click(await screen.findByText("Ignore"));
    await user.click(screen.getByRole("button", { name: /Copy ignore comment/ }));
    expect(writeText).toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /Copy ignore comment/ })).toBeInTheDocument();
  });
});
