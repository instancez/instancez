import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, useRoutes } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { ConsoleProvider } from "./ConsoleProvider";
import { adminBackend } from "./adminBackend";
import { tablesRoutes } from "./routes";
import * as api from "../api/client";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<typeof api>();
  return {
    ...real,
    getConfig: vi.fn().mockResolvedValue({
      version: 1, project: { name: "P", description: "" },
      extensions: [], tables: { todos: { fields: [{ name: "id", type: "bigserial", primary_key: true }], indexes: [], rls: [] } },
      auth: null, storage: {}, rpc: {}, functions: {}, data: {},
      providers: { email: null, storage: null },
      server: { port: 8080, max_body_size: "10MB", max_limit: 1000, docs_ui: true,
        cors: { origins: [], methods: [], headers: [], credentials: false, max_age: 0 },
        timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
        db: { pool: { max: 25, min: 5, idle_timeout: "5m" } } },
      _checksum: "abc",
    }),
    getConfigStatus: vi.fn().mockResolvedValue({ dotenv_writable: false }),
    getEnvVars: vi.fn().mockResolvedValue({ vars: {} }),
  };
});

function Mounted() {
  return useRoutes(tablesRoutes());
}

describe("chrome-free pages", () => {
  it("a mounted fragment renders content without page chrome", async () => {
    const { container } = renderWithChakra(
      <MemoryRouter initialEntries={["/"]}>
        <ConsoleProvider backend={adminBackend}>
          <Mounted />
        </ConsoleProvider>
      </MemoryRouter>
    );
    expect(await screen.findByText("todos")).toBeInTheDocument();
    // No <h1> page title and no page-level gutters in the fragment itself.
    expect(container.querySelector("h1")).toBeNull();
  });
});
