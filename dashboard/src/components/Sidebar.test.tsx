import { describe, expect, it } from "vitest";
import { SECTIONS } from "./Sidebar";

describe("Sidebar sections", () => {
  it("groups the areas in sidebar order", () => {
    expect(SECTIONS.map((s) => s.title)).toEqual(["Database", "Storage", "Code", "Security", "Integrations", "Manage"]);
  });

  it("puts Auth, Users and Advisor under Security, and Project under Manage", () => {
    const labels = (title: string) => SECTIONS.find((s) => s.title === title)?.items.map((i) => i.label);
    expect(labels("Security")).toEqual(["Auth", "Users", "Advisor"]);
    expect(labels("Manage")).toEqual(["Project"]);
  });

  it("lists every route once", () => {
    const routes = SECTIONS.flatMap((s) => s.items.map((i) => i.to));
    expect(new Set(routes).size).toBe(routes.length);
    expect(routes).toHaveLength(10);
  });
});
