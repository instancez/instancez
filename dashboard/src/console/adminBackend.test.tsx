import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { adminBackend } from "./adminBackend";
import { BackendProvider, useBackend } from "./BackendContext";
import * as api from "../api/client";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<typeof api>();
  return {
    ...real,
    getEnvVars: vi.fn().mockResolvedValue({ vars: { X: { set: true } } }),
    putDotenv: vi.fn().mockResolvedValue({ message: "ok" }),
    putConfig: vi.fn().mockResolvedValue({ message: "ok" }),
    putFunctionCode: vi.fn().mockResolvedValue({ message: "ok" }),
    adminRunQuery: vi.fn().mockResolvedValue({ columns: ["n"], rows: [[1]], row_count: 1 }),
  };
});

describe("adminBackend", () => {
  it("delegates to the api/client module (so vi.mock keeps intercepting)", async () => {
    const resp = await adminBackend.getEnvVars(["X"]);
    expect(resp.vars["X"]?.set).toBe(true);
    expect(api.getEnvVars).toHaveBeenCalledWith(["X"]);
  });

  it("advertises full capabilities", () => {
    expect(adminBackend.capabilities.canWriteSecrets).toBe(true);
  });

  it("routes writeSecrets to putDotenv", async () => {
    await adminBackend.writeSecrets({ K: "v" });
    expect(api.putDotenv).toHaveBeenCalledWith({ K: "v" });
  });

  it("createFunction writes config then function code", async () => {
    const putConfig = vi.mocked(api.putConfig).mockResolvedValue({ message: "ok" } as any);
    const putCode = vi.mocked(api.putFunctionCode).mockResolvedValue({ message: "ok" } as any);
    const cfg = { version: 1 } as any;

    await adminBackend.createFunction("orders", cfg, "code-src", "sum-1");

    expect(putConfig).toHaveBeenCalledWith(cfg, "sum-1");
    expect(putCode).toHaveBeenCalledWith("orders", "code-src");
    expect(putConfig.mock.invocationCallOrder[0]).toBeLessThan(putCode.mock.invocationCallOrder[0]!);
  });

  it("routes runQuery to adminRunQuery", async () => {
    const result = await adminBackend.runQuery("select 1");
    expect(api.adminRunQuery).toHaveBeenCalledWith("select 1");
    expect(result).toEqual({ columns: ["n"], rows: [[1]], row_count: 1 });
  });

  it("useBackend defaults to adminBackend and can be overridden", () => {
    function Probe() {
      const b = useBackend();
      return <span>{b.capabilities.canWriteConfig ? "yes" : "no"}</span>;
    }
    render(<Probe />);
    expect(screen.getByText("yes")).toBeInTheDocument();

    const custom = { ...adminBackend, capabilities: { ...adminBackend.capabilities, canWriteConfig: false } };
    render(
      <BackendProvider backend={custom}>
        <Probe />
      </BackendProvider>
    );
    expect(screen.getByText("no")).toBeInTheDocument();
  });
});

describe("storage requests", () => {
  const mockFetch = vi.fn();

  beforeEach(() => {
    vi.stubGlobal("fetch", mockFetch);
  });

  afterEach(() => {
    mockFetch.mockReset();
    vi.unstubAllGlobals();
    sessionStorage.clear();
  });

  it("sends the secret key as apikey/Authorization plus the upload's own headers", async () => {
    sessionStorage.setItem("instancez_secret_key", "k");
    mockFetch.mockResolvedValueOnce({ ok: true, json: async () => ({}) });

    const file = new File(["x"], "p.txt", { type: "text/plain" });
    await adminBackend.uploadObject("b", "p.txt", file);

    expect(mockFetch).toHaveBeenCalledWith(
      "/storage/v1/object/b/p.txt",
      expect.objectContaining({
        headers: expect.objectContaining({
          apikey: "k",
          Authorization: "Bearer k",
          "x-upsert": "true",
          "Content-Type": "text/plain",
        }),
      })
    );
  });

  it("rejects with no secret key configured when none is set", async () => {
    await expect(
      adminBackend.uploadObject("b", "p.txt", new File(["x"], "p.txt"))
    ).rejects.toThrow("No secret key configured");
    expect(mockFetch).not.toHaveBeenCalled();
  });

  it("clears the session key and logs out on a 401 from a storage op", async () => {
    const reloadMock = vi.fn();
    Object.defineProperty(window, "location", {
      value: { reload: reloadMock },
      writable: true,
    });

    sessionStorage.setItem("instancez_secret_key", "k");
    mockFetch.mockResolvedValueOnce({ ok: false, status: 401, json: async () => ({}) });

    await expect(
      adminBackend.uploadObject("b", "p.txt", new File(["x"], "p.txt"))
    ).rejects.toThrow("Unauthorized");
    expect(sessionStorage.getItem("instancez_secret_key")).toBeNull();
    expect(reloadMock).toHaveBeenCalledTimes(1);
  });
});
