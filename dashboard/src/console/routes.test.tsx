import { describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useRoutes } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { consoleRoutes } from "./routes";
import { ConsoleProvider } from "./ConsoleProvider";
import { adminBackend } from "./adminBackend";
import * as api from "../api/client";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<typeof api>();
  return {
    ...real,
    getConfig: vi.fn().mockResolvedValue({
      version: 1, project: { name: "P", description: "" },
      tables: {}, auth: null, storage: {}, rpc: {}, functions: {},
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
  };
});

function Console() {
  return useRoutes(consoleRoutes());
}

describe("consoleRoutes", () => {
  it("mounts the providers page standalone with an injected backend", async () => {
    renderWithChakra(
      <MemoryRouter initialEntries={["/providers"]}>
        <ConsoleProvider backend={adminBackend}>
          <Console />
        </ConsoleProvider>
      </MemoryRouter>
    );
    await waitFor(() => expect(screen.getByText("Email Provider")).toBeInTheDocument());
  });
});
