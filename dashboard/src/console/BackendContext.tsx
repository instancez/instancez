import { createContext, useContext, type ReactNode } from "react";
import type { ConsoleBackend } from "./backend";
import { adminBackend } from "./adminBackend";

const BackendContext = createContext<ConsoleBackend>(adminBackend);

/** Consumers (the platform app) inject their backend here; the instance
 * dashboard relies on the adminBackend default and needs no provider. */
export function BackendProvider({ backend, children }: { backend: ConsoleBackend; children: ReactNode }) {
  return <BackendContext.Provider value={backend}>{children}</BackendContext.Provider>;
}

/** Returns the ConsoleBackend for this consumer. Defaults to adminBackend. */
export function useBackend(): ConsoleBackend {
  return useContext(BackendContext);
}
