import { lazy } from "react";
import type { RouteObject } from "react-router-dom";

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
const Tables = lazy(() => import("../pages/Tables").then(m => ({ default: m.Tables })));
const TableDetail = lazy(() => import("../pages/TableDetail").then(m => ({ default: m.TableDetail })));
const AuthPage = lazy(() => import("../pages/Auth").then(m => ({ default: m.AuthPage })));
const Storage = lazy(() => import("../pages/Storage").then(m => ({ default: m.Storage })));
const StorageDetail = lazy(() => import("../pages/StorageDetail").then(m => ({ default: m.StorageDetail })));
const Rpc = lazy(() => import("../pages/Rpc").then(m => ({ default: m.Rpc })));
const RpcDetail = lazy(() => import("../pages/RpcDetail").then(m => ({ default: m.RpcDetail })));
const Functions = lazy(() => import("../pages/Functions").then(m => ({ default: m.Functions })));
const FunctionDetail = lazy(() => import("../pages/FunctionDetail").then(m => ({ default: m.FunctionDetail })));
const ProvidersPage = lazy(() => import("../pages/Providers").then(m => ({ default: m.ProvidersPage })));
const UsersPage = lazy(() => import("../pages/Users").then(m => ({ default: m.UsersPage })));

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
  ];
}
