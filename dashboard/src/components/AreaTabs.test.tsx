import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { createSystem, defaultConfig, ChakraProvider } from "@chakra-ui/react";
import { AreaTabs } from "./AreaTabs";

const system = createSystem(defaultConfig);

function renderAt(path: string) {
  const router = createMemoryRouter(
    [{ path: "*", element: <AreaTabs /> }],
    { initialEntries: [path] }
  );
  return render(
    <ChakraProvider value={system}>
      <RouterProvider router={router} />
    </ChakraProvider>
  );
}

describe("AreaTabs", () => {
  it("renders all eight area tabs as links", () => {
    renderAt("/");
    for (const label of ["Overview","Tables","Auth","Users","Storage","Database Functions","Code Functions","Providers"]) {
      expect(screen.getByRole("link", { name: new RegExp(label, "i") })).toBeInTheDocument();
    }
  });

  it("marks the current route's tab active via aria-current", () => {
    renderAt("/tables");
    expect(screen.getByRole("link", { name: /Tables/i })).toHaveAttribute("aria-current", "page");
    // Overview is not active on /tables (end-match on '/').
    expect(screen.getByRole("link", { name: /Overview/i })).not.toHaveAttribute("aria-current", "page");
  });

  it("marks Overview active only at exactly '/'", () => {
    renderAt("/");
    expect(screen.getByRole("link", { name: /Overview/i })).toHaveAttribute("aria-current", "page");
  });
});
