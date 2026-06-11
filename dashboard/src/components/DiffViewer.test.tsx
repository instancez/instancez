import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { DiffViewer } from "./DiffViewer";

describe("DiffViewer", () => {
  it("shows empty message when no statements", () => {
    render(<DiffViewer statements={[]} isDestructive={false} />);
    expect(screen.getByText("No pending migrations")).toBeInTheDocument();
  });

  it("renders SQL statements", () => {
    const statements = [
      "ALTER TABLE todos ADD COLUMN priority integer;",
      "CREATE INDEX idx_todos_priority ON todos (priority);",
    ];
    render(<DiffViewer statements={statements} isDestructive={false} />);
    expect(screen.getByText(statements[0]!)).toBeInTheDocument();
    expect(screen.getByText(statements[1]!)).toBeInTheDocument();
  });

  it("shows destructive warning when isDestructive is true", () => {
    render(
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
    render(
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
    render(
      <DiffViewer statements={["DROP TABLE users;"]} isDestructive={true} />
    );
    const stmt = screen.getByText("DROP TABLE users;");
    expect(stmt.className).toContain("text-destructive");
  });

  it("highlights ADD/CREATE statements with success styling", () => {
    render(
      <DiffViewer
        statements={["CREATE TABLE new_table (id serial);"]}
        isDestructive={false}
      />
    );
    const stmt = screen.getByText("CREATE TABLE new_table (id serial);");
    expect(stmt.className).toContain("text-success");
  });
});
