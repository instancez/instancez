import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { adminBackend } from "./adminBackend";
import { BackendProvider, useBackend } from "./BackendContext";
import * as api from "../api/client";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<typeof api>();
  return { ...real, getEnvVars: vi.fn().mockResolvedValue({ vars: { X: { set: true } } }) };
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
