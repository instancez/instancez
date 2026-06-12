import { lazy } from "react";
import type { RouteObject } from "react-router-dom";

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

export const overviewRoutes = (): RouteObject[] => [{ index: true, element: <Overview /> }];

export const tablesRoutes = (): RouteObject[] => [
  { index: true, element: <Tables /> },
  { path: ":name", element: <TableDetail /> },
];

export const authRoutes = (): RouteObject[] => [{ index: true, element: <AuthPage /> }];

export const storageRoutes = (): RouteObject[] => [
  { index: true, element: <Storage /> },
  { path: ":name", element: <StorageDetail /> },
];

export const rpcRoutes = (): RouteObject[] => [
  { index: true, element: <Rpc /> },
  { path: ":name", element: <RpcDetail /> },
];

export const functionsRoutes = (): RouteObject[] => [
  { index: true, element: <Functions /> },
  { path: ":name", element: <FunctionDetail /> },
];

export const providersRoutes = (): RouteObject[] => [
  { index: true, element: <ProvidersPage /> },
];

export function consoleRoutes(): RouteObject[] {
  return [
    ...overviewRoutes(),
    { path: "tables", children: tablesRoutes() },
    { path: "auth", children: authRoutes() },
    { path: "storage", children: storageRoutes() },
    { path: "rpc", children: rpcRoutes() },
    { path: "functions", children: functionsRoutes() },
    { path: "providers", children: providersRoutes() },
  ];
}
