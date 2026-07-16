import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { createSystem, defaultConfig, ChakraProvider } from "@chakra-ui/react";
import { Layout } from "./Layout";
import { consoleRoutes } from "../console/routes";
import * as api from "../api/client";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<typeof api>();
  return {
    ...real,
    getConfig: vi.fn().mockResolvedValue({
      version: 1,
      project: { name: "My Project", description: "" },
      tables: { todos: { fields: [{ name: "id", type: "bigserial", primary_key: true }], indexes: [], rls: [] } },
      auth: null, storage: {}, rpc: {}, functions: {},
      providers: { email: null, storage: null },
      server: {
        port: 8080, max_body_size: "10MB", max_limit: 1000,
        cors: { origins: [] },
        timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
      },
      database: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
      _checksum: "abc",
    }),
    getConfigStatus: vi.fn().mockResolvedValue({ dotenv_writable: false }),
    getEnvVars: vi.fn().mockResolvedValue({ vars: {} }),
    getStats: vi.fn().mockResolvedValue({ tables: {}, storage: {} }),
    getKeys: vi.fn().mockResolvedValue({ publishable_key: "test-publishable-key" }),
  };
});

const system = createSystem(defaultConfig);

function renderAt(path: string) {
  const router = createMemoryRouter(
    [{ element: <Layout />, children: consoleRoutes() }],
    { initialEntries: [path] }
  );
  return render(
    <ChakraProvider value={system}>
      <RouterProvider router={router} />
    </ChakraProvider>
  );
}

describe("Layout (shell owns titles)", () => {
  it("renders the list-page title from the route handle, above the page content", async () => {
    renderAt("/tables");
    // Title chrome supplied by the shell (PageHeader -> <h1>), not the page.
    expect(
      await screen.findByRole("heading", { name: "Tables", level: 1 })
    ).toBeInTheDocument();
    // Page content (the table list) renders underneath.
    await waitFor(() => expect(screen.getByText("todos")).toBeInTheDocument());
  });

  it("renders no shell title for Overview (null handle), page owns its heading", async () => {
    renderAt("/");
    // No <h1> shell title — Overview supplies its own in-content heading.
    await waitFor(() =>
      expect(screen.getByText("My Project")).toBeInTheDocument()
    );
    expect(screen.queryByRole("heading", { level: 1 })).toBeNull();
  });

  it("renders a dynamic detail title from the route param", async () => {
    renderAt("/tables/todos");
    expect(
      await screen.findByRole("heading", { name: "todos", level: 1 })
    ).toBeInTheDocument();
  });
});
