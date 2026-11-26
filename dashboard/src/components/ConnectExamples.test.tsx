import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithChakra } from "../test/helpers";
import { ApiBaseUrlProvider } from "../console/BackendContext";
import { CLIENT_LIBS, ClientSnippets, ConnectExamples, highlight } from "./ConnectExamples";

function mount(table = "todos") {
  renderWithChakra(
    <ApiBaseUrlProvider value="https://api.test">
      <ConnectExamples exampleTable={table} />
    </ApiBaseUrlProvider>
  );
}

describe("ConnectExamples", () => {
  it("uses a PUBLISHABLE_KEY placeholder in every language, never a literal key", async () => {
    mount();
    for (const lang of CLIENT_LIBS.map((l) => l.label)) {
      await userEvent.click(screen.getByRole("button", { name: lang }));
      const snippet = screen.getByTestId("connect-snippet").textContent ?? "";
      expect(snippet).toMatch(/PUBLISHABLE_KEY|publishableKey/);
      expect(snippet).not.toMatch(/eyJ|sb_publishable|sk_/);
      expect(snippet).toContain("https://api.test");
    }
  });

  it("tells the user where the key comes from and warns about the secret key", () => {
    mount();
    expect(screen.getByText(/publishable key above/i)).toBeInTheDocument();
    expect(screen.getByText(/never ship the secret key/i)).toBeInTheDocument();
  });

  it("puts the example table into the query", () => {
    mount("orders");
    expect(screen.getByTestId("connect-snippet")).toHaveTextContent("from('orders')");
  });
});

describe("ClientSnippets", () => {
  it("offers JS, Python, Dart, Swift, Kotlin and Go", () => {
    expect(CLIENT_LIBS.map((l) => l.label)).toEqual(["JS/TS", "Python", "Dart", "Swift", "Kotlin", "Go"]);
  });

  it("highlights each language and keeps the code text intact", () => {
    for (const lib of CLIENT_LIBS) {
      const code = lib.snippet("https://api.test", "todos", "k");
      const { container, unmount } = renderWithChakra(<pre data-testid="hl">{highlight(code, lib.language)}</pre>);
      const pre = container.querySelector("[data-testid=hl]")!;
      expect(pre.textContent, lib.label).toBe(code);
      expect(pre.querySelector(".tok-string"), lib.label).not.toBeNull();
      expect(pre.querySelector(".tok-keyword"), lib.label).not.toBeNull();
      unmount();
    }
  });

  it("keeps every snippet line short enough to fit the panel", () => {
    for (const lib of CLIENT_LIBS) {
      const longest = Math.max(...lib.snippet("https://api.test", "todos").split("\n").map((l) => l.length));
      expect(longest, lib.label).toBeLessThanOrEqual(80);
    }
  });

  it("shows a shell prompt only for shell install commands", async () => {
    renderWithChakra(<ClientSnippets url="https://api.test" table="todos" />);
    expect(screen.getByText(/^\$ npm install/, { selector: "code" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Kotlin" }));
    const gradle = screen.getByText(/^implementation\(/, { selector: "code" });
    expect(gradle).toHaveTextContent("postgrest-kt");
    expect(gradle).toHaveTextContent("ktor-client-cio");
  });

  it("inlines a passed publishable key as a string literal in every language", async () => {
    renderWithChakra(<ClientSnippets url="https://api.test" table="todos" publishableKey="sb_publishable_abc" />);
    for (const lib of CLIENT_LIBS) {
      await userEvent.click(screen.getByRole("button", { name: lib.label }));
      const snippet = screen.getByTestId("connect-snippet").textContent ?? "";
      expect(snippet).toMatch(/["']sb_publishable_abc["']/);
      expect(snippet).not.toMatch(/PUBLISHABLE_KEY|publishableKey/);
    }
  });

  it("marks the selected client and swaps the install line", async () => {
    renderWithChakra(<ClientSnippets url="https://api.test" table="todos" />);
    expect(screen.getByRole("button", { name: "JS/TS" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.click(screen.getByRole("button", { name: "Swift" }));
    expect(screen.getByRole("button", { name: "Swift" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText(/supabase-swift/, { selector: "code" })).toBeInTheDocument();
  });
});
