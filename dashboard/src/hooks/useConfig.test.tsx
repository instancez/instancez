import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent, act } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { useConfigState } from "./useConfig";
import { ConfirmSaveDialog } from "../components/ConfirmSaveDialog";
import type { Config } from "../lib/types";
import * as api from "../api/client";
import { showSaveErrorToast } from "../components/SaveToast";

vi.mock("../api/client", async (importOriginal) => {
  const real = await importOriginal<typeof api>();
  return {
    ...real,
    getConfig: vi.fn(),
    getConfigStatus: vi.fn().mockResolvedValue({ dotenv_writable: true }),
    previewConfig: vi.fn(),
    putConfig: vi.fn(),
  };
});

vi.mock("../components/SaveToast", () => ({
  showSaveToast: vi.fn(),
  showSaveErrorToast: vi.fn(),
  SaveToast: () => null,
}));

const baseConfig = {
  version: 1,
  project: { name: "P", description: "" },
  _checksum: "abc",
} as unknown as Config & { _checksum: string };

function Harness({ onResult }: { onResult: (ok: boolean) => void }) {
  const state = useConfigState();
  if (state.loading) return <p>loading</p>;
  return (
    <div>
      <button
        onClick={async () => {
          const ok = await state.save(structuredClone(baseConfig), {
            dotenvChanges: [{ name: "INSTANCEZ_RESEND_API_KEY", tail: "abcd", isUpdate: false }],
          });
          onResult(ok);
        }}
      >
        do-save
      </button>
      {state.pendingSave && (
        <ConfirmSaveDialog
          current={state.pendingSave.current}
          proposed={state.pendingSave.proposed}
          dotenvChanges={state.pendingSave.dotenvChanges}
          saving={state.saving}
          onConfirm={state.confirmPendingSave}
          onCancel={state.cancelPendingSave}
        />
      )}
    </div>
  );
}

describe("useConfigState save confirmation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getConfig).mockResolvedValue(structuredClone(baseConfig));
    vi.mocked(api.getConfigStatus).mockResolvedValue({ dotenv_writable: true } as any);
    vi.mocked(api.previewConfig).mockResolvedValue({
      current: "version: 1\nproject:\n  name: old\n",
      proposed: "version: 1\nproject:\n  name: new\n",
    });
    vi.mocked(api.putConfig).mockResolvedValue({ config_source: "file" } as any);
  });

  it("shows the preview dialog before saving and saves on confirm", async () => {
    const onResult = vi.fn();
    renderWithChakra(<Harness onResult={onResult} />);
    await waitFor(() => expect(screen.getByText("do-save")).toBeInTheDocument());

    fireEvent.click(screen.getByText("do-save"));
    await waitFor(() => expect(screen.getByText("instancez.yaml")).toBeInTheDocument());
    expect(api.previewConfig).toHaveBeenCalled();
    expect(api.putConfig).not.toHaveBeenCalled();
    expect(screen.getByText(/••••abcd/)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/type CONFIRM/i), {
      target: { value: "CONFIRM" },
    });
    fireEvent.click(screen.getByRole("button", { name: /confirm & save/i }));
    await waitFor(() => expect(api.putConfig).toHaveBeenCalled());
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(true));
  });

  it("does not save when the dialog is cancelled", async () => {
    const onResult = vi.fn();
    renderWithChakra(<Harness onResult={onResult} />);
    await waitFor(() => expect(screen.getByText("do-save")).toBeInTheDocument());

    fireEvent.click(screen.getByText("do-save"));
    await waitFor(() => expect(screen.getByText("instancez.yaml")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(false));
    expect(api.putConfig).not.toHaveBeenCalled();
    expect(screen.queryByText("instancez.yaml")).not.toBeInTheDocument();
  });

  it("surfaces preview validation errors without opening the dialog", async () => {
    vi.mocked(api.previewConfig).mockRejectedValue(
      Object.assign(new Error("Bad Request"), {
        body: { errors: [{ path: "tables.x", message: "boom" }] },
      })
    );
    const onResult = vi.fn();
    renderWithChakra(<Harness onResult={onResult} />);
    await waitFor(() => expect(screen.getByText("do-save")).toBeInTheDocument());

    fireEvent.click(screen.getByText("do-save"));
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(false));
    expect(screen.queryByText("instancez.yaml")).not.toBeInTheDocument();
    expect(api.putConfig).not.toHaveBeenCalled();
  });
});

describe("useConfigState seeding", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("seeds from initialConfig and skips the getConfig mount fetch", async () => {
    const seeded = {
      version: 1,
      project: { name: "Seeded", description: "" },
      tables: {},
      _checksum: "seed-123",
    } as unknown as Config & { _checksum: string };

    function SeedHarness() {
      const state = useConfigState(seeded);
      return (
        <div>
          <span data-testid="loading">{String(state.loading)}</span>
          <span data-testid="name">{state.config?.project.name ?? ""}</span>
          <span data-testid="checksum">{state.checksum}</span>
        </div>
      );
    }

    renderWithChakra(<SeedHarness />);

    // No loading flash and the seeded config is present on first paint.
    expect(screen.getByTestId("loading").textContent).toBe("false");
    expect(screen.getByTestId("name").textContent).toBe("Seeded");
    expect(screen.getByTestId("checksum").textContent).toBe("seed-123");

    // The mount fetch for config must NOT have fired (status may, and is inert in the mock).
    await waitFor(() => expect(api.getConfigStatus).toHaveBeenCalled());
    expect(api.getConfig).not.toHaveBeenCalled();
  });
});

describe("useConfigState error toast", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getConfig).mockResolvedValue(structuredClone(baseConfig));
    vi.mocked(api.getConfigStatus).mockResolvedValue({ dotenv_writable: true } as any);
  });

  it("shows an error toast when preview rejects with validation errors", async () => {
    vi.mocked(api.previewConfig).mockRejectedValue(
      Object.assign(new Error("Bad Request"), {
        body: { errors: [{ path: "tables.x", message: "bad" }] },
      })
    );
    const onResult = vi.fn();
    renderWithChakra(<Harness onResult={onResult} />);
    await waitFor(() => expect(screen.getByText("do-save")).toBeInTheDocument());

    await act(async () => {
      fireEvent.click(screen.getByText("do-save"));
      await waitFor(() => expect(onResult).toHaveBeenCalledWith(false));
    });

    expect(showSaveErrorToast).toHaveBeenCalledWith(
      expect.objectContaining({ message: expect.stringContaining("1 validation error") })
    );
  });

  it("shows an error toast when putConfig rejects", async () => {
    vi.mocked(api.previewConfig).mockResolvedValue({
      current: "version: 1\n",
      proposed: "version: 1\n",
    });
    vi.mocked(api.putConfig).mockRejectedValue(
      Object.assign(new Error("server exploded"), { body: undefined })
    );
    const onResult = vi.fn();
    renderWithChakra(<Harness onResult={onResult} />);
    await waitFor(() => expect(screen.getByText("do-save")).toBeInTheDocument());

    fireEvent.click(screen.getByText("do-save"));
    await waitFor(() => expect(screen.getByText("instancez.yaml")).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText(/type CONFIRM/i), {
      target: { value: "CONFIRM" },
    });
    fireEvent.click(screen.getByRole("button", { name: /confirm & save/i }));
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(false));

    expect(showSaveErrorToast).toHaveBeenCalledWith(
      expect.objectContaining({ message: "server exploded" })
    );
  });
});
