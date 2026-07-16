import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { ProjectPage } from "./Project";
import { ConfigContext } from "../hooks/useConfig";
import type { Config, ValidationError } from "../lib/types";

const makeConfig = (origins: string[]): Config => ({
  version: 1,
  project: { name: "Test", description: "" },
  tables: {},
  auth: null,
  storage: {},
  rpc: {},
  functions: {},
  providers: { email: null, storage: null },
  server: {
    port: 8080,
    max_body_size: "10MB",
    max_limit: 1000,
    cors: { origins },
    timeouts: { request: "30s", db_query: "10s", upload: "60s", shutdown: "10s" },
  },
  database: { pool: { max: 25, min: 5, idle_timeout: "5m" } },
});

function renderProject(config: Config) {
  const save = vi.fn().mockResolvedValue(true);
  const ctx = {
    config,
    loading: false,
    error: null,
    checksum: "abc",
    saving: false,
    saveErrors: [] as ValidationError[],
    dotenvWritable: false,
    oauthCallbackBase: "",
    refresh: vi.fn(),
    save,
    updateConfig: vi.fn(),
  };
  const result = renderWithChakra(
    <ConfigContext.Provider value={ctx}>
      <MemoryRouter>
        <ProjectPage />
      </MemoryRouter>
    </ConfigContext.Provider>
  );
  return { ...result, save };
}

describe("ProjectPage", () => {
  it("shows existing CORS origins", () => {
    renderProject(makeConfig(["https://acme.com"]));
    expect(screen.getByDisplayValue("https://acme.com")).toBeInTheDocument();
  });

  it("adds an origin and saves the cleaned list", async () => {
    const { save } = renderProject(makeConfig([]));
    fireEvent.click(screen.getByText("Add origin"));
    fireEvent.change(screen.getByLabelText("CORS origin 1"), {
      target: { value: "https://app.example.com" },
    });
    fireEvent.click(await screen.findByText("Save Changes"));
    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({
        server: expect.objectContaining({ cors: { origins: ["https://app.example.com"] } }),
      })
    );
  });

  it("flags an invalid origin", () => {
    renderProject(makeConfig([]));
    fireEvent.click(screen.getByText("Add origin"));
    fireEvent.change(screen.getByLabelText("CORS origin 1"), {
      target: { value: "not-a-url" },
    });
    expect(screen.getByText(/Enter an absolute http\(s\) origin/)).toBeInTheDocument();
  });

  it("removes an origin from the list", () => {
    renderProject(makeConfig(["https://acme.com", "https://app.acme.com"]));
    fireEvent.click(screen.getByLabelText("Remove CORS origin 1"));
    expect(screen.queryByDisplayValue("https://acme.com")).not.toBeInTheDocument();
    expect(screen.getByDisplayValue("https://app.acme.com")).toBeInTheDocument();
  });

  it("drops a blank added row on save instead of persisting an empty entry", async () => {
    const { save } = renderProject(makeConfig(["https://acme.com"]));
    // Adding a row (even unfilled) surfaces the save bar, matching the
    // redirect-URL editor's behavior.
    fireEvent.click(screen.getByText("Add origin"));
    fireEvent.click(await screen.findByText("Save Changes"));
    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({
        server: expect.objectContaining({ cors: { origins: ["https://acme.com"] } }),
      })
    );
  });

  it("hides the save bar when there are no pending changes", () => {
    renderProject(makeConfig(["https://acme.com"]));
    expect(screen.queryByText("Save Changes")).not.toBeInTheDocument();
  });
});
