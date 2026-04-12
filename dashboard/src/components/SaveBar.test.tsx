import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { SaveBar } from "./SaveBar";

describe("SaveBar", () => {
  it("renders nothing when not dirty and no errors", () => {
    const { container } = render(
      <SaveBar onSave={() => {}} saving={false} errors={[]} dirty={false} />
    );
    expect(container.innerHTML).toBe("");
  });

  it("renders save button when dirty", () => {
    render(
      <SaveBar onSave={() => {}} saving={false} errors={[]} dirty={true} />
    );
    expect(screen.getByText("Save Changes")).toBeInTheDocument();
  });

  it("calls onSave when button is clicked", () => {
    const onSave = vi.fn();
    render(
      <SaveBar onSave={onSave} saving={false} errors={[]} dirty={true} />
    );
    fireEvent.click(screen.getByText("Save Changes"));
    expect(onSave).toHaveBeenCalledOnce();
  });

  it("shows saving state", () => {
    render(
      <SaveBar onSave={() => {}} saving={true} errors={[]} dirty={true} />
    );
    expect(screen.getByText("Saving...")).toBeInTheDocument();
  });

  it("disables button while saving", () => {
    render(
      <SaveBar onSave={() => {}} saving={true} errors={[]} dirty={true} />
    );
    const button = screen.getByRole("button");
    expect(button).toBeDisabled();
  });

  it("renders validation errors", () => {
    render(
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
    render(
      <SaveBar onSave={() => {}} saving={false} errors={errors} dirty={true} />
    );
    expect(screen.getByText("+2 more errors")).toBeInTheDocument();
  });

  it("renders when errors exist even if not dirty", () => {
    render(
      <SaveBar
        onSave={() => {}}
        saving={false}
        errors={[{ path: "", message: "Something broke" }]}
        dirty={false}
      />
    );
    expect(screen.getByText("Something broke")).toBeInTheDocument();
  });
});
