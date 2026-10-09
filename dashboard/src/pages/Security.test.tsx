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

const report = (findings: VetFinding[], checks?: VetReport["checks"]): VetReport => {
  const counts = { critical: 0, high: 0, medium: 0, low: 0, info: 0 };
  for (const x of findings) if (x.severity in counts) counts[x.severity]++;
  return checks ? { findings, counts, checks } : { findings, counts };
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
    expect(screen.getByRole("button", { name: "1 critical" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "2 high" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "0 medium" })).toBeDisabled();
    const text = document.body.textContent ?? "";
    const idx = ["Crit one", "High one", "High two", "Info one"].map((t) => text.indexOf(t));
    expect(idx).toEqual([...idx].sort((a, b) => a - b));
    expect(order()).toHaveLength(4);
  });

  it("filters by tile and clears on a second click", async () => {
    const user = userEvent.setup();
    renderPage(async () => report(sample));
    await screen.findByText("Crit one");
    await user.click(screen.getByRole("button", { name: "2 high" }));
    expect(screen.queryByText("Crit one")).not.toBeInTheDocument();
    expect(screen.getByText("High one")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "2 high" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "2 high" }));
    expect(screen.getByText("Crit one")).toBeInTheDocument();
  });

  it("does not filter on a zero-count tile", async () => {
    const user = userEvent.setup();
    renderPage(async () => report(sample));
    await screen.findByText("Crit one");
    await user.click(screen.getByRole("button", { name: "0 medium" }));
    expect(screen.getByText("Crit one")).toBeInTheDocument();
  });

  it("shows the all-clear state for no findings", async () => {
    renderPage(async () => report([]));
    expect(await screen.findByText("No security findings")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "0 critical" })).toBeDisabled();
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
    expect(screen.getByRole("button", { name: "1 info" })).toBeInTheDocument();
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
    expect(await screen.findByText("4 findings", { exact: false })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    expect(await screen.findByText("boom")).toBeInTheDocument();
    expect(screen.queryByText("4 findings", { exact: false })).not.toBeInTheDocument();
  });

  it("takes tile counts from report.counts, missing keys as 0", async () => {
    renderPage(async () => ({ findings: [f({ severity: "high" })], counts: { high: 5 } as never }));
    expect(await screen.findByRole("button", { name: "5 high" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "0 low" })).toBeInTheDocument();
  });

  it("resets the filter when its severity disappears after a re-scan", async () => {
    const user = userEvent.setup();
    const get = vi.fn()
      .mockResolvedValueOnce(report(sample))
      .mockResolvedValueOnce(report([f({ rule: "r-crit", severity: "critical", title: "Crit one" })]))
      .mockResolvedValueOnce(report(sample));
    renderPage(get);
    await screen.findByText("Crit one");
    await user.click(screen.getByRole("button", { name: "2 high" }));
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    await waitFor(() => { expect(screen.getByRole("button", { name: "0 high" })).toBeInTheDocument(); });
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    expect(await screen.findByText("High one")).toBeInTheDocument();
    expect(screen.getByText("Crit one")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "2 high" })).toHaveAttribute("aria-pressed", "false");
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

describe("SecurityPage banner", () => {
  const ck = { total: 21, passed: 12 };
  const many = [
    f({ rule: "a", severity: "critical", title: "C1" }),
    f({ rule: "a", severity: "critical", title: "C2", path: "p2" }),
    f({ rule: "b", severity: "high", title: "H1" }),
    f({ rule: "c", severity: "low", title: "L1" }),
  ];

  it("shows passed of total and the summary line", async () => {
    renderPage(async () => report(many, ck));
    expect(await screen.findByText("12")).toBeInTheDocument();
    expect(screen.getByText("of 21 checks passed")).toBeInTheDocument();
    expect(screen.getByText("Needs attention")).toBeInTheDocument();
    expect(screen.getByText("4 findings across 3 checks · start with the 2 critical ones")).toBeInTheDocument();
    expect(screen.getByText("12 passed")).toBeInTheDocument();
  });

  it("singular wording and leads with high when no critical", async () => {
    renderPage(async () => report([f({ severity: "high" })], ck));
    expect(await screen.findByText("1 finding across 1 check · start with the 1 high one")).toBeInTheDocument();
  });

  it("omits the start-with clause without critical or high", async () => {
    renderPage(async () => report([f({ severity: "low" }), f({ severity: "medium", rule: "z" })], ck));
    expect(await screen.findByText("2 findings across 2 checks")).toBeInTheDocument();
  });

  it("says all checks passed with no findings", async () => {
    renderPage(async () => report([], { total: 21, passed: 21 }));
    expect(await screen.findByText("All checks passed")).toBeInTheDocument();
    expect(screen.getByText("of 21 checks passed")).toBeInTheDocument();
    expect(screen.queryByText(/across/)).not.toBeInTheDocument();
  });

  it("falls back to a findings headline without checks", async () => {
    renderPage(async () => report(many));
    expect(await screen.findByText("findings")).toBeInTheDocument();
    expect(screen.queryByText(/checks passed/)).not.toBeInTheDocument();
    expect(screen.queryByText(/ passed$/)).not.toBeInTheDocument();
  });

  it("fallback singular and all-clear without numbers", async () => {
    const { unmount } = renderPage(async () => report([f({})]));
    expect(await screen.findByText("finding")).toBeInTheDocument();
    unmount();
    renderPage(async () => report([]));
    expect(await screen.findByText("All clear")).toBeInTheDocument();
    expect(screen.queryByText(/of \d+ checks/)).not.toBeInTheDocument();
  });

  it("hides zero segments and aria-hides the bar", async () => {
    renderPage(async () => report(many, ck));
    await screen.findByText("Needs attention");
    const bars = [...document.querySelectorAll('div[aria-hidden="true"]')].filter((e) => e.children.length > 1);
    expect(bars).toHaveLength(1);
    expect(bars[0]!.children).toHaveLength(4);
  });

  it("omits the passed segment when nothing passed", async () => {
    renderPage(async () => report(many, { total: 4, passed: 0 }));
    await screen.findByText("Needs attention");
    const bar = [...document.querySelectorAll('div[aria-hidden="true"]')].find((e) => e.children.length > 1)!;
    expect(bar.children).toHaveLength(3);
  });

  it("chips filter with aria-pressed and All resets", async () => {
    const user = userEvent.setup();
    renderPage(async () => report(many, ck));
    await screen.findByText("C1");
    expect(screen.getByRole("button", { name: "All 4" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "1 high" }));
    expect(screen.queryByText("C1")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "1 high" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "All 4" })).toHaveAttribute("aria-pressed", "false");
    await user.click(screen.getByRole("button", { name: "All 4" }));
    expect(screen.getByText("C1")).toBeInTheDocument();
  });

  it("disables zero-count chips", async () => {
    renderPage(async () => report(many, ck));
    await screen.findByText("C1");
    expect(screen.getByRole("button", { name: "0 medium" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "0 info" })).toBeDisabled();
  });

  it("buckets an unknown severity as info in the summary and chips", async () => {
    renderPage(async () => report([f({ severity: "weird" as never })], ck));
    expect(await screen.findByText("1 finding across 1 check")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "1 info" })).toBeEnabled();
  });
});
