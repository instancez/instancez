import { createContext, useContext, type ReactNode } from "react";
import type { ConsoleBackend } from "./backend";
import { adminBackend } from "./adminBackend";

/** Consumers (the platform app) inject their backend here; the instance
 * dashboard relies on the adminBackend default and needs no provider. */
const BackendContext = createContext<ConsoleBackend>(adminBackend);

export function BackendProvider({ backend, children }: { backend: ConsoleBackend; children: ReactNode }) {
  return <BackendContext.Provider value={backend}>{children}</BackendContext.Provider>;
}

export function useBackend(): ConsoleBackend {
  return useContext(BackendContext);
}
