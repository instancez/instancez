import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { useConfigState } from "./useConfig";
import { ConfirmSaveDialog } from "../components/ConfirmSaveDialog";
import type { Config } from "../lib/types";
import * as api from "../api/client";

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

    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));
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
