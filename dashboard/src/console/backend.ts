import type { Config, ConfigStatus, StatsResponse, DiffResponse, AdminUser } from "../lib/types";
import type { EnvVarsResponse, ConfigPreview } from "../api/client";

/** What this consumer/deployment supports. Pages gate surfaces on these. */
export interface Capabilities {
  /** Config edits can be saved (instance: dashboard readwrite; platform: version+deploy). */
  canWriteConfig: boolean;
  /** Secrets can be written back (instance: dotenv file; platform: secret store + env sync). */
  canWriteSecrets: boolean;
  /** A function's source file *contents* can be edited in place (instance: local
   *  FS; platform: draft artifact). Distinct from canEditFunctionFile. */
  canEditFunctionCode: boolean;
  /** A function's source file *path* can be repointed (instance: local FS, the
   *  user owns the layout; platform: the file is a managed artifact at a fixed
   *  path, so the field is read-only). Distinct from canEditFunctionCode. */
  canEditFunctionFile: boolean;
  /** npm dependencies can be changed (instance: npm on host; platform: build pipeline). */
  canManageDeps: boolean;
  /** Live stats (row counts, storage usage) are available. */
  hasStats: boolean;
  /** The env var name shown next to a secret is one the user can actually set
   *  (instance: true — self-hosters manage their own .env; platform: false —
   *  secrets live in the platform's store and the name is an internal detail). */
  showsEnvVarNames: boolean;
}

export function fullCapabilities(): Capabilities {
  return {
    canWriteConfig: true,
    canWriteSecrets: true,
    canEditFunctionCode: true,
    canEditFunctionFile: true,
    canManageDeps: true,
    hasStats: true,
    showsEnvVarNames: true,
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

  // config
  getConfig(): Promise<Config>;
  getConfigStatus(): Promise<ConfigStatus>;
  previewConfig(config: Omit<Config, "_checksum">): Promise<ConfigPreview>;
  putConfig(
    config: Omit<Config, "_checksum">,
    checksum: string
  ): Promise<{ message: string; config_source?: string }>;

  // secrets — the write-back interface. Values are write-only; reads are existence-only via getEnvVars.
  getEnvVars(names?: string[]): Promise<EnvVarsResponse>;
  writeSecrets(vars: Record<string, string>): Promise<{ message: string }>;

  // keys / stats / diff
  getKeys(): Promise<{ anon_key: string }>;
  getStats(): Promise<StatsResponse>;
  getConfigDiff(): Promise<DiffResponse>;

  // code functions
  getFunctionCode(name: string): Promise<{ content: string; file: string }>;
  putFunctionCode(name: string, content: string): Promise<{ message: string }>;
  checkFunctionFile(file: string): Promise<{ exists: boolean }>;
  getFunctionDeps(): Promise<{
    dependencies: Record<string, string>;
    has_lock: boolean;
    readonly: boolean;
  }>;
  postFunctionDeps(
    add: string[],
    remove: string[]
  ): Promise<{ dependencies: Record<string, string>; has_lock: boolean; readonly: boolean }>;

  // user management — AdminUser.banned_until is "" (empty string) when not banned, never null/undefined
  listUsers(page?: number, perPage?: number): Promise<{ users: AdminUser[]; total: number }>;
  createUser(email: string, password: string, emailConfirm: boolean): Promise<AdminUser>;
  updateUser(
    id: string,
    patch: { email?: string; password?: string; ban_duration?: string; email_confirm?: boolean }
  ): Promise<AdminUser>;
  deleteUser(id: string): Promise<void>;
}
