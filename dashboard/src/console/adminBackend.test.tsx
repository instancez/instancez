import { describe, it, expect, vi } from "vitest";
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
