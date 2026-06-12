import type { RouteObject } from "react-router-dom";
import { Overview } from "../pages/Overview";
import { Tables } from "../pages/Tables";
import { TableDetail } from "../pages/TableDetail";
import { AuthPage } from "../pages/Auth";
import { Storage } from "../pages/Storage";
import { StorageDetail } from "../pages/StorageDetail";
import { Rpc } from "../pages/Rpc";
import { RpcDetail } from "../pages/RpcDetail";
import { Functions } from "../pages/Functions";
import { FunctionDetail } from "../pages/FunctionDetail";
import { ProvidersPage } from "../pages/Providers";

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
