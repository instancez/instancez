import { test, expect } from "vitest";
import { screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { renderWithChakra } from "../test/helpers";
import { Overview } from "./Overview";
import { ConsoleProvider } from "../console/ConsoleProvider";

function renderOverview(config: any, stats: any = null, keys: { anon_key: string } = { anon_key: "anon-xyz" }) {
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
      deals: { fields: [], indexes: [], rls: [{ operations: ["select"], check: "true" }] },
      activities: { fields: [], indexes: [], rls: [] },
    },
    storage: {}, rpc: {}, functions: {}, auth: null,
  };
  renderOverview(config);
  expect(await screen.findByText("deals")).toBeInTheDocument();
  expect(screen.getByText("activities")).toBeInTheDocument();
  // Both the Database card row and the Advisories card now show "exposed"; assert exactly two exist.
  expect(screen.getAllByText(/exposed/i)).toHaveLength(2);
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

test("Advisory card surfaces RLS-less tables", async () => {
  const config = {
    project: { name: "Beacon", description: "" },
    tables: { activities: { fields: [], indexes: [], rls: [] } },
    storage: {}, rpc: {}, functions: {}, auth: null,
  };
  renderOverview(config);
  expect(await screen.findByText(/1 advisory/i)).toBeInTheDocument();
  expect(screen.getByText(/no RLS policy/i)).toBeInTheDocument();
});

test("Advisory card shows clear state when all tables have RLS", async () => {
  const config = {
    project: { name: "Beacon", description: "" },
    tables: { deals: { fields: [], indexes: [], rls: [{ operations:["select"], check:"true" }] } },
    storage: {}, rpc: {}, functions: {}, auth: null,
  };
  renderOverview(config);
  expect(await screen.findByText(/no advisories/i)).toBeInTheDocument();
});

test("anon key shows publish hint in preview (empty key)", async () => {
  const config = { project:{name:"B",description:""}, tables:{}, storage:{}, rpc:{}, functions:{}, auth:null };
  renderOverview(config, null, { anon_key: "" });
  expect(await screen.findByText("https://x.instancez.app/api")).toBeInTheDocument();
  expect(screen.getByText(/publish to get/i)).toBeInTheDocument();
});
