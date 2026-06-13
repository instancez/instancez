import { describe, it, expect, vi } from "vitest";
import { screen, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithChakra } from "../test/helpers";
import { TagInput } from "./TagInput";

describe("TagInput", () => {
  it("renders existing tags", () => {
    renderWithChakra(<TagInput value={["react", "vue"]} onChange={() => {}} />);
    expect(screen.getByText("react")).toBeInTheDocument();
    expect(screen.getByText("vue")).toBeInTheDocument();
  });

  it("adds a tag on Enter", async () => {
    const onChange = vi.fn();
    renderWithChakra(<TagInput value={[]} onChange={onChange} />);

    const input = screen.getByRole("textbox");
    await userEvent.type(input, "newtag{Enter}");

    expect(onChange).toHaveBeenCalledWith(["newtag"]);
  });

  it("adds a tag on comma", async () => {
    const onChange = vi.fn();
    renderWithChakra(<TagInput value={[]} onChange={onChange} />);

    const input = screen.getByRole("textbox");
    await userEvent.type(input, "tag1,");

    expect(onChange).toHaveBeenCalledWith(["tag1"]);
  });

  it("removes a tag when X button is clicked", async () => {
    const onChange = vi.fn();
    renderWithChakra(<TagInput value={["a", "b", "c"]} onChange={onChange} />);

    const removeButtons = screen.getAllByRole("button");
    await userEvent.click(removeButtons[1]!); // Remove "b"

    expect(onChange).toHaveBeenCalledWith(["a", "c"]);
  });

  it("removes last tag on Backspace when input is empty", async () => {
    const onChange = vi.fn();
    renderWithChakra(<TagInput value={["a", "b"]} onChange={onChange} />);

    const input = screen.getByRole("textbox");
    await userEvent.click(input);
    await userEvent.keyboard("{Backspace}");

    expect(onChange).toHaveBeenCalledWith(["a"]);
  });

  it("does not add duplicate tags", async () => {
    const onChange = vi.fn();
    renderWithChakra(<TagInput value={["existing"]} onChange={onChange} />);

    const input = screen.getByRole("textbox");
    await userEvent.type(input, "existing{Enter}");

    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not add empty tags", async () => {
    const onChange = vi.fn();
    renderWithChakra(<TagInput value={[]} onChange={onChange} />);

    const input = screen.getByRole("textbox");
    await userEvent.type(input, "{Enter}");

    expect(onChange).not.toHaveBeenCalled();
  });

  it("shows placeholder when no tags", () => {
    renderWithChakra(
      <TagInput value={[]} onChange={() => {}} placeholder="Add tags..." />
    );
    expect(screen.getByPlaceholderText("Add tags...")).toBeInTheDocument();
  });

  it("hides placeholder when tags exist", () => {
    renderWithChakra(
      <TagInput value={["one"]} onChange={() => {}} placeholder="Add tags..." />
    );
    expect(screen.queryByPlaceholderText("Add tags...")).not.toBeInTheDocument();
  });

  it("shows filtered suggestions on focus", async () => {
    renderWithChakra(
      <TagInput
        value={[]}
        onChange={() => {}}
        suggestions={["react", "vue", "svelte"]}
      />
    );

    const input = screen.getByRole("textbox");
    await userEvent.click(input);
    await userEvent.type(input, "re");

    expect(screen.getByText("react")).toBeInTheDocument();
    expect(screen.queryByText("vue")).not.toBeInTheDocument();
  });
});
