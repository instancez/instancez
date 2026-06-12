import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ConfirmSaveDialog } from "./ConfirmSaveDialog";

const CURRENT = "version: 1\nproject:\n  name: old\n";
const PROPOSED = "version: 1\nproject:\n  name: new\n";

describe("ConfirmSaveDialog", () => {
  it("shows removed and added lines from the instancez.yaml diff", () => {
    render(
      <ConfirmSaveDialog
        current={CURRENT}
        proposed={PROPOSED}
        dotenvChanges={[]}
        saving={false}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />
    );
    expect(screen.getByText("instancez.yaml")).toBeInTheDocument();
    expect(screen.getByText(/name: old/)).toBeInTheDocument();
    expect(screen.getByText(/name: new/)).toBeInTheDocument();
  });

  it("notes when instancez.yaml is unchanged", () => {
    render(
      <ConfirmSaveDialog
        current={CURRENT}
        proposed={CURRENT}
        dotenvChanges={[]}
        saving={false}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />
    );
    expect(screen.getByText(/no changes/i)).toBeInTheDocument();
  });

  it("lists .env changes masked with a last-4 tail", () => {
    render(
      <ConfirmSaveDialog
        current={CURRENT}
        proposed={CURRENT}
        dotenvChanges={[
          { name: "INSTANCEZ_RESEND_API_KEY", tail: "abcd", isUpdate: false },
          { name: "AWS_SECRET_ACCESS_KEY", tail: "wxyz", isUpdate: true },
        ]}
        saving={false}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />
    );
    expect(screen.getByText(".env")).toBeInTheDocument();
    expect(screen.getByText(/INSTANCEZ_RESEND_API_KEY/)).toBeInTheDocument();
    expect(screen.getByText(/••••abcd/)).toBeInTheDocument();
    expect(screen.getByText("added")).toBeInTheDocument();
    expect(screen.getByText(/••••wxyz/)).toBeInTheDocument();
    expect(screen.getByText("updated")).toBeInTheDocument();
  });

  it("wires confirm and cancel buttons", () => {
    const onConfirm = vi.fn();
    const onCancel = vi.fn();
    render(
      <ConfirmSaveDialog
        current={CURRENT}
        proposed={PROPOSED}
        dotenvChanges={[]}
        saving={false}
        onConfirm={onConfirm}
        onCancel={onCancel}
      />
    );
    fireEvent.click(screen.getByRole("button", { name: /confirm/i }));
    expect(onConfirm).toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onCancel).toHaveBeenCalled();
  });
});
