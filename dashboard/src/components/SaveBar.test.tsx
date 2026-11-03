import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { SaveBar } from "./SaveBar";
import { ConfigContext } from "../hooks/useConfig";

describe("SaveBar", () => {
  it("renders nothing when not dirty and no errors", () => {
    renderWithChakra(
      <SaveBar onSave={() => {}} saving={false} errors={[]} dirty={false} />
    );
    expect(screen.queryByText("Save Changes")).toBeNull();
  });

  it("renders save button when dirty", () => {
    renderWithChakra(
      <SaveBar onSave={() => {}} saving={false} errors={[]} dirty={true} />
    );
    expect(screen.getByText("Save Changes")).toBeInTheDocument();
  });

  it("calls onSave when button is clicked", () => {
    const onSave = vi.fn();
    renderWithChakra(
      <SaveBar onSave={onSave} saving={false} errors={[]} dirty={true} />
    );
    fireEvent.click(screen.getByText("Save Changes"));
    expect(onSave).toHaveBeenCalledOnce();
  });

  it("shows saving state", () => {
    renderWithChakra(
      <SaveBar onSave={() => {}} saving={true} errors={[]} dirty={true} />
    );
    expect(screen.getByText("Saving...")).toBeInTheDocument();
  });

  it("disables button while saving", () => {
    renderWithChakra(
      <SaveBar onSave={() => {}} saving={true} errors={[]} dirty={true} />
    );
    const button = screen.getByRole("button");
    expect(button).toBeDisabled();
  });

  it("renders validation errors", () => {
    renderWithChakra(
      <SaveBar
        onSave={() => {}}
        saving={false}
        errors={[
          { path: "tables.todos", message: "Invalid field type" },
          { path: "auth", message: "Missing JWT expiry", suggestion: "Add jwt_expiry" },
        ]}
        dirty={true}
      />
    );
    expect(screen.getByText("Invalid field type")).toBeInTheDocument();
    expect(screen.getByText("Missing JWT expiry")).toBeInTheDocument();
    expect(screen.getByText(/Add jwt_expiry/)).toBeInTheDocument();
  });

  it("shows overflow count for many errors", () => {
    const errors = Array.from({ length: 5 }, (_, i) => ({
      path: `field_${i}`,
      message: `Error ${i}`,
    }));
    renderWithChakra(
      <SaveBar onSave={() => {}} saving={false} errors={errors} dirty={true} />
    );
    expect(screen.getByText("+2 more errors")).toBeInTheDocument();
  });

  it("renders when errors exist even if not dirty", () => {
    renderWithChakra(
      <SaveBar
        onSave={() => {}}
        saving={false}
        errors={[{ path: "", message: "Something broke" }]}
        dirty={false}
      />
    );
    expect(screen.getByText("Something broke")).toBeInTheDocument();
  });

  it("disables save and says why when the config is read-only", () => {
    const onSave = vi.fn();
    const ctx = { readOnly: true } as unknown as React.ContextType<typeof ConfigContext>;
    renderWithChakra(
      <ConfigContext.Provider value={ctx}>
        <SaveBar onSave={onSave} saving={false} errors={[]} dirty={true} />
      </ConfigContext.Provider>
    );
    const btn = screen.getByRole("button", { name: /save changes/i });
    expect(btn).toBeDisabled();
    expect(screen.getByText(/uses include:/i)).toBeInTheDocument();
    fireEvent.click(btn);
    expect(onSave).not.toHaveBeenCalled();
  });
});
