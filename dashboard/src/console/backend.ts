import type { Config, ConfigStatus, StatsResponse, DiffResponse } from "../lib/types";
import type { EnvVarsResponse, ConfigPreview } from "../api/client";

/** What this consumer/deployment supports. Pages gate surfaces on these. */
export interface Capabilities {
  /** Config edits can be saved (instance: dashboard readwrite; platform: version+deploy). */
  canWriteConfig: boolean;
  /** Secrets can be written back (instance: dotenv file; platform: secret store + env sync). */
  canWriteSecrets: boolean;
  /** Function source files are editable (instance: local FS; platform: version artifact). */
  canEditFunctionCode: boolean;
  /** npm dependencies can be changed (instance: npm on host; platform: build pipeline). */
  canManageDeps: boolean;
  /** Live stats (row counts, storage usage) are available. */
  hasStats: boolean;
}

export function fullCapabilities(): Capabilities {
  return {
    canWriteConfig: true,
    canWriteSecrets: true,
    canEditFunctionCode: true,
    canManageDeps: true,
    hasStats: true,
  };
}

/**
 * Everything the console UI needs from "the backend". The instance dashboard
 * implements this against the admin API; instancez-platform implements it
 * against platform APIs (config saves create a new version, secrets go to the
 * platform store and sync to the runtime environment).
 *
 * Method shapes intentionally mirror api/client.ts so the admin adapter is a
 * pass-through and existing tests that mock ../api/client stay valid.
 */
export interface ConsoleBackend {
  capabilities: Capabilities;

  getConfig(): Promise<Config>;
  getConfigStatus(): Promise<ConfigStatus>;
  previewConfig(config: Omit<Config, "_checksum">): Promise<ConfigPreview>;
  putConfig(config: Omit<Config, "_checksum">, checksum: string): Promise<{ message: string; config_source?: string }>;

  // secrets — the write-back interface. Values are write-only; reads are existence-only via getEnvVars.
  getEnvVars(names?: string[]): Promise<EnvVarsResponse>;
  writeSecrets(vars: Record<string, string>): Promise<{ message: string }>;

  getKeys(): Promise<{ anon_key: string }>;
  getStats(): Promise<StatsResponse>;
  getConfigDiff(): Promise<DiffResponse>;

  getFunctionCode(name: string): Promise<{ content: string; file: string }>;
  putFunctionCode(name: string, content: string): Promise<{ message: string }>;
  checkFunctionFile(file: string): Promise<{ exists: boolean }>;
  getFunctionDeps(): Promise<{ dependencies: Record<string, string>; has_lock: boolean; readonly: boolean }>;
  postFunctionDeps(add: string[], remove: string[]): Promise<{ dependencies: Record<string, string>; has_lock: boolean; readonly: boolean }>;
}
