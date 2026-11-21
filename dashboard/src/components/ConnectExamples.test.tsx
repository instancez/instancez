import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithChakra } from "../test/helpers";
import { ApiBaseUrlProvider } from "../console/BackendContext";
import { ConnectExamples } from "./ConnectExamples";

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
    for (const lang of ["JS/TS", "Python", "Go"]) {
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
