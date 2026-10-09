import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { ChakraProvider, createSystem, defaultConfig } from "@chakra-ui/react";
import { ColorModeProvider } from "../components/color-mode";
import { renderWithChakra } from "../test/helpers";
import { SecurityPage } from "./Security";
import { BackendProvider } from "../console/BackendContext";
import { fullCapabilities, type ConsoleBackend } from "../console/backend";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, VetFinding, VetReport } from "../lib/types";

const f = (over: Partial<VetFinding>): VetFinding => ({
  rule: "rls-disabled", severity: "high", path: "tables.notes.rls_enabled", line: 12,
  title: "RLS disabled", message: "Anyone can read this table.", fix: "Enable RLS.", ...over,
});

const report = (findings: VetFinding[], checks?: VetReport["checks"]): VetReport => {
  const counts = { critical: 0, high: 0, medium: 0, low: 0, info: 0 };
  for (const x of findings) counts[x.severity in counts ? x.severity : "info"]++;
  return checks ? { findings, counts, checks } : { findings, counts };
};

const baseConfig = {
  version: 1, project: { name: "t", description: "" }, tables: { notes: { rls_enabled: false } }, auth: null,
  storage: {}, rpc: {}, functions: {}, server: { cors: { origins: ["https://a.example", "null"] } },
} as unknown as Config;

type ConfigValue = NonNullable<React.ContextType<typeof ConfigContext>>;
type PageOpts = { save?: ConfigValue["save"]; canWriteConfig?: boolean; config?: Config | null };

const baseCtx = {
  config: baseConfig, loading: false, error: null, checksum: "c", saving: false, saveErrors: [],
  dotenvWritable: false, oauthCallbackBase: "", refresh: vi.fn(), save: vi.fn().mockResolvedValue(true), updateConfig: vi.fn(),
} as ConfigValue;

function renderPage(getVetReport: ConsoleBackend["getVetReport"], opts: PageOpts = {}) {
  const capabilities = { ...fullCapabilities(), canWriteConfig: opts.canWriteConfig ?? true };
  const backend = { capabilities, getVetReport } as unknown as ConsoleBackend;
  const save = opts.save ?? vi.fn().mockResolvedValue(true);
  const ctx = {
    config: opts.config === undefined ? baseConfig : opts.config, loading: false, error: null, checksum: "c", saving: false, saveErrors: [],
    dotenvWritable: false, oauthCallbackBase: "", refresh: vi.fn(), save, updateConfig: vi.fn(),
  } as ConfigValue;
  const view = renderWithChakra(
    <MemoryRouter><ConfigContext.Provider value={ctx}><BackendProvider backend={backend}><SecurityPage /><Where /></BackendProvider></ConfigContext.Provider></MemoryRouter>,
  );
  return { ...view, save: save as ReturnType<typeof vi.fn> };
}

function Where() { return <span data-testid="where">{useLocation().pathname}</span>; }

const sample = [
  f({ rule: "r-info", severity: "info", title: "Info one" }),
  f({ rule: "r-crit", severity: "critical", title: "Crit one" }),
  f({ rule: "r-high", severity: "high", title: "High one" }),
  f({ rule: "r-high2", severity: "high", title: "High two" }),
];

afterEach(() => { vi.unstubAllGlobals(); });

describe("SecurityPage", () => {
  it("shows a skeleton while loading", () => {
    renderPage(() => new Promise(() => {}));
    expect(screen.getByTestId("list-skeleton")).toBeInTheDocument();
  });

  it("renders per-severity counts and groups findings by severity descending", async () => {
    renderPage(async () => report(sample));
    await screen.findByText("HOW TO FIX");
    expect(screen.getByRole("button", { name: "1 critical" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "2 high" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "0 medium" })).toBeDisabled();
    const titles = ["Crit one", "High one", "High two", "Info one"];
    const listed = screen.getAllByRole("button").map((b) => titles.find((t) => b.textContent?.startsWith(t))).filter(Boolean);
    expect(listed).toEqual(titles);
  });

  it("filters by chip, clears on a second click or All, ignores zero chips", async () => {
    const user = userEvent.setup();
    renderPage(async () => report(sample));
    await screen.findByText("HOW TO FIX");
    expect(screen.getByRole("button", { name: "All 4" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "0 medium" }));
    expect(screen.getAllByText("Crit one").length).toBeGreaterThan(0);
    await user.click(screen.getByRole("button", { name: "2 high" }));
    expect(screen.queryByText("Crit one")).not.toBeInTheDocument();
    expect(screen.getAllByText("High one").length).toBeGreaterThan(0);
    expect(screen.getByRole("button", { name: "2 high" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", { name: "All 4" })).toHaveAttribute("aria-pressed", "false");
    await user.click(screen.getByRole("button", { name: "2 high" }));
    expect(screen.getAllByText("Crit one").length).toBeGreaterThan(0);
    await user.click(screen.getByRole("button", { name: "2 high" }));
    await user.click(screen.getByRole("button", { name: "All 4" }));
    expect(screen.getAllByText("Crit one").length).toBeGreaterThan(0);
  });

  it("shows the all-clear state for no findings", async () => {
    renderPage(async () => report([]));
    expect(await screen.findByText("Nothing to fix")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "0 critical" })).toBeDisabled();
  });

  it("shows the server message and retries", async () => {
    const user = userEvent.setup();
    const get = vi.fn().mockRejectedValueOnce(new Error("config is invalid: fix it first")).mockResolvedValueOnce(report([]));
    renderPage(get);
    expect(await screen.findByText("config is invalid: fix it first")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("Nothing to fix")).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("buckets an unknown severity as info everywhere, once, keeping its raw label in the detail", async () => {
    const weird = f({ rule: "r-x", severity: "weird" as never, title: "Future one" });
    renderPage(async () => report([weird], { total: 21, passed: 20 }));
    await screen.findByText("HOW TO FIX");
    expect(screen.getByText("weird")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "1 info" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "All 1" })).toBeInTheDocument();
    expect(screen.getByText("1 finding across 1 check")).toBeInTheDocument();
  });

  it("derives counts from findings, ignoring server counts that already fold unknown into info", async () => {
    const findings = [f({ severity: "high" }), f({ rule: "r-x", severity: "weird" as never })];
    renderPage(async () => ({ findings, counts: { critical: 0, high: 1, medium: 0, low: 0, info: 1 } }));
    expect(await screen.findByRole("button", { name: "1 info" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "1 high" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "All 2" })).toBeInTheDocument();
  });

  it("derives counts from findings when the server counts are wrong or missing", async () => {
    renderPage(async () => ({ findings: [f({ severity: "high" })], counts: { high: 5 } as never }));
    expect(await screen.findByRole("button", { name: "1 high" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "0 low" })).toBeInTheDocument();
  });

  it("ignores a stale response after the backend changes", async () => {
    let resolveFirst!: (r: VetReport) => void;
    const mk = (g: ConsoleBackend["getVetReport"]) => ({ capabilities: fullCapabilities(), getVetReport: g }) as unknown as ConsoleBackend;
    const first = mk(() => new Promise<VetReport>((r) => { resolveFirst = r; }));
    const second = mk(async () => report([f({ title: "Fresh" })]));
    const tree = (b: ConsoleBackend) => (
      <MemoryRouter><ConfigContext.Provider value={baseCtx}><BackendProvider backend={b}><SecurityPage /></BackendProvider></ConfigContext.Provider></MemoryRouter>
    );
    const { rerender } = renderWithChakra(tree(first));
    rerender(<ChakraProvider value={createSystem(defaultConfig)}><ColorModeProvider>{tree(second)}</ColorModeProvider></ChakraProvider>);
    await screen.findByText("HOW TO FIX");
    await act(async () => { resolveFirst(report([f({ title: "Stale" })])); });
    expect(screen.queryByText("Stale")).not.toBeInTheDocument();
    expect(screen.getAllByText("Fresh").length).toBeGreaterThan(0);
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

  it("says Scanning only while scanning, else Checked, also on error", async () => {
    const user = userEvent.setup();
    const get = vi.fn().mockResolvedValueOnce(report([])).mockRejectedValueOnce(new Error("boom"));
    renderPage(get);
    expect(screen.getByText(/Scanning/)).toBeInTheDocument();
    expect(await screen.findByText(/Checked/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    await screen.findByText("boom");
    expect(screen.getByText(/Checked/)).toBeInTheDocument();
    expect(screen.queryByText(/Scanning/)).not.toBeInTheDocument();
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

  it("resets the filter when its severity disappears after a re-scan", async () => {
    const user = userEvent.setup();
    const get = vi.fn()
      .mockResolvedValueOnce(report(sample))
      .mockResolvedValueOnce(report([f({ rule: "r-crit", severity: "critical", title: "Crit one" })]))
      .mockResolvedValueOnce(report(sample));
    renderPage(get);
    await screen.findByText("HOW TO FIX");
    await user.click(screen.getByRole("button", { name: "2 high" }));
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    await waitFor(() => { expect(screen.getByRole("button", { name: "0 high" })).toBeInTheDocument(); });
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    await screen.findAllByText("High one");
    expect(screen.getAllByText("Crit one").length).toBeGreaterThan(0);
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

});

describe("SecurityPage list and detail", () => {
  const items = [
    f({ rule: "r-crit", severity: "critical", title: "Crit one", path: "tables.a.rls[0]", line: 3, fix: "Fix crit." }),
    f({ rule: "r-high", severity: "high", title: "High one", path: "storage.b", line: 0, fix: "Fix high." }),
    f({ rule: "r-high", severity: "high", title: "High one", path: "storage.b", line: 0, fix: "Fix high dup." }),
    f({ rule: "r-low", severity: "low", title: "Low one", path: "server.cors", line: 9, fix: "Fix low." }),
  ];
  const row = (t: string, i = 0) => screen.getAllByRole("button").filter((b) => b.textContent?.includes(t))[i]!;

  it("selects the first finding by default and shows its detail", async () => {
    renderPage(async () => report(items));
    await screen.findByText("HOW TO FIX");
    expect(row("Crit one")).toHaveAttribute("aria-current", "true");
    expect(row("High one")).not.toHaveAttribute("aria-current");
    expect(screen.getByText("Fix crit.")).toBeInTheDocument();
    expect(screen.getByText("r-crit")).toBeInTheDocument();
    expect(screen.getByText("Anyone can read this table.")).toBeInTheDocument();
    expect(screen.getByText("line 3")).toBeInTheDocument();
  });

  it("lists path and line, hiding line 0", async () => {
    renderPage(async () => report(items));
    await screen.findByText("HOW TO FIX");
    expect(row("Crit one")).toHaveTextContent("tables.a.rls[0] · line 3");
    expect(row("High one")).toHaveTextContent(/^.*storage\.b(?! · line)/);
    expect(row("High one").textContent).not.toContain("line 0");
  });

  it("changes the detail on click, keeping duplicate rows distinct", async () => {
    const user = userEvent.setup();
    renderPage(async () => report(items));
    await screen.findByText("HOW TO FIX");
    await user.click(row("High one", 1));
    expect(screen.getByText("Fix high dup.")).toBeInTheDocument();
    expect(row("High one", 1)).toHaveAttribute("aria-current", "true");
    expect(row("High one", 0)).not.toHaveAttribute("aria-current");
  });

  it("resets the selection to the first visible when the filter changes", async () => {
    const user = userEvent.setup();
    renderPage(async () => report(items));
    await screen.findByText("HOW TO FIX");
    await user.click(row("Low one"));
    await user.click(screen.getByRole("button", { name: "2 high" }));
    expect(row("High one", 0)).toHaveAttribute("aria-current", "true");
    expect(screen.getByText("Fix high.")).toBeInTheDocument();
  });

  it("keeps the selection across a re-scan and drops it when it disappears", async () => {
    const user = userEvent.setup();
    const get = vi.fn()
      .mockResolvedValueOnce(report(items))
      .mockResolvedValueOnce(report(items))
      .mockResolvedValueOnce(report(items.slice(0, 2)));
    renderPage(get);
    await screen.findByText("HOW TO FIX");
    await user.click(row("Low one"));
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    await waitFor(() => { expect(get).toHaveBeenCalledTimes(2); });
    expect(row("Low one")).toHaveAttribute("aria-current", "true");
    await user.click(screen.getByRole("button", { name: /Re-scan/ }));
    await waitFor(() => { expect(screen.queryByText("Low one")).not.toBeInTheDocument(); });
    expect(row("Crit one")).toHaveAttribute("aria-current", "true");
  });

  it("hides the fix callout when there is no fix text", async () => {
    renderPage(async () => report([f({ fix: "" })]));
    await screen.findByText("Anyone can read this table.");
    expect(screen.queryByText("HOW TO FIX")).not.toBeInTheDocument();
  });

  it("has no copy, open-in or ignore actions", async () => {
    renderPage(async () => report([f({ edit: { path: ["tables", "notes", "rls_enabled"], value: true } })]));
    await screen.findByText("HOW TO FIX");
    expect(screen.queryByRole("button", { name: /Copy/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.queryByText(/Ignore/)).not.toBeInTheDocument();
    expect(screen.queryByText(/inz-vet-ignore/)).not.toBeInTheDocument();
  });

  it("scrolls the detail into view only when stacked", async () => {
    const user = userEvent.setup();
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    const rect = vi.spyOn(Element.prototype, "getBoundingClientRect");
    renderPage(async () => report(items));
    await screen.findByText("HOW TO FIX");
    rect.mockImplementation(function (this: Element) {
      return { top: this.textContent?.includes("HOW TO FIX") ? 0 : 0, bottom: 100 } as DOMRect;
    });
    await user.click(row("Low one"));
    expect(scroll).not.toHaveBeenCalled();
    rect.mockImplementation(function (this: Element) {
      return { top: this.textContent?.includes("HOW TO FIX") ? 500 : 0, bottom: 100 } as DOMRect;
    });
    await user.click(row("High one"));
    expect(scroll).toHaveBeenCalledTimes(1);
    rect.mockRestore();
    delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
  });

  it("does not crash on selection when scrollIntoView is missing", async () => {
    const user = userEvent.setup();
    renderPage(async () => report(items));
    await screen.findByText("HOW TO FIX");
    await user.click(row("Low one"));
    expect(row("Low one")).toHaveAttribute("aria-current", "true");
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

  it.each([
    ["critical leads", [f({ rule: "a", severity: "critical" }), f({ rule: "a", severity: "critical", path: "p2" }), f({ rule: "b", severity: "high" }), f({ rule: "c", severity: "low" })], "4 findings across 3 checks · start with the 2 critical ones"],
    ["singular high leads", [f({ severity: "high" })], "1 finding across 1 check · start with the 1 high one"],
    ["no lead without critical or high", [f({ severity: "low" }), f({ severity: "medium", rule: "z" })], "2 findings across 2 checks"],
  ])("summary: %s", async (_n, findings, text) => {
    renderPage(async () => report(findings, ck));
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(screen.getByText("Needs attention")).toBeInTheDocument();
  });

  it("shows passed of total", async () => {
    renderPage(async () => report(many, ck));
    expect(await screen.findByText("12")).toBeInTheDocument();
    expect(screen.getByText("of 21 checks passed")).toBeInTheDocument();
    expect(screen.getByText("12 passed")).toBeInTheDocument();
  });

  it("says all checks passed with no findings", async () => {
    renderPage(async () => report([], { total: 21, passed: 21 }));
    expect(await screen.findByText("All checks passed")).toBeInTheDocument();
    expect(screen.getByText("of 21 checks passed")).toBeInTheDocument();
    expect(screen.queryByText(/across/)).not.toBeInTheDocument();
  });

  it.each([
    ["findings", many, "findings"],
    ["finding", [f({})], "finding"],
    ["all clear", [], "All clear"],
  ])("without checks falls back to %s", async (_n, findings, text) => {
    renderPage(async () => report(findings));
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(screen.queryByText(/checks passed/)).not.toBeInTheDocument();
    expect(screen.queryByText(/ passed$/)).not.toBeInTheDocument();
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

  it("disables zero-count chips", async () => {
    renderPage(async () => report(many, ck));
    await screen.findByText("HOW TO FIX");
    expect(screen.getByRole("button", { name: "0 medium" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "0 info" })).toBeDisabled();
  });
});

describe("SecurityPage Fix it", () => {
  const edit = { path: ["tables", "notes", "rls_enabled"], value: true };
  const fixable = () => report([f({ edit })]);

  it("stages the edit through save and rescans on success", async () => {
    const user = userEvent.setup();
    const getVetReport = vi.fn().mockResolvedValue(fixable());
    const { save } = renderPage(getVetReport);
    await user.click(await screen.findByRole("button", { name: "Fix it" }));
    expect(save).toHaveBeenCalledTimes(1);
    const saved = save.mock.calls[0]![0] as Config;
    expect(saved.tables["notes"]!.rls_enabled).toBe(true);
    expect(baseConfig.tables["notes"]!.rls_enabled).toBe(false);
    await waitFor(() => { expect(getVetReport).toHaveBeenCalledTimes(2); });
  });

  it("does not rescan when the save is cancelled or rejected", async () => {
    const user = userEvent.setup();
    const getVetReport = vi.fn().mockResolvedValue(fixable());
    const { save } = renderPage(getVetReport, { save: vi.fn().mockResolvedValue(false) });
    await user.click(await screen.findByRole("button", { name: "Fix it" }));
    await waitFor(() => { expect(save).toHaveBeenCalled(); });
    expect(getVetReport).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Fix it" })).toBeEnabled();
  });

  it("shows Fixing and blocks a second click while the save is pending", async () => {
    const user = userEvent.setup();
    let finish!: (ok: boolean) => void;
    const save = vi.fn(() => new Promise<boolean>((r) => { finish = r; }));
    renderPage(async () => fixable(), { save });
    await user.click(await screen.findByRole("button", { name: "Fix it" }));
    const busy = await screen.findByRole("button", { name: "Fixing…" });
    expect(busy).toBeDisabled();
    await user.click(busy);
    expect(save).toHaveBeenCalledTimes(1);
    await act(async () => { finish(false); });
    expect(await screen.findByRole("button", { name: "Fix it" })).toBeEnabled();
  });

  it("rescans instead of saving when the scan no longer matches the config", async () => {
    const user = userEvent.setup();
    const getVetReport = vi.fn().mockResolvedValue(report([f({ edit: { path: ["server", "cors", "origins", 0], value: "null", remove: true } })]));
    const { save } = renderPage(getVetReport);
    await user.click(await screen.findByRole("button", { name: "Fix it" }));
    await waitFor(() => { expect(getVetReport).toHaveBeenCalledTimes(2); });
    expect(save).not.toHaveBeenCalled();
  });

  it("is hidden without an edit, without write access, or before the config loads", async () => {
    const { unmount } = renderPage(async () => report([f({})]));
    await screen.findByText("HOW TO FIX");
    expect(screen.queryByRole("button", { name: "Fix it" })).not.toBeInTheDocument();
    unmount();
    const second = renderPage(async () => fixable(), { canWriteConfig: false });
    await screen.findByText("HOW TO FIX");
    expect(screen.queryByRole("button", { name: "Fix it" })).not.toBeInTheDocument();
    second.unmount();
    renderPage(async () => fixable(), { config: null });
    await screen.findByText("HOW TO FIX");
    expect(screen.queryByRole("button", { name: "Fix it" })).not.toBeInTheDocument();
  });

  it("applies a remove edit and creates missing parents", async () => {
    const user = userEvent.setup();
    const { save, unmount } = renderPage(async () => report([f({ edit: { path: ["server", "cors", "origins", 1], value: null, remove: true } })]));
    await user.click(await screen.findByRole("button", { name: "Fix it" }));
    expect((save.mock.calls[0]![0] as Config).server.cors.origins).toEqual(["https://a.example"]);
    unmount();
    const next = renderPage(async () => report([f({ edit: { path: ["auth", "email", "verify_email"], value: true } })]), { config: { ...baseConfig, auth: { jwt_expiry: "1h" } as Config["auth"] } });
    await user.click(await screen.findByRole("button", { name: "Fix it" }));
    expect((next.save.mock.calls[0]![0] as Config).auth).toEqual({ jwt_expiry: "1h", email: { verify_email: true } });
  });
});
