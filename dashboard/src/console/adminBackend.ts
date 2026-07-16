import * as api from "../api/client";
import { fullCapabilities, type ConsoleBackend } from "./backend";

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
};
