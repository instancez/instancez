export type { ConsoleBackend, Capabilities } from "./backend";
export { fullCapabilities } from "./backend";
export { adminBackend } from "./adminBackend";
export { BackendProvider, useBackend } from "./BackendContext";
export { ConsoleProvider } from "./ConsoleProvider";
export {
  consoleRoutes,
  overviewRoutes,
  tablesRoutes,
  authRoutes,
  storageRoutes,
  rpcRoutes,
  functionsRoutes,
  providersRoutes,
} from "./routes";
export { DiffViewer } from "../components/DiffViewer";
export { ConfirmSaveDialog } from "../components/ConfirmSaveDialog";
export { VarRow } from "../components/VarRow";
