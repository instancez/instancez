import { screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { Overview } from "./Overview";
import { ConsoleProvider } from "../console/ConsoleProvider";

function renderOverview(config: any, stats: any = null) {
  const backend = {
    capabilities: { hasStats: !!stats },
    getConfig: async () => config,
    getConfigStatus: async () => null,
    getStats: async () => stats ?? { tables: {}, storage: {} },
    getKeys: async () => ({ anon_key: "anon-xyz" }),
    listUsers: async () => ({ users: [], total: 0 }),
  } as any;
  return renderWithChakra(
    <MemoryRouter>
      <ConsoleProvider backend={backend} initialConfig={config} apiBaseUrl="https://x.instancez.app/api">
        <Overview />
      </ConsoleProvider>
    </MemoryRouter>
  );
}

test("Database card lists tables and flags exposed ones", async () => {
  const config = {
    project: { name: "Beacon", description: "" },
    tables: {
      deals: { fields: [], indexes: [], rls: [{ operations: ["select"], check: "true" }] },
      activities: { fields: [], indexes: [], rls: [] },
    },
    storage: {}, rpc: {}, functions: {}, auth: null,
  };
  renderOverview(config);
  expect(await screen.findByText("deals")).toBeInTheDocument();
  expect(screen.getByText("activities")).toBeInTheDocument();
  expect(screen.getByText(/exposed/i)).toBeInTheDocument();
});
