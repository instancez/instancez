import { test, expect } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { MemoryRouter, createMemoryRouter, RouterProvider } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { Overview } from "./Overview";
import { ConsoleProvider } from "../console/ConsoleProvider";

function renderOverview(config: any, stats: any = null, keys: { publishable_key: string } = { publishable_key: "pub-xyz" }) {
  const backend = {
    capabilities: { hasStats: !!stats },
    getConfig: async () => config,
    getConfigStatus: async () => null,
    getStats: async () => stats ?? { tables: {}, storage: {} },
    getKeys: async () => keys,
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
      deals: { fields: [], indexes: [], rls: [{ operations: ["select"], using: "true" }] },
      activities: { fields: [], indexes: [], rls: [] },
    },
    storage: {}, rpc: {}, functions: {}, auth: null,
  };
  renderOverview(config);
  expect(await screen.findByText("deals")).toBeInTheDocument();
  expect(screen.getByText("activities")).toBeInTheDocument();
  // The Database card row flags the RLS-less table as "exposed" (one badge).
  expect(screen.getAllByText(/exposed/i)).toHaveLength(1);
});

test("Functions card counts code + database functions", async () => {
  const config = {
    project: { name: "Beacon", description: "" },
    tables: {}, storage: {}, auth: null,
    rpc: { calc_total: {} },
    functions: { notify_won_deal: {}, resize_avatar: {} },
  };
  renderOverview(config);
  expect(await screen.findByText(/Functions/)).toBeInTheDocument();
  expect(screen.getByText(/2/)).toBeInTheDocument();       // code functions
  expect(screen.getByText(/1 database/i)).toBeInTheDocument();
});

test("Storage usage hidden without stats capability", async () => {
  const config = { project:{name:"B",description:""}, tables:{}, storage:{ avatars:{} }, rpc:{}, functions:{}, auth:null };
  renderOverview(config, null); // hasStats false
  expect(await screen.findByText(/bucket/i)).toBeInTheDocument();
  expect(screen.queryByText(/used/i)).not.toBeInTheDocument();
});

test("publishable key shows creating hint while the backend has no key yet", async () => {
  const config = { project:{name:"B",description:""}, tables:{}, storage:{}, rpc:{}, functions:{}, auth:null };
  renderOverview(config, null, { publishable_key: "" });
  expect(await screen.findByText("https://x.instancez.app/api")).toBeInTheDocument();
  expect(screen.getByText(/creating/i)).toBeInTheDocument();
});

test("platform mount depth: Database card click navigates absolutely to /tables not /overview/tables", async () => {
  // Reproduces the platform host where Overview is mounted under /overview.
  // The old relative-path code resolved "tables" → /overview/tables (no route match).
  // The fixed absolute-path code resolves "/tables" → /tables (correct sibling).
  const config = {
    project: { name: "PlatformApp", description: "" },
    tables: {
      users_data: { fields: [], indexes: [], rls: [] },
    },
    storage: {}, rpc: {}, functions: {}, auth: null,
  } as any;
  const backend = {
    capabilities: { hasStats: false },
    getConfig: async () => config,
    getConfigStatus: async () => null,
    getStats: async () => ({ tables: {}, storage: {} }),
    getKeys: async () => ({ publishable_key: "pub-xyz" }),
    listUsers: async () => ({ users: [], total: 0 }),
  } as any;

  const router = createMemoryRouter(
    [
      {
        path: "overview",
        element: (
          <ConsoleProvider backend={backend} initialConfig={config} apiBaseUrl="https://x.instancez.app/api">
            <Overview />
          </ConsoleProvider>
        ),
      },
      { path: "tables", element: <div>TABLES PAGE</div> },
      { path: "tables/:name", element: <div>TABLES PAGE</div> },
    ],
    { initialEntries: ["/overview"] }
  );

  renderWithChakra(<RouterProvider router={router} />);

  // Wait for the Tables card title to appear, then click it.
  // CardTitle "Tables" is inside the Card whose onClick navigates to /tables.
  const tablesCardTitle = await screen.findByText("Tables");
  fireEvent.click(tablesCardTitle);

  // Absolute path fix: /tables. Old relative path would give /overview/tables → no match.
  expect(router.state.location.pathname).toBe("/tables");
});
