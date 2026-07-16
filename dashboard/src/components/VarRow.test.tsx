import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { VarRow } from "./VarRow";

function renderRow(props: Partial<Parameters<typeof VarRow>[0]> = {}) {
  const onInputChange = vi.fn();
  renderWithChakra(
    <VarRow
      label="API key"
      name="INSTANCEZ_RESEND_API_KEY"
      isSet={false}
      canWrite={true}
      inputValue=""
      onInputChange={onInputChange}
      showEnvName={true}
      {...props}
    />
  );
  return { onInputChange };
}

describe("VarRow", () => {
  it("shows the input directly when the var is unset", () => {
    renderRow({ isSet: false });
    expect(screen.getByLabelText("INSTANCEZ_RESEND_API_KEY")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /override/i })).not.toBeInTheDocument();
  });

  it("hides the input behind an Override button when the var is already set", () => {
    renderRow({ isSet: true });
    expect(screen.queryByLabelText("INSTANCEZ_RESEND_API_KEY")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /override/i }));
    const input = screen.getByLabelText("INSTANCEZ_RESEND_API_KEY");
    expect(input).toBeInTheDocument();
    expect(input).toHaveAttribute("placeholder", expect.stringMatching(/override/i));
  });

  it("cancelling an override clears the staged value and hides the input", () => {
    const { onInputChange } = renderRow({ isSet: true, inputValue: "new-secret" });
    // staged value present → input is visible without clicking Override
    expect(screen.getByLabelText("INSTANCEZ_RESEND_API_KEY")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /keep current/i }));
    expect(onInputChange).toHaveBeenCalledWith("");
  });

  it("shows neither input nor Override button when writing is disabled", () => {
    renderRow({ isSet: true, canWrite: false });
    expect(screen.queryByLabelText("INSTANCEZ_RESEND_API_KEY")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /override/i })).not.toBeInTheDocument();
  });

  it("shows a masked tail (never the plaintext) when the var is set", () => {
    renderRow({ isSet: true, tail: "1a2b" });
    expect(screen.getByText("••••1a2b")).toBeInTheDocument();
    expect(screen.queryByLabelText("INSTANCEZ_RESEND_API_KEY")).not.toBeInTheDocument();
  });

  it("shows 'not set' when unset and writing is disabled", () => {
    renderRow({ isSet: false, canWrite: false });
    expect(screen.getByText("not set")).toBeInTheDocument();
    expect(screen.queryByLabelText("INSTANCEZ_RESEND_API_KEY")).not.toBeInTheDocument();
  });

  it("shows the env var name caption when showEnvName is true", () => {
    renderRow({ showEnvName: true });
    expect(screen.getByText("INSTANCEZ_RESEND_API_KEY")).toBeInTheDocument();
  });

  it("hides the env var name caption when showEnvName is false", () => {
    renderRow({ showEnvName: false });
    expect(screen.queryByText("INSTANCEZ_RESEND_API_KEY")).not.toBeInTheDocument();
  });
});
