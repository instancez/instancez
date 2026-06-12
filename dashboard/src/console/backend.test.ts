import { describe, it, expect } from "vitest";
import { fullCapabilities, type ConsoleBackend } from "./backend";

describe("ConsoleBackend types", () => {
  it("fullCapabilities enables every surface", () => {
    const caps = fullCapabilities();
    expect(caps.canWriteConfig).toBe(true);
    expect(caps.canWriteSecrets).toBe(true);
    expect(caps.canEditFunctionCode).toBe(true);
    expect(caps.canManageDeps).toBe(true);
    expect(caps.hasStats).toBe(true);
  });

  it("a stub satisfies the interface", () => {
    const stub: ConsoleBackend = {
      capabilities: fullCapabilities(),
      getConfig: async () => ({ version: 1 }) as any,
      getConfigStatus: async () => ({ dotenv_writable: false }) as any,
      previewConfig: async () => ({ current: "", proposed: "" }),
      putConfig: async () => ({ message: "" }),
      getEnvVars: async () => ({ vars: {} }),
      writeSecrets: async () => ({ message: "" }),
      getKeys: async () => ({ anon_key: "" }),
      getStats: async () => ({ tables: {}, storage: {} }) as any,
      getConfigDiff: async () => ({ statements: [], is_destructive: false }) as any,
      getFunctionCode: async () => ({ content: "", file: "" }),
      putFunctionCode: async () => ({ message: "" }),
      checkFunctionFile: async () => ({ exists: true }),
      getFunctionDeps: async () => ({ dependencies: {}, has_lock: false, readonly: true }),
      postFunctionDeps: async () => ({ dependencies: {}, has_lock: false, readonly: true }),
    };
    expect(stub.capabilities.canWriteConfig).toBe(true);
  });
});
