import { lazy } from "react";
import type { RouteObject } from "react-router-dom";

// The area index pages are tiny config-backed lists with no heavy deps, so we
// import them statically rather than splitting each into its own chunk. A
// per-page chunk buys almost nothing at first load and adds a round-trip on the
// first click of each tab. Auth stays lazy below because it pulls in CodeMirror,
// as do the detail sub-pages.
import { Tables } from "../pages/Tables";
import { Storage } from "../pages/Storage";
import { Rpc } from "../pages/Rpc";
import { Functions } from "../pages/Functions";

/**
 * Route metadata the OSS shell reads (via useMatches) to render page chrome
 * (title + description) outside the page itself. Pages are chrome-free; the
 * host owns the title. A `null` title means "render no shell title" — the page
 * supplies its own in-content heading (e.g. Overview's project name).
 */
export interface ConsoleRouteHandle {
  title: string | ((params: Record<string, string | undefined>) => string) | null;
  description?: string;
}

const Overview = lazy(() => import("../pages/Overview").then(m => ({ default: m.Overview })));
const TableDetail = lazy(() => import("../pages/TableDetail").then(m => ({ default: m.TableDetail })));
const AuthPage = lazy(() => import("../pages/Auth").then(m => ({ default: m.AuthPage })));
const StorageDetail = lazy(() => import("../pages/StorageDetail").then(m => ({ default: m.StorageDetail })));
const RpcDetail = lazy(() => import("../pages/RpcDetail").then(m => ({ default: m.RpcDetail })));
const FunctionDetail = lazy(() => import("../pages/FunctionDetail").then(m => ({ default: m.FunctionDetail })));
const ProvidersPage = lazy(() => import("../pages/Providers").then(m => ({ default: m.ProvidersPage })));
const UsersPage = lazy(() => import("../pages/Users").then(m => ({ default: m.UsersPage })));
const ProjectPage = lazy(() => import("../pages/Project").then(m => ({ default: m.ProjectPage })));

export const overviewRoutes = (): RouteObject[] => [
  { index: true, element: <Overview />, handle: { title: null } satisfies ConsoleRouteHandle },
];

export const tablesRoutes = (): RouteObject[] => [
  // List-page descriptions were dynamic counts; that count now lives in the
  // page's in-content toolbar, so the handle carries only a title.
  { index: true, element: <Tables />, handle: { title: "Tables" } satisfies ConsoleRouteHandle },
  { path: "new", element: <TableDetail />, handle: { title: "New table" } satisfies ConsoleRouteHandle },
  { path: ":name", element: <TableDetail />, handle: { title: (p) => p.name ?? "Table" } satisfies ConsoleRouteHandle },
];

export const authRoutes = (): RouteObject[] => [
  { index: true, element: <AuthPage />, handle: { title: "Authentication" } satisfies ConsoleRouteHandle },
];

export const storageRoutes = (): RouteObject[] => [
  { index: true, element: <Storage />, handle: { title: "Storage" } satisfies ConsoleRouteHandle },
  { path: ":name", element: <StorageDetail />, handle: { title: (p) => p.name ?? "Bucket" } satisfies ConsoleRouteHandle },
];

export const rpcRoutes = (): RouteObject[] => [
  { index: true, element: <Rpc />, handle: { title: "Database Functions" } satisfies ConsoleRouteHandle },
  { path: ":name", element: <RpcDetail />, handle: { title: (p) => p.name ?? "Function" } satisfies ConsoleRouteHandle },
];

export const functionsRoutes = (): RouteObject[] => [
  { index: true, element: <Functions />, handle: { title: "Code Functions" } satisfies ConsoleRouteHandle },
  { path: "new", element: <FunctionDetail />, handle: { title: "New function" } satisfies ConsoleRouteHandle },
  { path: ":name", element: <FunctionDetail />, handle: { title: (p) => p.name ?? "Function" } satisfies ConsoleRouteHandle },
];

export const providersRoutes = (): RouteObject[] => [
  { index: true, element: <ProvidersPage />, handle: { title: "Providers" } satisfies ConsoleRouteHandle },
];

export const usersRoutes = (): RouteObject[] => [
  {
    index: true,
    element: <UsersPage />,
    handle: { title: "Users" } satisfies ConsoleRouteHandle,
  },
];

export const projectRoutes = (): RouteObject[] => [
  { index: true, element: <ProjectPage />, handle: { title: "Project" } satisfies ConsoleRouteHandle },
];

export function consoleRoutes(): RouteObject[] {
  return [
    ...overviewRoutes(),
    { path: "tables", children: tablesRoutes() },
    { path: "auth", children: authRoutes() },
    { path: "users", children: usersRoutes() },
    { path: "storage", children: storageRoutes() },
    { path: "rpc", children: rpcRoutes() },
    { path: "functions", children: functionsRoutes() },
    { path: "providers", children: providersRoutes() },
    { path: "project", children: projectRoutes() },
  ];
}
