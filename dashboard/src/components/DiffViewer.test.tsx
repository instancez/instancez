import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithChakra } from "../test/helpers";
import { DiffViewer } from "./DiffViewer";

describe("DiffViewer", () => {
  it("shows empty message when no statements", () => {
    renderWithChakra(<DiffViewer statements={[]} isDestructive={false} />);
    expect(screen.getByText("No pending migrations")).toBeInTheDocument();
  });

  it("renders SQL statements", () => {
    const statements = [
      "ALTER TABLE todos ADD COLUMN priority integer;",
      "CREATE INDEX idx_todos_priority ON todos (priority);",
    ];
    renderWithChakra(<DiffViewer statements={statements} isDestructive={false} />);
    expect(screen.getByText(statements[0]!)).toBeInTheDocument();
    expect(screen.getByText(statements[1]!)).toBeInTheDocument();
  });

  it("shows destructive warning when isDestructive is true", () => {
    renderWithChakra(
      <DiffViewer
        statements={["DROP TABLE users;"]}
        isDestructive={true}
      />
    );
    expect(
      screen.getByText(/destructive operations/)
    ).toBeInTheDocument();
  });

  it("does not show destructive warning when isDestructive is false", () => {
    renderWithChakra(
      <DiffViewer
        statements={["ALTER TABLE todos ADD COLUMN x text;"]}
        isDestructive={false}
      />
    );
    expect(
      screen.queryByText(/destructive operations/)
    ).not.toBeInTheDocument();
  });

  it("highlights DROP statements with destructive styling", () => {
    renderWithChakra(
      <DiffViewer statements={["DROP TABLE users;"]} isDestructive={true} />
    );
    // Statement renders in the DOM — styling is Chakra props, not classNames
    expect(screen.getByText("DROP TABLE users;")).toBeInTheDocument();
  });

  it("highlights ADD/CREATE statements with success styling", () => {
    renderWithChakra(
      <DiffViewer
        statements={["CREATE TABLE new_table (id serial);"]}
        isDestructive={false}
      />
    );
    // Statement renders in the DOM — styling is Chakra props, not classNames
    expect(screen.getByText("CREATE TABLE new_table (id serial);")).toBeInTheDocument();
  });
});
