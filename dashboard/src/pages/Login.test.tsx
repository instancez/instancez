import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Login } from "./Login";

vi.mock("../api/client", () => ({
  validateAdminKey: vi.fn(),
}));

import { validateAdminKey } from "../api/client";

const mockValidate = vi.mocked(validateAdminKey);

describe("Login", () => {
  const onSuccess = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders login form", () => {
    render(<Login onSuccess={onSuccess} />);
    expect(screen.getByText("Ultrabase Dashboard")).toBeInTheDocument();
    expect(screen.getByLabelText("Admin Key")).toBeInTheDocument();
    expect(screen.getByText("Continue")).toBeInTheDocument();
  });

  it("disables button when input is empty", () => {
    render(<Login onSuccess={onSuccess} />);
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
  });

  it("calls validateAdminKey and onSuccess for valid key", async () => {
    mockValidate.mockResolvedValue(true);
    render(<Login onSuccess={onSuccess} />);

    const input = screen.getByLabelText("Admin Key");
    await userEvent.type(input, "my-secret-key");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(mockValidate).toHaveBeenCalledWith("my-secret-key");
    expect(onSuccess).toHaveBeenCalledOnce();
  });

  it("shows error for invalid key", async () => {
    mockValidate.mockResolvedValue(false);
    render(<Login onSuccess={onSuccess} />);

    const input = screen.getByLabelText("Admin Key");
    await userEvent.type(input, "wrong-key");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByText(/Invalid admin key/)).toBeInTheDocument();
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it("shows loading state while validating", async () => {
    let resolve: (v: boolean) => void;
    mockValidate.mockImplementation(
      () => new Promise((r) => { resolve = r; })
    );
    render(<Login onSuccess={onSuccess} />);

    const input = screen.getByLabelText("Admin Key");
    await userEvent.type(input, "key");
    await userEvent.click(screen.getByRole("button"));

    expect(screen.getByText("Verifying...")).toBeInTheDocument();

    resolve!(true);
  });

  it("stores key in sessionStorage on success", async () => {
    mockValidate.mockResolvedValue(true);
    render(<Login onSuccess={onSuccess} />);

    await userEvent.type(screen.getByLabelText("Admin Key"), "stored-key");
    await userEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(sessionStorage.getItem("ultrabase_admin_key")).toBe("stored-key");
  });
});
