import { describe, it, expect } from "vitest";
import type { RouteObject } from "react-router-dom";
import {
  overviewRoutes,
  tablesRoutes,
  authRoutes,
  storageRoutes,
  rpcRoutes,
  functionsRoutes,
  providersRoutes,
  type ConsoleRouteHandle,
} from "./routes";

// The shell (Layout) renders page chrome from each route's handle. Titles and
// descriptions that pages used to render via PageHeader now live here; these
// assertions relocate that behavioral coverage to the source of truth.
function handleOf(routes: RouteObject[], index: number): ConsoleRouteHandle {
  return routes[index]!.handle as ConsoleRouteHandle;
}

function titleString(
  h: ConsoleRouteHandle,
  params: Record<string, string | undefined> = {}
): string | null {
  return typeof h.title === "function" ? h.title(params) : h.title;
}

describe("console route handles (shell-owned titles)", () => {
  it("Overview opts out of a shell title (it renders its own in-content heading)", () => {
    expect(handleOf(overviewRoutes(), 0).title).toBeNull();
  });

  it("list pages expose static titles", () => {
    expect(titleString(handleOf(tablesRoutes(), 0))).toBe("Tables");
    expect(titleString(handleOf(authRoutes(), 0))).toBe("Authentication");
    expect(titleString(handleOf(storageRoutes(), 0))).toBe("Storage");
    expect(titleString(handleOf(rpcRoutes(), 0))).toBe("Database Functions");
    expect(titleString(handleOf(functionsRoutes(), 0))).toBe("Code Functions");
    expect(titleString(handleOf(providersRoutes(), 0))).toBe("Providers");
  });

  it("detail pages derive the title from the route param (entity name)", () => {
    expect(titleString(handleOf(tablesRoutes(), 1), { name: "todos" })).toBe("todos");
    expect(titleString(handleOf(storageRoutes(), 1), { name: "avatars" })).toBe("avatars");
    expect(titleString(handleOf(rpcRoutes(), 1), { name: "get_todos" })).toBe("get_todos");
    expect(titleString(handleOf(functionsRoutes(), 1), { name: "process_image" })).toBe(
      "process_image"
    );
  });
});
