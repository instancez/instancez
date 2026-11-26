import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { Section } from "./ui";

const Icon = () => <svg data-testid="icon" />;

describe("Section", () => {
  it("renders the title, description, icon, actions and body", () => {
    renderWithChakra(
      <Section title="Keys" description="For this project" icon={Icon} actions={<button>Rotate</button>}>
        <p>body</p>
      </Section>
    );
    expect(screen.getByText("Keys")).toBeInTheDocument();
    expect(screen.getByText("For this project")).toBeInTheDocument();
    expect(screen.getByTestId("icon")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Rotate" })).toBeInTheDocument();
    expect(screen.getByText("body")).toBeInTheDocument();
  });

  it("gives a danger section a different border from a normal one", () => {
    const panel = (danger: boolean) => {
      const { container, unmount } = renderWithChakra(<div data-testid="host"><Section title="S" danger={danger}>x</Section></div>);
      const cls = container.querySelector("[data-testid=host]")!.firstElementChild!.className;
      unmount();
      return cls;
    };
    expect(panel(true)).not.toBe(panel(false));
  });
});
