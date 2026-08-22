import * as api from "../api/client";
import { fullCapabilities, type ConsoleBackend } from "./backend";
import type { StorageListResult } from "../lib/types";

// Same-origin storage against the engine's Supabase-compatible /storage/v1.
const STORAGE = "/storage/v1";
async function storageFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const key = sessionStorage.getItem("instancez_secret_key") || "";
  if (!key) throw new Error("No secret key configured");
  // nosemgrep: rules_lgpl_javascript_ssrf_rule-node-ssrf -- host is the fixed app origin; only path segments are user data
  const res = await fetch(`${STORAGE}${path}`, {
    ...init,
    headers: { apikey: key, Authorization: `Bearer ${key}`, ...init.headers },
  });
  if (res.status === 401) {
    sessionStorage.removeItem("instancez_secret_key");
    window.location.reload();
    throw new Error("Unauthorized");
  }
  if (!res.ok) {
    const b = await res.json().catch(() => null);
    throw new Error(b?.error || b?.message || `HTTP ${res.status}`);
  }
  return res;
}

/**
 * The instance-dashboard backend: a pass-through to the admin API client.
 * IMPORTANT: delegate via the module namespace (api.fn(...)) — not by
 * destructured references — so test suites that vi.mock("../api/client")
 * keep intercepting calls.
 */
export const adminBackend: ConsoleBackend = {
  capabilities: fullCapabilities(),
  getConfig: () => api.getConfig(),
  getConfigStatus: () => api.getConfigStatus(),
  previewConfig: (config) => api.previewConfig(config),
  putConfig: (config, checksum) => api.putConfig(config, checksum),
  getEnvVars: (names) => api.getEnvVars(names),
  writeSecrets: (vars) => api.putDotenv(vars),
  getKeys: () => api.getKeys(),
  getStats: () => api.getStats(),
  getConfigDiff: () => api.getConfigDiff(),
  getFunctionCode: (name) => api.getFunctionCode(name),
  putFunctionCode: (name, content) => api.putFunctionCode(name, content),
  createFunction: async (name, config, code, checksum) => {
    // OSS has no atomic endpoint and no build cost, so sequencing the existing
    // two writes matches current behavior.
    await api.putConfig(config, checksum);
    await api.putFunctionCode(name, code);
  },
  checkFunctionFile: (file) => api.checkFunctionFile(file),
  getFunctionDeps: () => api.getFunctionDeps(),
  postFunctionDeps: (add, remove) => api.postFunctionDeps(add, remove),
  listUsers: (page, perPage) => api.adminListUsers(page, perPage),
  createUser: (email, password, emailConfirm) => api.adminCreateUser(email, password, emailConfirm),
  updateUser: (id, patch) => api.adminUpdateUser(id, patch),
  deleteUser: (id) => api.adminDeleteUser(id),

  runQuery: (sql) => api.adminRunQuery(sql),

  async listObjects(bucket, prefix, cursor): Promise<StorageListResult> {
    const res = await storageFetch(`/object/list-v2/${bucket}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ prefix, cursor, with_delimiter: true, limit: 100 }),
    });
    return res.json();
  },
  async uploadObject(bucket, path, file): Promise<void> {
    await storageFetch(`/object/${bucket}/${path}`, {
      method: "POST",
      headers: { "Content-Type": file.type || "application/octet-stream", "x-upsert": "true" },
      body: file,
    });
  },
  async signObjectUrl(bucket, path, expiresIn = 3600): Promise<{ signedURL: string }> {
    const res = await storageFetch(`/object/sign/${bucket}/${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ expiresIn }),
    });
    return res.json();
  },
  async moveObject(bucket, sourceKey, destinationKey): Promise<void> {
    await storageFetch(`/object/move`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ bucketId: bucket, sourceKey, destinationKey }),
    });
  },
  async deleteObjects(bucket, prefixes): Promise<void> {
    await storageFetch(`/object/${bucket}`, {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ prefixes }),
    });
  },
};
